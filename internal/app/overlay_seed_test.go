package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

const overlayPolicySentence = "It cannot make a background request to any other origin: `uiCSP` sets `default-src 'none'` and allows only `'self'` on every fetch-carrying directive (script, style, img, connect, frame) and on `form-action`. The browser enforces that per response. That is a limit on requests, not a promise that nothing a page shows can leave it: what you write here runs with everything your operator's page can see, and is trusted with it."

func TestTheOverlayREADMESaysWhatThePolicyStopsAndAnUntouchedCopyUpgrades(t *testing.T) {
	doc := strings.Join(strings.Fields(string(overlayREADME)), " ")
	if !strings.Contains(doc, overlayPolicySentence) {
		t.Fatalf("the overlay README does not carry the approved sentence:\n%s", overlayPolicySentence)
	}
	for _, untrue := range []string{"never beacon", "form-action 'none'", "kernel-enforced per response"} {
		if strings.Contains(doc, untrue) {
			t.Errorf("the overlay README still says %q", untrue)
		}
	}

	// The edition before this one is this one with the old sentence: its
	// key must be one shipped before the current, or this test is not
	// holding the edition an identity really has.
	newText := "re-form the view and speak to this server. It cannot make a background\n" +
		"   request to any other origin: `uiCSP` sets `default-src 'none'` and\n" +
		"   allows only `'self'` on every fetch-carrying directive (script, style,\n" +
		"   img, connect, frame) and on `form-action`. The browser enforces that\n" +
		"   per response. That is a limit on requests, not a promise that nothing\n" +
		"   a page shows can leave it: what you write here runs with everything\n" +
		"   your operator's page can see, and is trusted with it.\n"
	oldText := "re-form the view and speak to this server — but it can never beacon to\n" +
		"   an external origin: `uiCSP` sets `default-src 'none'` and allows only\n" +
		"   `'self'` on every fetch-carrying directive (script, style, img,\n" +
		"   connect, frame), with `form-action 'none'`. The network boundary is\n" +
		"   the browser's, kernel-enforced per response.\n"
	if !bytes.Contains(overlayREADME, []byte(newText)) {
		t.Fatal("rig: the README's policy sentence is not wrapped as this test expects")
	}
	previous := bytes.Replace(overlayREADME, []byte(newText), []byte(oldText), 1)
	n := len(overlayShippedSeeds)
	if n < 2 || !slices.Contains(overlayShippedSeeds[:n-1], docSeedKey(nil, previous)) {
		t.Fatalf("rig: the edition rebuilt here is not one shipped before the current (%s)", docSeedKey(nil, previous))
	}

	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "the-ledger-path.jsonl")
	a := &App{cfg: &Config{Identity: IdentityConfig{LedgerPath: ledgerPath}}}
	a.snapshotUILayoutPath(ledgerPath)
	readme := filepath.Join(dir, "ui", "README.md")
	if err := os.MkdirAll(filepath.Dir(readme), 0o755); err != nil {
		t.Fatal(err)
	}

	// Untouched previous edition: upgraded, with nothing left beside it.
	if err := os.WriteFile(readme, previous, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	if got, _ := os.ReadFile(readme); !bytes.Equal(got, overlayREADME) {
		t.Fatal("an identity's untouched copy of the previous edition was not upgraded: it still says the policy keeps overlay code from beaconing out")
	}
	if _, err := os.Stat(readme + sidecarSuffix); !os.IsNotExist(err) {
		t.Fatalf("an upgraded README left a copy beside it: %v", err)
	}

	// Edited previous edition: theirs, with this edition beside it.
	edited := append(append([]byte{}, previous...), []byte("\nMy own note on the policy.\n")...)
	if err := os.WriteFile(readme, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	a.seedOverlayREADME()
	if got, _ := os.ReadFile(readme); !bytes.Equal(got, edited) {
		t.Fatal("an identity's edited README was replaced")
	}
	if got, err := os.ReadFile(readme + sidecarSuffix); err != nil || !bytes.Equal(got, overlayREADME) {
		t.Fatalf("the current edition was not left beside an edited README: %v", err)
	}
}
