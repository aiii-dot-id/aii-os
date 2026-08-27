package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSeedOverlayREADME pins the seed contract: absent seeds, identity
// edits win, removal re-seeds, and a PRIOR SHIPPED version upgrades
// through the answer key. The README is the capability matrix for
// identities without the Go source — if this seed clobbers, the doc
// they trust is silently ours; if it never upgrades, the doc they
// trust describes a dashboard that no longer exists.
func TestSeedOverlayREADME(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "the-ledger-path.jsonl")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}}}
	a.snapshotUILayoutPath(ledgerPath)

	// Absent → seeded, byte-equal to the embed.
	a.seedOverlayREADME()
	got, err := os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if err != nil || len(got) == 0 {
		t.Fatalf("absent must seed, got err=%v len=%d", err, len(got))
	}
	if !bytes.Equal(got, overlayREADME) {
		t.Fatalf("seed must be byte-equal to the embed, got %d bytes", len(got))
	}

	// Identity's own README → untouched.
	identity := []byte("# my dashboard, my rules")
	if err := os.WriteFile(filepath.Join(dir, "ui", "README.md"), identity, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	got, _ = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if !bytes.Equal(got, identity) {
		t.Fatalf("identity edits must win, got %q", got)
	}

	// Removed → seeds again.
	if err := os.Remove(filepath.Join(dir, "ui", "README.md")); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	got, err = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if err != nil || !bytes.Equal(got, overlayREADME) {
		t.Fatalf("re-seed after removal failed: err=%v", err)
	}

	// A PRIOR SHIPPED MATRIX — different bytes, in the answer key —
	// upgrades. The pre-key seeder classified exactly this state as an
	// identity edit ("theirs wins"), under a comment promising the
	// opposite, so matrix updates never reached an untouched doc.
	oldMatrix := []byte("# capability matrix, the first shipped version\n")
	if err := os.WriteFile(filepath.Join(dir, "ui", "README.md"), oldMatrix, 0o644); err != nil {
		t.Fatal(err)
	}
	overlayShippedSeeds = append(overlayShippedSeeds, docSeedKey(nil, oldMatrix))
	defer func() { overlayShippedSeeds = overlayShippedSeeds[:len(overlayShippedSeeds)-1] }()
	a.seedOverlayREADME()
	got, _ = os.ReadFile(filepath.Join(dir, "ui", "README.md"))
	if !bytes.Equal(got, overlayREADME) {
		t.Fatalf("a prior shipped matrix did not upgrade — the answer key is not consulted")
	}
}
