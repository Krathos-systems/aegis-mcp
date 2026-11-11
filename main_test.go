package main

import (
	"bufio"
	"bytes"
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
	"time"
)

// env returns a getenv for m. The config file always points at a path that
// does not exist, so no test reads the real one.
func env(m map[string]string) func(string) string {
	return func(k string) string {
		if k == "AEGIS_MCP_CONFIG" && m[k] == "" {
			return "/nonexistent/aegis-mcp/config.json"
		}
		return m[k]
	}
}

// nopWriteCloser adapts a buffer to the io.WriteCloser run wants.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func runWith(t *testing.T, args []string, e map[string]string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := run(t.Context(), args, io.NopCloser(strings.NewReader("")), nopWriteCloser{&out}, &errw, env(e))
	return code, out.String(), errw.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := runWith(t, []string{"--version"}, nil)
	if code != 0 || strings.TrimSpace(out) != version {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelpAndBadFlag(t *testing.T) {
	code, out, errw := runWith(t, []string{"-h"}, nil)
	if code != 0 || out != "" || !strings.Contains(errw, "AEGIS_SERVER") {
		t.Fatalf("help: code=%d out=%q err=%q", code, out, errw)
	}
	if code, _, _ := runWith(t, []string{"--nope"}, nil); code != 2 {
		t.Fatalf("bad flag: code=%d", code)
	}
}

func TestMissingConfig(t *testing.T) {
	code, out, errw := runWith(t, nil, map[string]string{"AEGIS_TOKEN": "x"})
	if code != 1 || out != "" || !strings.Contains(errw, "aegis-mcp setup") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errw)
	}
}

// TestSession drives the real program over pipes: initialize, list tools,
// call one, then close stdin and expect a clean exit.
func TestSession(t *testing.T) {
	aegis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"name":"ada","level":"editor"}`)
	}))
	defer aegis.Close()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var errw bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run(t.Context(), []string{"--read-only"}, inR, outW,
			&errw, env(map[string]string{"AEGIS_SERVER": aegis.URL, "AEGIS_TOKEN": "tok"}))
		outW.Close()
	}()

	lines := bufio.NewScanner(outR)
	send := func(msg string) {
		if _, err := io.WriteString(inW, msg+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	recv := func() map[string]any {
		if !lines.Scan() {
			t.Fatalf("no reply; stderr: %s", errw.String())
		}
		var m map[string]any
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatalf("bad reply %q", lines.Text())
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	init := recv()["result"].(map[string]any)
	if init["serverInfo"].(map[string]any)["name"] != "aegis" || !strings.Contains(init["instructions"].(string), "Layers:") {
		t.Fatalf("initialize: %v", init)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var names []string
	for _, tl := range recv()["result"].(map[string]any)["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Contains(strings.Join(names, ","), "update") {
		t.Fatalf("--read-only still offers writes: %v", names)
	}

	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"aegis_whoami","arguments":{}}}`)
	res := recv()["result"].(map[string]any)
	if res["isError"] == true || !strings.Contains(lines.Text(), "ada") {
		t.Fatalf("whoami: %v", res)
	}

	inW.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d; stderr: %s", code, errw.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop when stdin closed")
	}
}

func TestWarningsGoToStderr(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // stop at once: this test is only about start-up output
	var out, errw bytes.Buffer
	code := run(ctx, nil, io.NopCloser(strings.NewReader("")), nopWriteCloser{&out}, &errw,
		env(map[string]string{"AEGIS_SERVER": "http://aegis.example.com", "AEGIS_TOKEN": "tok"}))
	if code != 0 || out.Len() != 0 || !strings.Contains(errw.String(), "uses http://, so the token travels unencrypted") {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errw.String())
	}
}

// errReader fails the first read, as a broken stdin would.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin broke") }
func (errReader) Close() error             { return nil }

