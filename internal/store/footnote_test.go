package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A footnote projects nothing: no receipt row, no error.
func TestAFootnoteProjectsNoReceipt(t *testing.T) {
	st, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evt := &ledger.Event{Seq: 4, Type: ledger.EventSystemWitnessed, Payload: []byte(`{"receipt_before_rewrap":{"ledger_ordinal":3,"ledger_hash":"sha256:old","range_hash":"x"}}`)}
	if err := st.materializeSystemWitnessed(evt); err != nil {
		t.Fatalf("a footnote must project nothing, not fail: %v", err)
	}
	if ordinal, raw, err := st.LastWitnessReceipt(); err != nil || ordinal != 0 || len(raw) != 0 {
		t.Fatalf("a footnote left a receipt row: ordinal=%d raw=%d err=%v", ordinal, len(raw), err)
	}
	if err := st.materializeSystemWitnessed(&ledger.Event{Seq: 5, Type: ledger.EventSystemWitnessed, Payload: []byte(`{"note":"neither"}`)}); err == nil {
		t.Fatal("a witnessed record with neither a receipt nor a footnote materialized")
	}
}

// A LATER VERSION'S HEAD KEEPS ITS CONTEXT DIGEST IN THE PROJECTION: its
// receipt signs the digest, and the anchorer restores its last receipt from
// this row (witness.NewAnchorer), so the row is the record's receipt,
// digest and all. The context beside the receipt is the record's, not the
// row's. No column: the row is JSON text.
func TestALaterVersionsHeadKeepsItsContextDigestInTheProjection(t *testing.T) {
	st, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	digest := "sha256:" + strings.Repeat("c", 64)
	payload := `{"context":{"expected_head":null,"target_key_hash":"sha256:` + strings.Repeat("d", 64) + `","v":1},"receipt":{"context_sha256":"` + digest +
		`","identity_id":"did:aiii:identity:sha256:x","ledger_hash":"` + strings.Repeat("a", 64) + `","ledger_ordinal":3,"previous_witnessed_ledger_hash":"` + strings.Repeat("0", 64) +
		`","previous_witnessed_ledger_ordinal":-1,"witness_key_id":"aiii_witness_x","witness_sig_b64":"AAAA","witnessed_at":"2026-10-23T04:01:31Z"}}`
	if err := st.materializeSystemWitnessed(&ledger.Event{Seq: 4, Type: ledger.EventSystemWitnessed, Payload: []byte(payload)}); err != nil {
		t.Fatalf("a later version's head did not materialize: %v", err)
	}
	ordinal, raw, err := st.LastWitnessReceipt()
	if err != nil || ordinal != 3 {
		t.Fatalf("ordinal %d, %v", ordinal, err)
	}
	var row struct {
		ContextSHA256 string `json:"context_sha256"`
	}
	if err := json.Unmarshal(raw, &row); err != nil || row.ContextSHA256 != digest {
		t.Fatalf("the projection lost the receipt's context digest: %s (%v)", raw, err)
	}
}
