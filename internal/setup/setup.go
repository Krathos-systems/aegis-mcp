// Package setup is `aegis-mcp setup`: ask for the Aegis address and token,
// check them against the server, save them, and register the server with
// the AI clients it can find.
package setup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
	"github.com/Krathos-systems/aegis-mcp/internal/config"
)

// Options are the flags and facts setup needs.
type Options struct {
	// Server is --server; empty means ask.
	Server string
	// TokenStdin reads the token from stdin instead of asking (for scripts).
	TokenStdin bool
	// NoClient skips registering with AI clients.
	NoClient bool
	// ConfigPath is where the config file is written.
	ConfigPath string
	// Self is the absolute path of the aegis-mcp binary clients should start.
	Self    string
	Version string
}

// Replaceable in tests.
var (
	lookPath   = exec.LookPath
	isTerminal = func(in io.Reader) (int, bool) {
		f, ok := in.(*os.File)
		if !ok {
			return 0, false
		}
		fd := int(f.Fd())
		return fd, term.IsTerminal(fd)
	}
)

// Run performs setup, reading answers from in and writing to out.
func Run(ctx context.Context, o Options, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)

	server, err := askServer(o.Server, r, out)
	if err != nil {
		return err
	}

	var token string
	if o.TokenStdin {
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("reading the token from stdin: %w", err)
		}
		token = strings.TrimSpace(string(data))
	} else {
		fmt.Fprint(out, "Aegis admin token (input is hidden): ")
		if token, err = readSecret(in, r, out); err != nil {
			return fmt.Errorf("reading the token: %w", err)
		}
	}
	if token == "" {
		return errors.New("no token given")
	}

	me, err := aegis.New(server, token, "aegis-mcp/"+o.Version).Me(ctx)
	if err != nil {
		var se *aegis.StatusError
		if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
			return fmt.Errorf("%s did not accept that token; nothing was saved", server)
		}
		return fmt.Errorf("could not check the token with %s; nothing was saved: %w", server, err)
	}
	fmt.Fprintf(out, "✓ Signed in to %s as %s\n", server, describe(me))

	if err := config.WriteFile(o.ConfigPath, config.File{Server: server, Token: token}); err != nil {
		return fmt.Errorf("saving %s: %w", o.ConfigPath, err)
	}
	fmt.Fprintf(out, "✓ Saved to %s (only you can read it)\n", o.ConfigPath)

	if !o.NoClient {
		registerClaudeCode(ctx, o.Self, out)
	}
	printSnippet(o.Self, out)
	return nil
}

// askServer returns the --server value, or asks until it gets a valid one.
func askServer(given string, r *bufio.Reader, out io.Writer) (string, error) {
	if given != "" {
		return config.NormalizeServer(given)
	}
	for {
		fmt.Fprint(out, "Aegis address (e.g. https://acme.example.com): ")
		line, err := readLine(r)
		if err != nil {
			return "", fmt.Errorf("reading the address: %w", err)
		}
		server, err := config.NormalizeServer(line)
		if err == nil {
			return server, nil
		}
		fmt.Fprintln(out, "✗", err)
	}
}

// readSecret reads without echo from a terminal, or a plain line otherwise.
func readSecret(in io.Reader, r *bufio.Reader, out io.Writer) (string, error) {
	if fd, ok := isTerminal(in); ok {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(out) // the user's Enter was not echoed
		return strings.TrimSpace(string(b)), err
	}
	return readLine(r)
}

// readLine returns one line without its newline. A last line without a
// newline is accepted; no input at all is io.ErrUnexpectedEOF.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err == io.EOF && line != "" {
		err = nil
	} else if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return strings.TrimSpace(line), err
}

func describe(me map[string]any) string {
	name, _ := me["name"].(string)
	if name == "" {
		name = "an unnamed account"
	}
	if level, ok := me["level"].(string); ok && level != "" {
		return fmt.Sprintf("%s (%s)", name, level)
	}
	return name
}

// registerClaudeCode adds the server to Claude Code's user scope when the
// claude command is installed. A failure is explained, never fatal.
func registerClaudeCode(ctx context.Context, self string, out io.Writer) {
	claude, err := lookPath("claude")
	if err != nil {
		return
	}
	args := []string{"mcp", "add", "--scope", "user", "aegis", "--", self}
	result, err := exec.CommandContext(ctx, claude, args...).CombinedOutput()
	if err != nil {
		fmt.Fprintf(out, "! Could not add it to Claude Code: %s\n  To add it yourself: claude %s\n",
			strings.TrimSpace(string(result)), strings.Join(quoteAll(args), " "))
		return
	}
	fmt.Fprintln(out, "✓ Added to Claude Code. Restart Claude Code to load it.")
}

func printSnippet(self string, out io.Writer) {
	snippet, _ := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"aegis": map[string]any{"command": self}},
	}, "", "  ") // plain maps of strings: cannot fail
	fmt.Fprintf(out, "\nFor other MCP clients (Cursor, VS Code, …), add this server:\n%s\n", snippet)
}

func quoteAll(args []string) []string {
	q := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		q[i] = a
	}
	return q
}
