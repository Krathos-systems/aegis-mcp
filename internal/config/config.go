// Package config reads the server's settings from the environment.
//
// The token is accepted from AEGIS_TOKEN or a file named by AEGIS_TOKEN_FILE,
// never from a command-line flag: argv is visible to every user on the
// machine through ps.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
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

// Load reads the environment through getenv (os.Getenv in production).
// readOnlyFlag is the --read-only flag; either it or AEGIS_READ_ONLY turns
// write tools off.
func Load(getenv func(string) string, readOnlyFlag bool) (*Config, error) {
	c := &Config{ReadOnly: readOnlyFlag}

	server, err := normalizeServer(getenv("AEGIS_SERVER"))
	if err != nil {
		return nil, err
	}
	c.Server = server
	if u, _ := url.Parse(server); u.Scheme == "http" && !isLoopback(u.Hostname()) {
		c.Warnings = append(c.Warnings,
			"AEGIS_SERVER uses http://, so the token travels unencrypted")
	}

	if v := getenv("AEGIS_READ_ONLY"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("AEGIS_READ_ONLY must be true or false, got %q", v)
		}
		c.ReadOnly = c.ReadOnly || on
	}

	token, warn, err := loadToken(getenv)
	if err != nil {
		return nil, err
	}
	c.Token = token
	if warn != "" {
		c.Warnings = append(c.Warnings, warn)
	}
	return c, nil
}

func normalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("AEGIS_SERVER is not set: set it to your Aegis address, e.g. https://acme.example.com")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("AEGIS_SERVER %q is not a valid address", raw)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("AEGIS_SERVER must start with https://, got %q", u.Scheme+"://")
	}
	if u.User != nil {
		return "", errors.New("AEGIS_SERVER must not contain a user name or password")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("AEGIS_SERVER must not contain a query or fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func loadToken(getenv func(string) string) (token, warning string, err error) {
	inline := strings.TrimSpace(getenv("AEGIS_TOKEN"))
	file := getenv("AEGIS_TOKEN_FILE")
	switch {
	case inline != "" && file != "":
		return "", "", errors.New("set AEGIS_TOKEN or AEGIS_TOKEN_FILE, not both")
	case inline != "":
		return inline, "", nil
	case file != "":
		info, err := os.Stat(file)
		if err != nil {
			return "", "", fmt.Errorf("AEGIS_TOKEN_FILE: %w", err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("AEGIS_TOKEN_FILE: %w", err)
		}
		t := strings.TrimSpace(string(data))
		if t == "" {
			return "", "", fmt.Errorf("AEGIS_TOKEN_FILE %s is empty", file)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			warning = fmt.Sprintf("AEGIS_TOKEN_FILE %s can be read by other users (mode %o); chmod 600 it",
				file, info.Mode().Perm())
		}
		return t, warning, nil
	}
	return "", "", errors.New("no token: set AEGIS_TOKEN or AEGIS_TOKEN_FILE")
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
