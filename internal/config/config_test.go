package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// env returns a getenv for m. Unless m names one, the config file is a path
// in a fresh temp dir that does not exist, so no test reads the real one.
func env(t *testing.T, m map[string]string) func(string) string {
	t.Helper()
	if _, ok := m["AEGIS_MCP_CONFIG"]; !ok {
		m["AEGIS_MCP_CONFIG"] = filepath.Join(t.TempDir(), "absent.json")
	}
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil { // the umask may have narrowed it
		t.Fatal(err)
	}
	return p
}

func TestLoadFromEnvironment(t *testing.T) {
	c, err := Load(env(t, map[string]string{"AEGIS_SERVER": "acme.example.com/", "AEGIS_TOKEN": " t0k \n"}), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server != "https://acme.example.com" || c.Token != "t0k" || c.ReadOnly || len(c.Warnings) != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestNormalizeServer(t *testing.T) {
	ok := map[string]string{
		"https://acme.example.com":        "https://acme.example.com",
		"https://acme.example.com/aegis/": "https://acme.example.com/aegis",
		" acme.example.com:8443 ":         "https://acme.example.com:8443",
		"http://localhost:8080":           "http://localhost:8080",
	}
	for in, want := range ok {
		got, err := NormalizeServer(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "  ", "https://a b", "https://", "ftp://a.example",
		"https://u:p@a.example", "https://a.example/?x=1", "https://a.example/#x"} {
		if _, err := NormalizeServer(bad); err == nil {
			t.Errorf("NormalizeServer(%q) accepted", bad)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"bad server":  {"AEGIS_SERVER": "ftp://a.example", "AEGIS_TOKEN": "t"},
		"both tokens": {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN": "t", "AEGIS_TOKEN_FILE": "/x"},
		"bad bool":    {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN": "t", "AEGIS_READ_ONLY": "maybe"},
		"no file":     {"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": "/does/not/exist"},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(t, e), false); err == nil || errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestNotConfigured(t *testing.T) {
	cases := map[string]map[string]string{
		"nothing":    {},
		"no token":   {"AEGIS_SERVER": "https://a.example"},
		"no server":  {"AEGIS_TOKEN": "t"},
		"blank both": {"AEGIS_SERVER": "  ", "AEGIS_TOKEN": "  "},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(t, e), false); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestTokenFile(t *testing.T) {
	private := writeFile(t, "filetok\n", 0o600)
	c, err := Load(env(t, map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": private}), true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "filetok" || !c.ReadOnly || len(c.Warnings) != 0 {
		t.Fatalf("got %+v", c)
	}

	empty := writeFile(t, " \n", 0o600)
	if _, err := Load(env(t, map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": empty}), false); err == nil ||
		!strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty: %v", err)
	}

	dir := t.TempDir() // a directory cannot be read as a file
	if _, err := Load(env(t, map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN_FILE": dir}), false); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestWarnings(t *testing.T) {
	open := writeFile(t, "filetok\n", 0o644)
	c, err := Load(env(t, map[string]string{
		"AEGIS_SERVER": "http://aegis.internal.example", "AEGIS_TOKEN_FILE": open, "AEGIS_READ_ONLY": "1",
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ReadOnly {
		t.Fatal("AEGIS_READ_ONLY ignored")
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
		c, err := Load(env(t, map[string]string{"AEGIS_SERVER": s, "AEGIS_TOKEN": "t"}), false)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if len(c.Warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", s, c.Warnings)
		}
	}
}

// ---- config file ----

func TestLoadFromConfigFile(t *testing.T) {
	path := writeFile(t, `{"server":"acme.example.com","token":" filetok "}`, 0o600)
	c, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": path}), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server != "https://acme.example.com" || c.Token != "filetok" || len(c.Warnings) != 0 {
		t.Fatalf("got %+v", c)
	}
}

func TestEnvironmentOverridesConfigFile(t *testing.T) {
	path := writeFile(t, `{"server":"https://file.example","token":"filetok"}`, 0o600)

	c, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": path, "AEGIS_SERVER": "https://env.example"}), false)
	if err != nil || c.Server != "https://env.example" || c.Token != "filetok" {
		t.Fatalf("server from env: %+v %v", c, err)
	}
	c, err = Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": path, "AEGIS_TOKEN": "envtok"}), false)
	if err != nil || c.Server != "https://file.example" || c.Token != "envtok" {
		t.Fatalf("token from env: %+v %v", c, err)
	}
	// Both from the environment: the file is not even read.
	broken := writeFile(t, `not json`, 0o600)
	c, err = Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": broken, "AEGIS_SERVER": "https://e.example", "AEGIS_TOKEN": "t"}), false)
	if err != nil || c.Token != "t" {
		t.Fatalf("env only: %+v %v", c, err)
	}
}

func TestConfigFileProblems(t *testing.T) {
	if _, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": writeFile(t, `not json`, 0o600)}), false); err == nil ||
		!strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("bad JSON: %v", err)
	}
	if _, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": t.TempDir()}), false); err == nil ||
		!strings.Contains(err.Error(), "config file") {
		t.Errorf("unreadable: %v", err)
	}
	blankToken := writeFile(t, `{"server":"https://a.example","token":"  "}`, 0o600)
	if _, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": blankToken}), false); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("blank token: %v", err)
	}
	open := writeFile(t, `{"server":"https://a.example","token":"t"}`, 0o644)
	c, err := Load(env(t, map[string]string{"AEGIS_MCP_CONFIG": open}), false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && (len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "config file")) {
		t.Errorf("warnings = %v", c.Warnings)
	}
}

func TestPath(t *testing.T) {
	if p, err := Path(func(string) string { return "/x/c.json" }); err != nil || p != "/x/c.json" {
		t.Errorf("override: %q %v", p, err)
	}

	old := userConfigDir
	t.Cleanup(func() { userConfigDir = old })
	userConfigDir = func() (string, error) { return "/cfg", nil }
	if p, err := Path(func(string) string { return "" }); err != nil || p != filepath.Join("/cfg", "aegis-mcp", "config.json") {
		t.Errorf("default: %q %v", p, err)
	}

	userConfigDir = func() (string, error) { return "", errors.New("no home") }
	if _, err := Path(func(string) string { return "" }); err == nil {
		t.Error("no config dir accepted")
	}
	// With no config dir, the environment alone still works…
	if _, err := Load(func(k string) string {
		return map[string]string{"AEGIS_SERVER": "https://a.example", "AEGIS_TOKEN": "t"}[k]
	}, false); err != nil {
		t.Errorf("env only: %v", err)
	}
	// …and without it, the answer is "not set up".
	if _, err := Load(func(string) string { return "" }, false); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("nothing: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "dir", "config.json")
	if err := WriteFile(path, File{Server: "https://a.example", Token: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, File{Server: "https://a.example", Token: "two"}); err != nil {
		t.Fatal(err)
	}
	f, warning, err := ReadFile(path)
	if err != nil || f.Token != "two" || warning != "" {
		t.Fatalf("f=%+v warning=%q err=%v", f, warning, err)
	}
	info, _ := os.Stat(path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind")
	}
}

func TestWriteFileFailures(t *testing.T) {
	// The parent "directory" is a file.
	parent := writeFile(t, "x", 0o600)
	if err := WriteFile(filepath.Join(parent, "config.json"), File{}); err == nil {
		t.Error("parent is a file: accepted")
	}

	// The temp file cannot be created.
	locked := t.TempDir()
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if os.Getuid() != 0 { // root ignores directory permissions
		if err := WriteFile(filepath.Join(locked, "config.json"), File{}); err == nil {
			t.Error("unwritable dir: accepted")
		}
	}

	// The target is a non-empty directory, so the rename fails; the temp
	// file must not be left behind.
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(filepath.Join(target, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, File{}); err == nil {
		t.Error("rename over a directory: accepted")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file left behind after a failed rename")
	}
}
