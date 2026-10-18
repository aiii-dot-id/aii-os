package filelock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The lock is on the open file: a second handle on the same name is
// refused with the typed contention, and closing the holder releases it.
// The cross-process half — a holder in another process, and its death
// releasing the lock — is the ledger's TestLedgerLockExcludesProcessAndReleasesOnDeath,
// which locks through this primitive.
func TestASecondHandleIsRefusedUntilTheHolderCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	open := func() *os.File {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	holder := open()
	if err := Lock(holder); err != nil {
		t.Fatalf("an unheld lock must be taken: %v", err)
	}
	second := open()
	defer second.Close()
	if err := Lock(second); !errors.Is(err, ErrHeld) {
		t.Fatalf("a held lock must refuse with ErrHeld, got %v", err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Lock(second); err != nil {
		t.Fatalf("closing the holder must release the lock: %v", err)
	}
}
