package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// External C2 (2026-08-20): torn-tail recovery can prove a trailing
// line was never fsync-ACKNOWLEDGED only when nothing else remembers
// it. A committed event damaged in place (newline lost, bytes mangled)
// is indistinguishable from crash debris by the file alone — and a
// chain is a valid prefix of itself, so VerifyChain blesses the
// shortened file. The projection mirror is the runtime's own memory of
// what it acknowledged: mirror ahead of ledger = acknowledged history
// is gone = SAFE, with the torn bytes quarantined for forensics.

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

	app := New(safebootConfig(t, dir, "MirrorLoss", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot must come up in SAFE, not die: %v", err)
	}
	defer app.Stop()

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "projection") {
		t.Fatalf("acknowledged-event loss must enter SAFE with the projection reason, got %q %v", reason, ok)
	}
	if len(tornSidecars(t, ledgerPath)) == 0 {
		t.Fatal("the damaged trailing bytes were destroyed without quarantine")
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
	defer app.Stop()

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

	projection, err := store.New(dbPath)
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
	defer app.Stop()

	if reason, safe := app.SafeMode(); !safe || !strings.Contains(reason, "projection mirror could not be read") {
		t.Fatalf("unreadable mirror must enter SAFE, got %q safe=%v", reason, safe)
	}
}
