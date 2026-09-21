package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .
// .
func TestACitationOfASealedRecordIsCrossCheckedUnderTheKeysBesideItsLedger(t *testing.T) {
	peer := newSealedFixture(t)
	head := peer.bootAndAnchor(t)
	var cited *ledger.Event
	if err := ledger.Stream(peer.ledgerPath, func(evt *ledger.Event) error {
		if evt.Seq == 1 && evt.Sealed() {
			e := *evt
			cited = &e
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if cited == nil || head < 2 {
		t.Fatalf("fixture: the peer's first record is not sealed (head %d)", head)
	}
	peerKey, err := crypto.LoadKeyPair(peer.keyPath)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	keyPath, ledgerPath, _ := birthFixture(t, dir, "Citing")
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{
		"id": "exp_cites_sealed", "content": "the peer's first record", "category": "observation", "provenance": "self",
		"cites": []ledger.Citation{{Identity: peerKey.Fingerprint(), Seq: cited.Seq, EntryHash: cited.EntryHash()}},
	}, kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()

	loaded := map[string]bool{}
	load := func(ledgerDir string) (genesis.Beside, error) {
		loaded[ledgerDir] = true
		beside, err := witness.LoadBeside(ledgerDir, peer.platform.Env)
		if err != nil {
			return nil, err
		}
		return beside, nil
	}
	out, errOut, code := genesis.VerifyCommand(ledgerPath, []string{peer.ledgerPath}, load)
	if code != genesis.ExitVerified || !strings.Contains(out, "CROSS-CHECKED: all 1 citation(s)") {
		t.Fatalf("a citation of a sealed record: %d %q %q", code, out, errOut)
	}
	if !loaded[peer.dir] || !loaded[dir] {
		t.Fatalf("each ledger verifies under the keys beside IT; keys were loaded from %v", loaded)
	}
}
