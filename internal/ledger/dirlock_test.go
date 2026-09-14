package ledger

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
// .
func TestASecondOpenerIsRefusedThroughoutASeal(t *testing.T) {
	path, _ := sealedFixture(t, 30, 10, 20, 30)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// .
	// .
	// .
	var tried []string
	prev := sealStep
	sealStep = func(step string) {
		second, err := New(path)
		if err == nil {
			second.Close()
			t.Errorf("a second opener got in at %q — this is the corruption", step)
			return
		}
		if !errors.Is(err, ErrLedgerInUse) {
			t.Errorf("at %q the refusal must be ErrLedgerInUse, got %v", step, err)
		}
		tried = append(tried, step)
	}
	t.Cleanup(func() { sealStep = prev })

	if err := l.Seal(10); err != nil {
		t.Fatalf("the seal itself must still succeed: %v", err)
	}
	if len(tried) == 0 {
		t.Fatal("the seal protocol reported no steps; the interleaving was never tested")
	}

	// .
	// .
	// .
	if got := entryHashes(t, path); len(got) == 0 {
		t.Fatal("the ledger reads back after the seal")
	}
}

// .
// .
func TestTheDirectoryLockIsReleasedOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LedgerDirLockName)); err != nil {
		t.Fatalf("the lock file is created beside the ledger: %v", err)
	}
	if second, err := New(path); err == nil {
		second.Close()
		t.Fatal("two ledgers on one directory is exactly what this prevents")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := New(path)
	if err != nil {
		t.Fatalf("after a clean close the next opener gets in: %v", err)
	}
	again.Close()
}
