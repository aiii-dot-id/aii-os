//go:build !windows

package updates

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .

func rollbackFixture(t *testing.T) (dir, prev, pend, marker, exe string) {
	t.Helper()
	dir = t.TempDir()
	prev = filepath.Join(dir, previousFile)
	pend = filepath.Join(dir, pendingFile)
	marker = filepath.Join(dir, markerFile)
	exe = filepath.Join(t.TempDir(), "aii")
	if err := os.WriteFile(prev, []byte("previous binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("current binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	return
}

func TestBootMarkerPresentRetiresStaleUpdateState(t *testing.T) {
	_, prev, pend, marker, exe := rollbackFixture(t)
	if err := os.WriteFile(pend, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := checkRollbackAt(filepath.Dir(marker), exe); got != "" {
		t.Fatalf("a completed previous boot must not roll back, got %q", got)
	}
	if _, err := os.Stat(prev); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale backup must be retired once the marker proves the boot succeeded: %v", err)
	}
	if _, err := os.Stat(pend); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale tombstone must be retired with it: %v", err)
	}
}

func TestBootMarkerAbsentContinuesRecoveryWithoutRollback(t *testing.T) {
	dir, prev, _, _, exe := rollbackFixture(t)
	// .
	// .
	if got := checkRollbackAt(dir, exe); got != "" {
		t.Fatalf("an absent marker with no tombstone adopts state; it must not roll back, got %q", got)
	}
	if _, err := os.Stat(prev); err != nil {
		t.Errorf("the backup must be kept for the next boot's decision: %v", err)
	}
	pend, ok := readPending(dir)
	if !ok || pend.Attempts != 1 {
		t.Errorf("absence must continue the recovery logic: want an adopted tombstone with attempts=1, got ok=%v %+v", ok, pend)
	}
}

func TestBootMarkerCannotSeeLeavesEverythingUntouched(t *testing.T) {
	dir, prev, pend, marker, exe := rollbackFixture(t)
	const tombstone = "not a tombstone the updater wrote"
	if err := os.WriteFile(pend, []byte(tombstone), 0o600); err != nil {
		t.Fatal(err)
	}
	// .
	if err := os.Symlink(markerFile, marker); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, err := os.Stat(marker); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Skipf("this filesystem does not fail the self-symlink stat the way the test needs (err=%v)", err)
	}

	if got := checkRollbackAt(dir, exe); got != "" {
		t.Fatalf("cannot-see must never proceed to a rollback, got %q", got)
	}
	if b, err := os.ReadFile(prev); err != nil || string(b) != "previous binary" {
		t.Errorf("the backup must be untouched when the marker cannot be seen: %v %q", err, b)
	}
	if b, err := os.ReadFile(pend); err != nil || string(b) != tombstone {
		t.Errorf("the tombstone must be untouched — neither retired nor rewritten by the adopt path: %v %q", err, b)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Errorf("the marker path itself must be left alone: %v", err)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "current binary" {
		t.Errorf("the running binary must be untouched: %v %q", err, b)
	}
}
