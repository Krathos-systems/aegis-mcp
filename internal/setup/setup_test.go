package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Krathos-systems/aegis-mcp/internal/config"
)

// aegisServer answers /api/admin/me: 200 with body for the token "good",
// 401 for anything else.
func aegisServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"token rejected"}`)
			return
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// noClaude makes the claude command look absent for the test.
func noClaude(t *testing.T) {
	t.Helper()
	old := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookPath = old })
}

// fakeClaude installs a claude script that writes its arguments to a file
// and exits with code; it returns the file.
func fakeClaude(t *testing.T, code int, output string) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\necho '" + output + "'\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	old := lookPath
	lookPath = func(string) (string, error) { return script, nil }
	t.Cleanup(func() { lookPath = old })
	return argsFile
}

func opts(t *testing.T) Options {
	return Options{
		ConfigPath: filepath.Join(t.TempDir(), "aegis-mcp", "config.json"),
		Self:       "/opt/aegis mcp/aegis-mcp",
		Version:    "test",
	}
}

func run(t *testing.T, o Options, input string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := Run(context.Background(), o, strings.NewReader(input), &out)
	return out.String(), err
}

func saved(t *testing.T, path string) config.File {
	t.Helper()
	f, _, err := config.ReadFile(path)
	if err != nil || f == nil {
		t.Fatalf("config file: %v", err)
	}
	return *f
}

func TestInteractive(t *testing.T) {
	noClaude(t)
	url := aegisServer(t, `{"name":"ada","level":"editor"}`)
	o := opts(t)

	// A bad address is explained and asked again; the token's last line has
	// no newline, as when a paste is followed by Ctrl-D.
	out, err := run(t, o, "ftp://nope\n"+url+"\ngood")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"✗", "Signed in to " + url + " as ada (editor)", "Saved to " + o.ConfigPath, `"command": "/opt/aegis mcp/aegis-mcp"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Claude Code") {
		t.Errorf("mentions Claude Code without it installed:\n%s", out)
	}
	if f := saved(t, o.ConfigPath); f.Server != url || f.Token != "good" {
		t.Errorf("saved %+v", f)
	}
}

func TestScripted(t *testing.T) {
	noClaude(t)
	url := aegisServer(t, `{"name":"ada"}`)
	o := opts(t)
	o.Server, o.TokenStdin = url+"/", true
	out, err := run(t, o, "  good\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "address") || strings.Contains(out, "token (") || !strings.Contains(out, "as ada\n") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if f := saved(t, o.ConfigPath); f.Server != url {
		t.Errorf("saved %+v", f)
	}
}

func TestFailuresSaveNothing(t *testing.T) {
	noClaude(t)
	good := aegisServer(t, `{}`)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()

	cases := []struct {
		name   string
		mod    func(*Options)
		input  string
		reader io.Reader
		want   string
	}{
		{"bad --server", func(o *Options) { o.Server = "ftp://x" }, "", nil, "https://"},
		{"no address", nil, "", nil, "reading the address"},
		{"no token", nil, good + "\n", nil, "reading the token"},
		{"blank token", func(o *Options) { o.Server, o.TokenStdin = good, true }, "   ", nil, "no token given"},
		{"stdin fails", func(o *Options) { o.Server, o.TokenStdin = good, true }, "", errReader{}, "reading the token from stdin"},
		{"rejected", func(o *Options) { o.Server = good }, "bad\n", nil, "did not accept that token"},
		{"server down", func(o *Options) { o.Server = broken.URL }, "good\n", nil, "could not check the token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := opts(t)
			if c.mod != nil {
				c.mod(&o)
			}
			in := c.reader
			if in == nil {
				in = strings.NewReader(c.input)
			}
			err := Run(context.Background(), o, in, io.Discard)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want %q", err, c.want)
			}
			if _, statErr := os.Stat(o.ConfigPath); !os.IsNotExist(statErr) {
				t.Error("config file written despite the failure")
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestSaveFailure(t *testing.T) {
	noClaude(t)
	o := opts(t)
	o.Server = aegisServer(t, `{"name":"ada"}`)
	parent := filepath.Join(t.TempDir(), "file")
	os.WriteFile(parent, nil, 0o600)
	o.ConfigPath = filepath.Join(parent, "config.json")
	if _, err := run(t, o, "good\n"); err == nil || !strings.Contains(err.Error(), "saving") {
		t.Fatalf("err = %v", err)
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]map[string]any{
		"ada (editor)":       {"name": "ada", "level": "editor"},
		"ada":                {"name": "ada", "level": ""},
		"an unnamed account": {},
	}
	for want, me := range cases {
		if got := describe(me); got != want {
			t.Errorf("describe(%v) = %q", me, got)
		}
	}
}

func TestRegistersWithClaudeCode(t *testing.T) {
	argsFile := fakeClaude(t, 0, "added")
	o := opts(t)
	o.Server = aegisServer(t, `{"name":"ada"}`)
	out, err := run(t, o, "good\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Added to Claude Code") {
		t.Errorf("output:\n%s", out)
	}
	args, _ := os.ReadFile(argsFile)
	want := "mcp\nadd\n--scope\nuser\naegis\n--\n/opt/aegis mcp/aegis-mcp\n"
	if string(args) != want {
		t.Errorf("claude called with %q", args)
	}
}

func TestClaudeCodeFailureIsExplained(t *testing.T) {
	fakeClaude(t, 1, "MCP server aegis already exists in user config")
	o := opts(t)
	o.Server = aegisServer(t, `{"name":"ada"}`)
	out, err := run(t, o, "good\n")
	if err != nil {
		t.Fatalf("a client failure must not fail setup: %v", err)
	}
	for _, want := range []string{"already exists", "claude mcp add --scope user aegis -- '/opt/aegis mcp/aegis-mcp'"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "mcpServers") {
		t.Error("snippet missing")
	}
}

func TestNoClientSkipsClaudeCode(t *testing.T) {
	called := false
	old := lookPath
	lookPath = func(string) (string, error) { called = true; return "", exec.ErrNotFound }
	t.Cleanup(func() { lookPath = old })
	o := opts(t)
	o.Server, o.NoClient = aegisServer(t, `{"name":"ada"}`), true
	if _, err := run(t, o, "good\n"); err != nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestQuoteAll(t *testing.T) {
	got := strings.Join(quoteAll([]string{"plain", "with space", "it's"}), " ")
	if got != `plain 'with space' 'it'\''s'` {
		t.Errorf("quoteAll = %s", got)
	}
}

func TestSnippetIsValidJSON(t *testing.T) {
	var out strings.Builder
	printSnippet(`C:\Program Files\aegis-mcp.exe`, &out)
	text := out.String()
	var v map[string]map[string]map[string]string
	if err := json.Unmarshal([]byte(text[strings.Index(text, "{"):]), &v); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if v["mcpServers"]["aegis"]["command"] != `C:\Program Files\aegis-mcp.exe` {
		t.Errorf("got %v", v)
	}
}

func TestIsTerminal(t *testing.T) {
	if _, ok := isTerminal(strings.NewReader("")); ok {
		t.Error("a string reader is a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, ok := isTerminal(r); ok {
		t.Error("a pipe is a terminal")
	}
}
