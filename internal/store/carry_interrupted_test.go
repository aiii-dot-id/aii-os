package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
// .
func TestCarryAcrossInterruptedAfterTheMirrorRebuildsAtBoot(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	if _, err := ledger.Rewrap(ledgerPath, kp, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// .
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	raw := openRawForTest(t, dbPath)
	if err := recreateMirror(raw, string(schemaBytes)); err != nil {
		t.Fatalf("recreate mirror: %v", err)
	}
	raw.Close()

	// .
	st, err := New(dbPath)
	if err != nil {
		t.Fatalf("the interrupted projection does not open at boot: %v", err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		t.Fatalf("boot replay after the interruption: %v", err)
	}
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
