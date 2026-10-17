package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// damageFinalLine strips the trailing newline and mangles the tail of
// the LAST ledger line — committed-then-damaged, not crash debris.
func damageFinalLine(t *testing.T, ledgerPath string) {
	t.Helper()
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 30 || raw[len(raw)-1] != '\n' {
		t.Fatalf("fixture ledger not in expected shape")
	}
	if err := os.WriteFile(ledgerPath, raw[:len(raw)-12], 0600); err != nil {
		t.Fatal(err)
	}
}

func tornSidecars(t *testing.T, ledgerPath string) []string {
	t.Helper()
	m, err := filepath.Glob(ledgerPath + ".torn-*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAckedEventLossEntersSafe(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "MirrorLoss")
	buildPriorProjection(t, ledgerPath, dbPath) // the runtime acknowledged everything
	damageFinalLine(t, ledgerPath)
	before, _ := fileDigest(t, ledgerPath)

	app := New(safebootConfig(t, dir, "MirrorLoss", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot must come up in SAFE, not die: %v", err)
	}
	defer stopApp(t, app)

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "projection") {
		t.Fatalf("acknowledged-event loss must enter SAFE with the projection reason, got %q %v", reason, ok)
	}
	if after, _ := fileDigest(t, ledgerPath); after != before {
		t.Fatal("acknowledged damaged history was changed before completeness refusal")
	}
	if len(tornSidecars(t, ledgerPath)) != 0 {
		t.Fatal("refused recovery should retain the original file without creating a sidecar")
	}
}

func TestCrashDebrisStillBootsLive(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Debris")
	buildPriorProjection(t, ledgerPath, dbPath)
	// A torn write the runtime never acknowledged: garbage appended
	// with no newline, and the mirror knows nothing of it.
	f, err := os.OpenFile(ledgerPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"seq":999,"type":"experi`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	app := New(safebootConfig(t, dir, "Debris", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("crash debris is survivable, boot died: %v", err)
	}
	defer stopApp(t, app)

	if reason, ok := app.SafeMode(); ok {
		t.Fatalf("crash debris must NOT trip the mirror check (false positive): %q", reason)
	}
	if len(tornSidecars(t, ledgerPath)) == 0 {
		t.Fatal("torn debris must still be quarantined for forensics")
	}
}

func TestUnreadableProjectionMirrorEntersSafe(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "UnreadableMirror")
	buildPriorProjection(t, ledgerPath, dbPath)

	projection, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projection.DB().Exec(`ALTER TABLE ledger RENAME COLUMN seq TO broken`); err != nil {
		projection.Close()
		t.Fatal(err)
	}
	if err := projection.Close(); err != nil {
		t.Fatal(err)
	}

	app := New(safebootConfig(t, dir, "UnreadableMirror", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot must come up in SAFE, not die: %v", err)
	}
	defer stopApp(t, app)

	if reason, safe := app.SafeMode(); !safe || !strings.Contains(reason, "projection mirror could not be read") {
		t.Fatalf("unreadable mirror must enter SAFE, got %q safe=%v", reason, safe)
	}
}
