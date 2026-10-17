package install

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("AII_INSTALL_STAND_IN") == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "aii-install-guard")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the refusing managers' directory:", err)
		os.Exit(1)
	}
	reached := filepath.Join(dir, "reached.log")
	for _, tool := range []string{"launchctl", "systemctl", "loginctl", "schtasks"} {
		if err := writeRefusing(dir, tool, reached); err != nil {
			fmt.Fprintln(os.Stderr, "a refusing manager:", err)
			os.Exit(1)
		}
	}
	realPATH = os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+realPATH)
	restore, err := guardRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "the test's own registry keys:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := restore(); err != nil {
		fmt.Fprintln(os.Stderr, "the test's own registry keys were not removed:", err)
		code = 1
	}
	if b, _ := os.ReadFile(reached); len(b) > 0 {
		fmt.Fprintf(os.Stderr, "a test reached a real service manager — it would have acted on this account's own registrations:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// realPATH is PATH as the test binary found it, for the one opt-in test
// that asks the real task scheduler (AII_WINDOWS_TASK_LIVE).
var realPATH string
