package witness

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A footnote — a witnessed record whose receipt attested an earlier
// shape — is accepted on the identity's proof, counted, and leaves the
// receipt chain where it was.
func TestAFootnoteIsAcceptedAndLeavesTheChainAlone(t *testing.T) {
	f := persistedFixture(t)
	keys, err := LoadWitnessKeys(f.dir, f.p.env)
	if err != nil {
		t.Fatal(err)
	}
	lg, kp := testLedger(t, 3)
	defer lg.Close()
	if _, err := lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{
		"receipt_before_rewrap": map[string]any{"ledger_ordinal": 2, "ledger_hash": "sha256:old", "range_hash": "x", "witness_version": "1.0"},
	}, kp); err != nil {
		t.Fatal(err)
	}
	footnote := headOf(t, lg) // seq 4
	const id = "did:aiii:identity:sha256:test"
	r := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: 4, LedgerHash: lg.LastHash()}, -1, ZeroLedgerHash)
	if _, err := (testMinter{lg, kp}).MintWitnessed(r, f.fw.witnessEnv.KeyID); err != nil {
		t.Fatal(err)
	}
	head := headOf(t, lg) // seq 5

	hv := NewHeadVerifier(keys)
	if _, err := hv.VerifyHead(footnote, nil); err != nil {
		t.Fatalf("a footnote must be accepted: %v", err)
	}
	if _, err := hv.VerifyHead(head, nil); err != nil {
		t.Fatalf("the first real head after a footnote chains from (-1, zero): %v", err)
	}
	if hv.Footnotes() != 1 || hv.Verified() != 1 {
		t.Fatalf("footnotes=%d verified=%d", hv.Footnotes(), hv.Verified())
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(keys)); err != nil || n != 5 {
		t.Fatalf("whole chain: n=%d err=%v", n, err)
	}
	if _, err := NewHeadVerifier(keys).VerifyHead(&ledger.Event{Seq: 9, Type: ledger.EventSystemWitnessed, Payload: []byte(`{"note":"neither"}`)}, nil); err == nil {
		t.Fatal("a witnessed record with neither a receipt nor a footnote verified")
	}
}
