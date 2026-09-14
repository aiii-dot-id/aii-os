package witness

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
func TestAFootnoteIsAcceptedAndLeavesTheChainAlone(t *testing.T) {
	f := persistedFixture(t)
	keys, err := LoadWitnessKeys(f.dir, f.p.env)
	if err != nil {
		t.Fatal(err)
	}
	lg, kp := testLedger(t, 3)
	defer lg.Close()
	if _, err := lg.Append(ledger.EventSystemWitnessed, kp.Fingerprint(), 0, map[string]interface{}{
		"receipt_before_rewrap": map[string]interface{}{"ledger_ordinal": 2, "ledger_hash": "sha256:old", "range_hash": "x", "witness_version": "1.0"},
	}, kp); err != nil {
		t.Fatal(err)
	}
	footnote := headOf(t, lg)
	const id = "did:aiii:identity:sha256:test"
	r := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: 4, LedgerHash: lg.LastHash()}, -1, ZeroLedgerHash)
	if _, err := (testMinter{lg, kp}).MintWitnessed(r, f.fw.witnessEnv.KeyID); err != nil {
		t.Fatal(err)
	}
	head := headOf(t, lg)

	hv := NewHeadVerifier(keys)
	if err := hv.VerifyHead(footnote); err != nil {
		t.Fatalf("a footnote must be accepted: %v", err)
	}
	if err := hv.VerifyHead(head); err != nil {
		t.Fatalf("the first real head after a footnote chains from (-1, zero): %v", err)
	}
	if hv.Footnotes() != 1 || hv.Verified() != 1 {
		t.Fatalf("footnotes=%d verified=%d", hv.Footnotes(), hv.Verified())
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(keys)); err != nil || n != 5 {
		t.Fatalf("whole chain: n=%d err=%v", n, err)
	}
	if err := NewHeadVerifier(keys).VerifyHead(&ledger.Event{Seq: 9, Type: ledger.EventSystemWitnessed, Payload: []byte(`{"note":"neither"}`)}); err == nil {
		t.Fatal("a witnessed record with neither a receipt nor a footnote verified")
	}
}
