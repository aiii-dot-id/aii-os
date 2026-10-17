package store

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestCarryAcrossInterruptedAfterTheMirrorRebuildsAtBoot(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	if _, err := ledger.Rewrap(ledgerPath, kp, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	verified := carryHead(t, ledgerPath, kp)
	interrupted := errors.New("synthetic interrupted replay")
	opened, err := OpenAndReplay(t.Context(), dbPath, verified, func(func(*ledger.Event) error) error { return interrupted })
	if opened != nil {
		opened.Close()
		t.Fatal("failed carry published a store")
	}
	if !errors.Is(err, interrupted) {
		t.Fatalf("interruption not reached: %v", err)
	}
	raw := openRawForTest(t, dbPath)
	var original uint64
	err = raw.QueryRow("SELECT MAX(seq) FROM ledger").Scan(&original)
	raw.Close()
	if err != nil || original != 1 {
		t.Fatalf("interruption lost acknowledgment: %d %v", original, err)
	}
	// The next normal open uses the same atomic operation.
	st, err := OpenAndReplay(t.Context(), dbPath, verified, func(yield func(*ledger.Event) error) error { return ledger.Stream(ledgerPath, yield) })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("ephemeral rows after the interrupted carry: conversations=%d %v, want 2", n, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM runtime_meta`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ephemeral rows after the interrupted carry: runtime_meta=%d %v, want 1", n, err)
	}
	var head int64
	if err := st.db.QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&head); err != nil || head != 4 {
		t.Fatalf("mirror head after the boot rebuild %d %v, want 4", head, err)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM experiences`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("derived rows after the boot rebuild: experiences=%d %v, want 3", n, err)
	}
}