func TestTransportFailureExitsNonZero(t *testing.T) {
	var out, errw bytes.Buffer
	code := run(t.Context(), nil, errReader{}, nopWriteCloser{&out}, &errw,
		env(map[string]string{"AEGIS_SERVER": "https://aegis.example.com", "AEGIS_TOKEN": "tok"}))
	if code != 1 || !strings.Contains(errw.String(), "stdin broke") {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
}

// TestMainProcess runs main() in a child copy of this test binary, so the
// wiring of os.Args, stdout and the exit code is tested for real. Go merges
// the child's coverage into the profile, so main() counts toward the gate.
func TestMainProcess(t *testing.T) {
	if os.Getenv("AEGIS_MCP_RUN_MAIN") == "1" {
		os.Args = append([]string{"aegis-mcp"}, strings.Fields(os.Getenv("AEGIS_MCP_ARGS"))...)
		main()
		return
	}
	cases := []struct {
		args string
		code int
		out  string
	}{
		{"--version", 0, version},
		{"--nope", 2, ""},
		{"", 1, ""}, // no AEGIS_SERVER
	}
	for _, c := range cases {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
		cmd.Env = append(os.Environ(), "AEGIS_MCP_RUN_MAIN=1", "AEGIS_MCP_ARGS="+c.args,
			"AEGIS_SERVER=", "AEGIS_TOKEN=", "AEGIS_TOKEN_FILE=",
			"AEGIS_MCP_CONFIG=/nonexistent/aegis-mcp/config.json")
		out, _ := cmd.Output()
		code := cmd.ProcessState.ExitCode()
		if code != c.code || (c.out != "" && strings.TrimSpace(string(out)) != c.out) {
			t.Errorf("args %q: exit %d out %q; want exit %d", c.args, code, out, c.code)
		}
	}
}

// ---- aegis-mcp setup ----

func setupAegis(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"name":"ada","level":"editor"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSetupThenServe(t *testing.T) {
	url := setupAegis(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	e := map[string]string{"AEGIS_MCP_CONFIG": cfg}

	var out, errw bytes.Buffer
	code := run(t.Context(), []string{"setup", "--server", url, "--token-stdin", "--no-client"},
		io.NopCloser(strings.NewReader("good\n")), nopWriteCloser{&out}, &errw, env(e))
	if code != 0 || !strings.Contains(out.String(), "as ada (editor)") {
		t.Fatalf("setup: code=%d out=%q err=%q", code, out.String(), errw.String())
	}

	// The server now starts from the saved file with no environment at all.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out.Reset()
	errw.Reset()
	if code := run(ctx, nil, io.NopCloser(strings.NewReader("")), nopWriteCloser{&out}, &errw, env(e)); code != 0 {
		t.Fatalf("serve from saved config: code=%d err=%q", code, errw.String())
	}
}

func TestSetupFailures(t *testing.T) {
	url := setupAegis(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	cases := []struct {
		name  string
		args  []string
		input string
		code  int
		want  string
	}{
		{"help", []string{"setup", "-h"}, "", 0, "-token-stdin"},
		{"bad flag", []string{"setup", "--nope"}, "", 2, ""},
		{"rejected token", []string{"setup", "--server", url, "--token-stdin", "--no-client"}, "bad", 1, "did not accept"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errw bytes.Buffer
			code := run(t.Context(), c.args, io.NopCloser(strings.NewReader(c.input)), nopWriteCloser{&out}, &errw,
				env(map[string]string{"AEGIS_MCP_CONFIG": cfg}))
			if code != c.code || !strings.Contains(errw.String(), c.want) {
				t.Fatalf("code=%d err=%q", code, errw.String())
			}
		})
	}
}

func TestSetupWithoutConfigDirOrExecutable(t *testing.T) {
	oldPath, oldExe := configPath, executable
	t.Cleanup(func() { configPath, executable = oldPath, oldExe })

	configPath = func(func(string) string) (string, error) { return "", errors.New("no home directory") }
	var errw bytes.Buffer
	if code := run(t.Context(), []string{"setup"}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}, &errw, env(nil)); code != 1 ||
		!strings.Contains(errw.String(), "no place to save") {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}

	configPath = oldPath
	executable = func() (string, error) { return "", errors.New("no /proc") }
	errw.Reset()
	if code := run(t.Context(), []string{"setup"}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}, &errw, env(nil)); code != 1 ||
		!strings.Contains(errw.String(), "cannot tell where") {
		t.Fatalf("code=%d err=%q", code, errw.String())
	}
}
