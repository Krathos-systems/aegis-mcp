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
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Krathos-systems/aegis-mcp/internal/aegis"
	"github.com/Krathos-systems/aegis-mcp/internal/config"
	"github.com/Krathos-systems/aegis-mcp/internal/tools"
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
		fmt.Fprint(out, "Aegis API token (input is hidden): ")
		if token, err = readSecret(in, r, out); err != nil {
			return fmt.Errorf("reading the token: %w", err)
		}
	}
	if token == "" {
		return errors.New("no token given")
	}
	if strings.HasPrefix(token, "aeg_") {
		return errors.New("that is a developer API key (it starts with aeg_), which the aegis command uses. " +
			"This needs an API token, which starts with aegt_: make one on your Aegis account page under API tokens")
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
	if _, isToken := me["api_token"].(map[string]any); !isToken {
		fmt.Fprintln(out, "! That is a sign-in session, not an API token, and it stops working within minutes.\n"+
			"  Make an API token on your Aegis account page and run aegis-mcp setup again.")
	} else {
		printExpiry(me, out)
		if !tools.WritesAllowed(me) {
			fmt.Fprintln(out, "! This token can read but not change anything, so only the tools that read will be offered.")
		}
	}

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

// describe names the account, its level and the token, e.g.
// `ada (editor), with the token "laptop" limited to collaborator`.
func describe(me map[string]any) string {
	name, _ := me["username"].(string) // what GET /admin/me calls it
	if name == "" {
		name, _ = me["name"].(string)
	}
	if name == "" {
		name = "an unnamed account"
	}
	s := name
	if level, ok := me["highest_level"].(string); ok && level != "" {
		s = fmt.Sprintf("%s (%s)", name, level)
	}
	if token, ok := me["api_token"].(map[string]any); ok {
		if tokenName, _ := token["name"].(string); tokenName != "" {
			s += fmt.Sprintf(", with the token %q", tokenName)
		}
		if limit, _ := token["level"].(string); limit != "" {
			s += " limited to " + limit
		}
	}
	return s
}

// soonDays is how close an expiry must be for setup to warn about it.
const soonDays = 14

// printExpiry says when the token expires, as a warning when that is soon.
func printExpiry(me map[string]any, out io.Writer) {
	expires, ok := tools.TokenExpiry(me)
	if !ok {
		return
	}
	days := int(math.Round(time.Until(expires).Hours() / 24))
	when := fmt.Sprintf("on %s, in %d days", expires.UTC().Format("2 Jan 2006"), days)
	if days < soonDays {
		fmt.Fprintf(out, "! This token expires %s. Make a new one before then and run aegis-mcp setup again.\n", when)
		return
	}
	fmt.Fprintf(out, "✓ The token expires %s\n", when)
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
