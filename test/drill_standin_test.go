package test

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const drillStandInEnv = "AII_TEST_DRILL_STAND_IN"

// TestMain lets the test binary stand in for a running identity
// (liveSlotProcess). Every other run goes straight to the tests.
func TestMain(m *testing.M) {
	if os.Getenv(drillStandInEnv) == "1" {
		// Parked until its input closes or it is killed. Its command line
		// carries the slot's -dir marker; that is all slot_pid reads.
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// liveSlotProcess starts a process slot_pid (packaging/drills/lib.sh) must
// find for this slot, held open on its input, and stops it when the test
// ends. Elsewhere slot_pid is pgrep over every command line, so a shell
// carrying the -dir marker is found — and a final external `sleep` can be
// exec'd by sh (macOS does this), erasing the marker, so the shell parks
// on a read. On Windows slot_pid asks WMI for aii.exe processes only; a
// shell there is never looked at, so the stand-in is this test binary
// copied under that name and run with the marker.
func liveSlotProcess(t *testing.T, slot string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	live := exec.Command("sh", "-c", "read -r pending; : # -dir "+slot)
	if runtime.GOOS == "windows" {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(t.TempDir(), "aii.exe")
		if err := os.WriteFile(exe, raw, 0o755); err != nil {
			t.Fatal(err)
		}
		live = exec.Command(exe, "-dir", slot)
		live.Env = append(os.Environ(), drillStandInEnv+"=1")
	}
	input, err := live.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	// Killed and reaped before the directory holding the stand-in is
	// removed: Windows cannot delete a running image.
	t.Cleanup(func() {
		input.Close()
		_ = live.Process.Kill()
		_ = live.Wait()
	})
	return live, input
}

// uniqueSlotName names a fixture slot nothing else on the host carries.
// slot_pid on Windows matches the slot's NAME on an aii.exe command line
// ("*-dir*<slot>*"); a fixture slot called "s" matched the host's own
// running identity ("-dir C:\Users\...") and the stop drill waited on it.
func uniqueSlotName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "drill-slot-" + hex.EncodeToString(b)
}
