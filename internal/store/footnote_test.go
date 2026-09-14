package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
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
