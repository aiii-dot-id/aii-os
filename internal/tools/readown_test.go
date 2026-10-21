package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// THE HOST READS THE IDENTITY'S FILE THROUGH THE READ TOOL'S DOOR. The
// playbook shown at a work session's start was read with os.ReadFile: a
// symlink or a second name the identity made at its path put a kept file
// in front of the model, a FIFO held the turn, and a huge file was read
// whole before it was cut. ReadOwn is the
// read tool's door: a kept file under any name is refused as a protected
// path and nothing of it comes back, a FIFO is refused without blocking,
// no more than the bound and one byte is consumed, and a cancelled read
// ends. The control half: a plain file in the home is read.
func TestTheHostReadsAnIdentityFileOnlyThroughTheReadDoor(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	profile := put("KEPT SECRET\n", "conf", "settings")
	config := put("KEPT SECRET\n", "config.json")
	key := put("KEPT SECRET\n", "data", "identity.sec")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, profile))
	r := NewRegistry(home, policy, Timeouts{})
	ctx := context.Background()
	book := func(name string) string {
		p := filepath.Join(home, "playbooks", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rel := func(name string) string { return filepath.Join("playbooks", name, "SKILL.md") }

	refused := func(name string) {
		t.Helper()
		data, _, err := r.ReadOwn(ctx, rel(name), 2000)
		var denied *SubstrateError
		if !errors.As(err, &denied) || strings.Contains(string(data), "KEPT") {
			t.Errorf("%s: a kept file must be refused as a protected path, nothing of it read: %q, %v", name, data, err)
		}
	}
	for name, kept := range map[string]string{"to-key": key, "to-config": config, "to-profile": profile} {
		if err := os.Symlink(kept, book(name)); err != nil {
			t.Fatal(err)
		}
		refused(name)
	}
	if err := os.Symlink(filepath.Join(home, "data"), filepath.Join(home, "playbooks", "dir-link")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadOwn(ctx, filepath.Join("playbooks", "dir-link", "identity.sec"), 2000); err == nil {
		t.Error("a linked directory must not reach a file inside the kept data directory")
	}
	if err := os.Link(key, book("hard")); err == nil {
		refused("hard")
	}

	big := strings.Repeat("x", 10000)
	if err := os.WriteFile(book("big"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, more, err := r.ReadOwn(ctx, rel("big"), 2000); err != nil || len(data) != 2000 || !more {
		t.Errorf("an oversized file comes back cut at the bound, and says it goes on: %d bytes, more=%v, %v", len(data), more, err)
	}
	if err := os.WriteFile(book("plain"), []byte("1. read the log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, more, err := r.ReadOwn(ctx, rel("plain"), 2000); err != nil || string(data) != "1. read the log\n" || more {
		t.Errorf("a plain file in the home is read whole: %q, more=%v, %v", data, more, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := r.ReadOwn(cancelled, rel("big"), 2000); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled read ends: %v", err)
	}
	if _, _, err := r.ReadOwn(ctx, rel("absent"), 2000); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file says so: %v", err)
	}

}
