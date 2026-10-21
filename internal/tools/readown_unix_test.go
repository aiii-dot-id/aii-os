//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A FIFO AT THE PLAYBOOK'S PATH DOES NOT HOLD THE HOST. os.ReadFile on a
// FIFO with no writer waited for one for ever, and the card read the
// playbook at every turn. The read door
// refuses it as not a regular file, without blocking.
func TestTheReadDoorRefusesAFIFOWithoutBlocking(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(home, "playbooks", "fifo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(fifo), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	r := NewRegistry(home, firewall.DefaultPolicy(), Timeouts{})
	done := make(chan error, 1)
	go func() {
		_, _, err := r.ReadOwn(context.Background(), filepath.Join("playbooks", "fifo", "SKILL.md"), 2000)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("a FIFO is refused as not a regular file: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a FIFO with no writer held the read")
	}
}

// A FILE REPLACED AFTER IT WAS OPENED IS NOT READ. A link swapped to a kept
// file that is a place of its own (a credential file a profile names), then
// that file replaced by a rename while the handle still holds the old one:
// Linux names the handle "<path> (deleted)", which no place matches, and
// the old file has no name left to be asked about. The place the system names must still hold the very file the
// handle holds, so the read is refused and nothing of the old bytes is read.
func TestAFileReplacedAfterItWasOpenedIsNotRead(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "conf", "settings")
	for _, p := range []string{profile, filepath.Join(home, "playbooks", "x", "SKILL.md")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("KEPT SECRET\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, profile))
	r := NewRegistry(home, policy, Timeouts{})
	leaf := filepath.Join(home, "playbooks", "x", "SKILL.md")
	r.afterAdmit = func() {
		os.Remove(leaf)
		if err := os.Symlink(profile, leaf); err != nil {
			t.Fatal(err)
		}
	}
	r.afterOpen = func() {
		next := profile + ".next"
		if err := os.WriteFile(next, []byte("NEW SECRET\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, profile); err != nil {
			t.Fatal(err)
		}
	}
	data, _, err := r.ReadOwn(context.Background(), filepath.Join("playbooks", "x", "SKILL.md"), 2000)
	if err == nil || strings.Contains(string(data), "SECRET") {
		t.Fatalf("a kept file replaced after it was opened was read: %q, %v", data, err)
	}
}
