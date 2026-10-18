//go:build windows

package machine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// storeCapabilities are every capability SID found on any path under dir.
func storeCapabilities(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		caps, err := plugstore.Capabilities(p)
		if err != nil {
			return nil // a path that went while it was walked
		}
		for _, c := range caps {
			out[c.String()] = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRemovalTakesAStoreWhoseBlobsCarryCapabilities(t *testing.T) {
	rig := newRemovalRig(t)
	if caps := storeCapabilities(t, rig.store); len(caps) == 0 {
		t.Fatal("rig: the writer granted no capability in the store")
	}
	blob := filepath.Join(rig.store, blobEntry(rig.works[0].View.Models[0]))
	if fi, err := os.Stat(blob); err != nil || fi.Mode().Perm()&0o200 != 0 {
		t.Fatalf("rig: the blob is not read-only: %v %v", fi, err)
	}
	rem, err := Remove(rig.dir)
	if err != nil || !rem.Done() || !gone(t, rig.store) || !gone(t, rig.drain) || !gone(t, rig.dir) {
		t.Fatalf("a store whose files carry capabilities: %+v %v", rem, err)
	}
}

// NO STORE CAPABILITY REMAINS OUTSIDE THE STORE: every entry's derived SID,
// looked for on every path under the account's directory (the test's), is
// on none once the stores are removed.
func TestNoStoreCapabilityRemainsOutsideTheStore(t *testing.T) {
	rig := newRemovalRig(t)
	var sids []string
	for _, w := range rig.works {
		e, err := plugstore.ParseEntry(w.Entry)
		if err != nil {
			t.Fatal(err)
		}
		sid, err := plugstore.CapabilitySID(e)
		if err != nil {
			t.Fatal(err)
		}
		sids = append(sids, sid.String())
	}
	account := filepath.Dir(filepath.Dir(rig.dir))
	if _, err := Remove(rig.dir); err != nil {
		t.Fatal(err)
	}
	found := storeCapabilities(t, account)
	found2 := storeCapabilities(t, filepath.Dir(rig.drain))
	for _, sid := range sids {
		if p, ok := found[sid]; ok {
			t.Errorf("the capability %s remains on %s", sid, p)
		}
		if p, ok := found2[sid]; ok {
			t.Errorf("the capability %s remains on %s", sid, p)
		}
	}
}

// AN ENTRY HELD OPEN KEEPS ITS STORE, NAMED: a view whose file a process
// has open cannot be moved, so the store stays with its lock and its state,
// the entry named; once let go, a rerun takes it. (Windows may instead let
// the open file's other names go, with POSIX semantics: then the store is
// taken at once, which is as good.)
func TestAnEntryHeldOpenKeepsItsStoreAndIsNamed(t *testing.T) {
	rig := newRemovalRig(t)
	view := rig.works[0]
	f, err := os.Open(filepath.Join(rig.store, view.Entry, view.View.Models[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	rem, err := Remove(rig.dir)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	if s := rem.store(rig.store); s != nil && !s.Gone {
		if !strings.Contains(s.Kept, view.Entry) && !strings.Contains(strings.Join(s.Busy, " "), view.Entry) {
			t.Errorf("the store is kept without naming the entry held open: %+v", s)
		}
		if gone(t, filepath.Join(rig.dir, stateFileName)) {
			t.Error("state.json went while a store it names was kept")
		}
	} else {
		t.Logf("Windows let the open view go (POSIX delete semantics): %+v", rem)
	}
	f.Close()
	if rem, err := Remove(rig.dir); err != nil || !rem.Done() || !gone(t, rig.store) {
		t.Fatalf("once let go: %+v %v", rem, err)
	}
}
