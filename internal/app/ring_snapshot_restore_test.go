package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A ring snapshot the manager refuses — a Ring 0 row, which the ring
// writer used to keep (plan of record T68) — is skipped at the boot's
// restore, and the rest of the restore goes on: the facilities' sections
// come back, and Ring 0 holds the constitution and nothing else.
func TestARing0SnapshotIsSkippedAtRestoreAndTheRestComeBack(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Snapshots")
	buildPriorProjection(t, ledgerPath, dbPath)
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRingSection(int(ring.Ring0), "amendment", "a section of my own"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRingSection(int(ring.Ring3), "surfacing", "a note kept across the restart"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	app := New(safebootConfig(t, dir, "Snapshots", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if reason, safe := app.SafeMode(); safe {
		t.Fatalf("a refused snapshot row should not stop the boot: SAFE (%s)", reason)
	}
	if got := app.rings.Section(ring.Ring0, "amendment"); got != "" {
		t.Fatalf("A RING 0 SNAPSHOT WAS RESTORED: %q", got)
	}
	if got := app.rings.Section(ring.Ring3, "surfacing"); got != "a note kept across the restart" {
		t.Fatalf("the Ring 3 section should come back, got %q", got)
	}
	if !app.rings.Ring0IsConstitution() {
		t.Fatal("Ring 0 should hold the constitution")
	}
}
