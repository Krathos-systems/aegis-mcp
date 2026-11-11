// Command aegis-mcp is a Model Context Protocol server that lets an AI client
// read and change an organisation's Aegis context and configuration.
//
// It speaks MCP over stdio: the AI client starts it as a subprocess. stdout
// carries the protocol, so everything meant for a person goes to stderr.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
	"github.com/Krathos-systems/aegis-mcp/internal/config"
	"github.com/Krathos-systems/aegis-mcp/internal/tools"
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
	fs := flag.NewFlagSet("aegis-mcp", flag.ContinueOnError)
	fs.SetOutput(errw)
	showVersion := fs.Bool("version", false, "print the version and exit")
	readOnly := fs.Bool("read-only", false, "offer only tools that read (also AEGIS_READ_ONLY=true)")
	fs.Usage = func() {
		fmt.Fprintf(errw, `aegis-mcp %s: an MCP server for Aegis, over stdio.

Environment:
  AEGIS_SERVER      your Aegis address, e.g. https://acme.example.com (required)
  AEGIS_TOKEN       an Aegis admin token, or
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
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "aegis",
		Title:   "Aegis",
		Version: version,
	}, &mcp.ServerOptions{Instructions: tools.Instructions})
	tools.Register(server, client, tools.Options{ReadOnly: cfg.ReadOnly})

	if err := server.Run(ctx, &mcp.IOTransport{Reader: in, Writer: out}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(errw, "aegis-mcp:", err)
		return 1
	}
	return 0
}
