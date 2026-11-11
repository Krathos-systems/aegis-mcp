package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	c, err := Load(env(map[string]string{"AEGIS_SERVER": "acme.example.com/", "AEGIS_TOKEN": " t0k \n"}), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server != "https://acme.example.com" || c.Token != "t0k" || c.ReadOnly || len(c.Warnings) != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestServerNormalization(t *testing.T) {
	cases := map[string]string{
		"https://acme.example.com":        "https://acme.example.com",
		"https://acme.example.com/aegis/": "https://acme.example.com/aegis",
		"acme.example.com:8443":           "https://acme.example.com:8443",
		"http://localhost:8080":           "http://localhost:8080",
	}
	for in, want := range cases {
		got, err := normalizeServer(in)
		if err != nil || got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"no server":   {"AEGIS_TOKEN": "t"},
		"blank":       {"AEGIS_SERVER": "   ", "AEGIS_TOKEN": "t"},
		"unparseable": {"AEGIS_SERVER": "https://a b", "AEGIS_TOKEN": "t"},
		"no host":     {"AEGIS_SERVER": "https://", "AEGIS_TOKEN": "t"},
		"bad scheme":  {"AEGIS_SERVER": "ftp://a.example", "AEGIS_TOKEN": "t"},
		"userinfo":    {"AEGIS_SERVER": "https://u:p@a.example", "AEGIS_TOKEN": "t"},
		"query":       {"AEGIS_SERVER": "https://a.example/?x=1", "AEGIS_TOKEN": "t"},
		"fragment":    {"AEGIS_SERVER": "https://a.example/#x", "AEGIS_TOKEN": "t"},
		"no token":    {"AEGIS_SERVER": "https://a.example"},
		"both tokens": {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN": "t", "AEGIS_TOKEN_FILE": "/x"},
		"bad bool":    {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN": "t", "AEGIS_READ_ONLY": "maybe"},
		"no file":     {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": "/does/not/exist"},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(e), false); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestTokenFileUnreadable(t *testing.T) {
	dir := t.TempDir() // a directory passes Stat but fails ReadFile
	_, err := Load(env(map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": dir}), false)
	if err == nil {
		t.Fatal("directory accepted as a token file")
	}
}

func TestTokenFileEmpty(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(env(map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": f}), false)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestTokenFilePrivateHasNoWarning(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("filetok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(env(map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": f}), true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "filetok" || !c.ReadOnly || len(c.Warnings) != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestWarnings(t *testing.T) {
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("filetok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f, 0o644); err != nil { // the umask may have narrowed it
		t.Fatal(err)
	}
	c, err := Load(env(map[string]string{
		"AEGIS_SERVER": "http://aegis.internal.example", "AEGIS_TOKEN_FILE": f, "AEGIS_READ_ONLY": "1",
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "filetok" || !c.ReadOnly {
		t.Fatalf("got %+v", c)
	}
	joined := strings.Join(c.Warnings, "\n")
	if !strings.Contains(joined, "http://") {
		t.Errorf("no plain-http warning: %q", joined)
	}
	if runtime.GOOS != "windows" && !strings.Contains(joined, "chmod 600") {
		t.Errorf("no file-mode warning: %q", joined)
	}
}

func TestLoopbackHTTPHasNoWarning(t *testing.T) {
	for _, s := range []string{"http://localhost", "http://127.0.0.1:8080", "http://[::1]:80"} {
		c, err := Load(env(map[string]string{"AEGIS_SERVER": s, "AEGIS_TOKEN": "t"}), false)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if len(c.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", s, c.Warnings)
		}
	}
}
