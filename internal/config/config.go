// Package config decides which Aegis server and token to use.
//
// Two sources, environment first:
//   - AEGIS_SERVER, and AEGIS_TOKEN or AEGIS_TOKEN_FILE (what Docker and
//     hand-written client configs use);
//   - the config file `aegis-mcp setup` writes (what the installer sets up).
//
// The token is never accepted from a command-line flag: argv is visible to
// every user on the machine through ps.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Config is everything the server needs to start.
type Config struct {
	Server   string
	Token    string
	ReadOnly bool
	// Warnings are printed on stderr at start; they never stop the server.
	Warnings []string
}

// File is the config file's contents.
type File struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// ErrNotConfigured means neither the environment nor the config file says
// which server to use or with which token.
var ErrNotConfigured = errors.New("not set up yet: run `aegis-mcp setup`, or set AEGIS_SERVER and AEGIS_TOKEN")

// userConfigDir is replaceable in tests.
var userConfigDir = os.UserConfigDir

// Path is where the config file lives: AEGIS_MCP_CONFIG if set, otherwise
// aegis-mcp/config.json in the user's config directory (~/.config on Linux,
// ~/Library/Application Support on macOS, %AppData% on Windows).
func Path(getenv func(string) string) (string, error) {
	if p := getenv("AEGIS_MCP_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aegis-mcp", "config.json"), nil
}

// ReadFile reads the config file. A missing file is (nil, "", nil).
// warning is set when other users can read the file.
func ReadFile(path string) (f *File, warning string, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("config file: %w", err)
	}
	f = &File{}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, "", fmt.Errorf("config file %s is not valid JSON: %w", path, err)
	}
	return f, modeWarning(path, "config file"), nil
}

// WriteFile writes the config file readable only by its owner, replacing any
// previous one in a single rename so a crash never leaves half a file.
func WriteFile(path string, f File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ") // two strings: cannot fail
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Load decides the configuration. getenv is os.Getenv in production.
// readOnlyFlag is the --read-only flag; either it or AEGIS_READ_ONLY turns
// write tools off.
func Load(getenv func(string) string, readOnlyFlag bool) (*Config, error) {
	c := &Config{ReadOnly: readOnlyFlag}

	if v := getenv("AEGIS_READ_ONLY"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("AEGIS_READ_ONLY must be true or false, got %q", v)
		}
		c.ReadOnly = c.ReadOnly || on
	}

	token, tokenWarning, err := envToken(getenv)
	if err != nil {
		return nil, err
	}
	server := getenv("AEGIS_SERVER")

	// The file only fills in what the environment left out.
	var file *File
	var fileWarning string
	if strings.TrimSpace(server) == "" || token == "" {
		if path, err := Path(getenv); err == nil {
			if file, fileWarning, err = ReadFile(path); err != nil {
				return nil, err
			}
		}
	}
	if strings.TrimSpace(server) == "" && file != nil {
		server = file.Server
	}
	if token == "" && file != nil && strings.TrimSpace(file.Token) != "" {
		token, tokenWarning = strings.TrimSpace(file.Token), fileWarning
	}
	if strings.TrimSpace(server) == "" || token == "" {
		return nil, ErrNotConfigured
	}

	if c.Server, err = NormalizeServer(server); err != nil {
		return nil, err
	}
	if u, _ := url.Parse(c.Server); u.Scheme == "http" && !isLoopback(u.Hostname()) {
		c.Warnings = append(c.Warnings, c.Server+" uses http://, so the token travels unencrypted")
	}
	c.Token = token
	if tokenWarning != "" {
		c.Warnings = append(c.Warnings, tokenWarning)
	}
	return c, nil
}

// NormalizeServer turns what a person types into an origin: https:// is
// assumed when no scheme is given, and a trailing slash is dropped.
func NormalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("the Aegis address is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a valid address", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("the Aegis address must start with https://, not %q", u.Scheme+"://")
	}
	if u.User != nil {
		return "", errors.New("the Aegis address must not contain a user name or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the Aegis address must not contain a query or fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// envToken reads AEGIS_TOKEN or AEGIS_TOKEN_FILE. Neither set is ("", "", nil).
func envToken(getenv func(string) string) (token, warning string, err error) {
	inline := strings.TrimSpace(getenv("AEGIS_TOKEN"))
	file := getenv("AEGIS_TOKEN_FILE")
	switch {
	case inline != "" && file != "":
		return "", "", errors.New("set AEGIS_TOKEN or AEGIS_TOKEN_FILE, not both")
	case inline != "":
		return inline, "", nil
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("AEGIS_TOKEN_FILE: %w", err)
		}
		t := strings.TrimSpace(string(data))
		if t == "" {
			return "", "", fmt.Errorf("AEGIS_TOKEN_FILE %s is empty", file)
		}
		return t, modeWarning(file, "AEGIS_TOKEN_FILE"), nil
	}
	return "", "", nil
}

// modeWarning says so when a file holding a token is readable by others.
func modeWarning(path, what string) string {
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS == "windows" || info.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("%s %s can be read by other users (mode %o); run chmod 600 on it",
		what, path, info.Mode().Perm())
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
