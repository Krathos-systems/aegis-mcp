// Command aegis-mcp is a Model Context Protocol server that lets an AI client
// read and change an organisation's Aegis context and configuration.
//
// It speaks MCP over stdio: the AI client starts it as a subprocess. stdout
// carries the protocol, so everything meant for a person goes to stderr.
package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
	"github.com/Krathos-systems/aegis-mcp/internal/config"
	"github.com/Krathos-systems/aegis-mcp/internal/setup"
	"github.com/Krathos-systems/aegis-mcp/internal/tools"
)

// The Aegis mark, sent to clients that show an icon for a server. Inline as data
// URIs, so a client never has to fetch it.
var (
	//go:embed assets/aegis-mark.svg
	markSVG []byte
	//go:embed assets/aegis-mark-256.png
	markPNG []byte
	icons   = []mcp.Icon{
		{Source: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(markSVG), MIMEType: "image/svg+xml", Sizes: []string{"any"}},
		{Source: "data:image/png;base64," + base64.StdEncoding.EncodeToString(markPNG), MIMEType: "image/png", Sizes: []string{"256x256"}},
	}
)

// version is set at build time with -ldflags "-X main.version=<tag>".
var version = "dev"

// main only wires the process to run; everything testable is in run.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

// run is the whole program. in and out carry the MCP protocol; errw is for
// people. It returns the process exit code.
func run(ctx context.Context, args []string, in io.ReadCloser, out io.WriteCloser, errw io.Writer, getenv func(string) string) int {
	if len(args) > 0 && args[0] == "setup" {
		return runSetup(ctx, args[1:], in, out, errw, getenv)
	}

	fs := flag.NewFlagSet("aegis-mcp", flag.ContinueOnError)
	fs.SetOutput(errw)
	showVersion := fs.Bool("version", false, "print the version and exit")
	readOnly := fs.Bool("read-only", false, "offer only tools that read (also AEGIS_READ_ONLY=true)")
	fs.Usage = func() {
		fmt.Fprintf(errw, `aegis-mcp %s: an MCP server for Aegis, over stdio.

  aegis-mcp setup   connect to Aegis and register with your AI clients
  aegis-mcp         run the server (your AI client does this)

The server uses what setup saved, unless these are set:
  AEGIS_SERVER      your Aegis address, e.g. https://acme.example.com
  AEGIS_TOKEN       an Aegis API token (aegt_…), or
  AEGIS_TOKEN_FILE  a file holding one
  AEGIS_READ_ONLY   true to offer only tools that read

Flags:
`, version)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, version)
		return 0
	}

	cfg, err := config.Load(getenv, *readOnly)
	if err != nil {
		fmt.Fprintln(errw, "aegis-mcp:", err)
		return 1
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(errw, "aegis-mcp: warning:", w)
	}

	client := aegis.New(cfg.Server, cfg.Token, "aegis-mcp/"+version)
	if !cfg.ReadOnly {
		cfg.ReadOnly = !canWrite(ctx, client, errw)
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:       "aegis",
		Title:      "Aegis",
		Version:    version,
		WebsiteURL: "https://aegis.krathos.systems",
		Icons:      icons,
	}, &mcp.ServerOptions{Instructions: tools.Instructions})
	tools.Register(server, client, tools.Options{ReadOnly: cfg.ReadOnly})

	if err := server.Run(ctx, &mcp.IOTransport{Reader: in, Writer: out}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(errw, "aegis-mcp:", err)
		return 1
	}
	return 0
}

// probeTimeout bounds the start-up check of what the token may do.
var probeTimeout = 5 * time.Second

// expiryWarning is how close a token's expiry must be for start-up to warn.
const expiryWarning = 7 * 24 * time.Hour

// canWrite asks Aegis what the token may do, so a token that cannot change
// anything is offered only the tools that read. If Aegis cannot be asked, every
// tool is offered and Aegis refuses what it must.
func canWrite(ctx context.Context, c *aegis.Client, errw io.Writer) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	me, err := c.Me(ctx)
	var se *aegis.StatusError
	if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
		fmt.Fprintf(errw, "aegis-mcp: Aegis refused the token (%s). Make a new API token on your Aegis account page and run aegis-mcp setup.\n", se.Message)
		return true
	}
	if err != nil {
		fmt.Fprintln(errw, "aegis-mcp: warning: could not check the token with Aegis:", err)
		return true
	}
	if expires, ok := tools.TokenExpiry(me); ok && time.Until(expires) < expiryWarning {
		fmt.Fprintf(errw, "aegis-mcp: warning: this API token expires on %s. Make a new one on your Aegis account page and run aegis-mcp setup.\n",
			expires.UTC().Format("2 Jan 2006"))
	}
	if tools.WritesAllowed(me) {
		return true
	}
	fmt.Fprintln(errw, "aegis-mcp: this token cannot change anything, so only the tools that read are offered")
	return false
}

// Replaceable in tests.
var (
	executable = os.Executable
	configPath = config.Path
)

// runSetup is `aegis-mcp setup`. It talks to a person, so it prints to out.
func runSetup(ctx context.Context, args []string, in io.Reader, out io.Writer, errw io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("aegis-mcp setup", flag.ContinueOnError)
	fs.SetOutput(errw)
	server := fs.String("server", "", "your Aegis address (asked for if not given)")
	tokenStdin := fs.Bool("token-stdin", false, "read the token from stdin instead of asking")
	noClient := fs.Bool("no-client", false, "do not register with AI clients")
	fs.Usage = func() {
		fmt.Fprintf(errw, "aegis-mcp setup: connect to Aegis and register with your AI clients.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	path, err := configPath(getenv)
	if err != nil {
		fmt.Fprintln(errw, "aegis-mcp setup: no place to save the settings:", err)
		return 1
	}
	self, err := executable()
	if err != nil {
		fmt.Fprintln(errw, "aegis-mcp setup: cannot tell where aegis-mcp is installed:", err)
		return 1
	}

	err = setup.Run(ctx, setup.Options{
		Server:     *server,
		TokenStdin: *tokenStdin,
		NoClient:   *noClient,
		ConfigPath: path,
		Self:       self,
		Version:    version,
	}, in, out)
	if err != nil {
		fmt.Fprintln(errw, "aegis-mcp setup:", err)
		return 1
	}
	return 0
}
