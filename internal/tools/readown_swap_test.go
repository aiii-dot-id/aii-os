package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A LINK SWAPPED AFTER THE GATE IS JUDGED AS WHAT IT REACHED. The gate
// judged a path, and the door opened it a moment later; a link put in that
// moment at the file, or at a folder above it, reached a kept file the
// gate never judged, and a file with one name was asked nothing more.
// Each swap here is made between the
// admission and the open (afterAdmit): of the leaf, to a link to the key;
// of a folder above it, to a link to the kept data directory. The host's
// read of the identity's file (ReadOwn), the read tool and the write tool
// refuse each one, and nothing of the kept file is read or written.
func TestALinkSwappedAfterTheGateIsJudgedAsWhatItReached(t *testing.T) {
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
	key := put("KEPT SECRET\n", "data", "identity.sec")
	put("KEPT SECRET\n", "data", "SKILL.md")
	profile := put("KEPT SECRET\n", "conf", "settings")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, profile))
	r := NewRegistry(home, policy, Timeouts{})

	reset := func() {
		t.Helper()
		os.RemoveAll(filepath.Join(home, "playbooks"))
		put("1. read the log\n", "playbooks", "x", "SKILL.md")
	}
	swaps := map[string]func(){
		"the leaf": func() {
			leaf := filepath.Join(home, "playbooks", "x", "SKILL.md")
			os.Remove(leaf)
			if err := os.Symlink(key, leaf); err != nil {
				t.Fatal(err)
			}
		},
		"a folder above it": func() {
			dir := filepath.Join(home, "playbooks", "x")
			os.RemoveAll(dir)
			if err := os.Symlink(filepath.Join(home, "data"), dir); err != nil {
				t.Fatal(err)
			}
		},
	}
	for what, swap := range swaps {
		reset()
		r.afterAdmit = swap
		data, _, err := r.ReadOwn(context.Background(), filepath.Join("playbooks", "x", "SKILL.md"), 2000)
		if err == nil || strings.Contains(string(data), "KEPT") {
			t.Errorf("the host's read after a swap of %s: %q, %v; want a refusal and nothing read", what, data, err)
		}

		reset()
		r.afterAdmit = swap
		if res := modelCall(t, r, "read", map[string]any{"file_path": filepath.Join("playbooks", "x", "SKILL.md")}); !res.Refused || strings.Contains(res.Output, "KEPT") {
			t.Errorf("the read tool after a swap of %s must be refused, nothing read: %+v", what, res)
		}

		reset()
		r.afterAdmit = swap
		if res := modelCall(t, r, "write", map[string]any{"file_path": filepath.Join("playbooks", "x", "SKILL.md"), "content": "SPRAY"}); !res.Refused && res.Error == "" {
			t.Errorf("the write tool after a swap of %s must be refused: %+v", what, res)
		}
		for _, kept := range []string{key, filepath.Join(home, "data", "SKILL.md")} {
			if b, _ := os.ReadFile(kept); string(b) != "KEPT SECRET\n" {
				t.Errorf("after a swap of %s, the kept %s was written: %q", what, filepath.Base(kept), b)
			}
		}
	}

	// The control half: with no swap, the file is read and written.
	reset()
	r.afterAdmit = nil
	if data, _, err := r.ReadOwn(context.Background(), filepath.Join("playbooks", "x", "SKILL.md"), 2000); err != nil || string(data) != "1. read the log\n" {
		t.Fatalf("an unswapped read: %q, %v", data, err)
	}
	if res := modelCall(t, r, "write", map[string]any{"file_path": filepath.Join("playbooks", "x", "SKILL.md"), "content": "2. open it\n"}); res.Error != "" || res.Refused {
		t.Fatalf("an unswapped write: %+v", res)
	}
}
