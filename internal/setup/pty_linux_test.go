package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// openPTY returns a pseudo-terminal pair: what is written to master is read
// from slave as if typed.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminals here: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close(); master.Close() })
	return master, slave
}

// The token is read from a real terminal without echo.
func TestTokenFromTerminalIsHidden(t *testing.T) {
	noClaude(t)
	master, slave := openPTY(t)
	if _, err := master.WriteString("good\n"); err != nil {
		t.Fatal(err)
	}

	o := opts(t)
	o.Server = aegisServer(t, `{"name":"ada"}`)
	o.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	var out strings.Builder
	if err := Run(t.Context(), o, slave, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if f := saved(t, o.ConfigPath); f.Token != "good" {
		t.Errorf("saved %+v", f)
	}
	if !strings.Contains(out.String(), "(input is hidden): \n") {
		t.Errorf("no newline after the hidden input:\n%s", out.String())
	}
}
