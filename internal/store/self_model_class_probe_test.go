package store

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// The generic citation refusal sent the live corrective round in
// circles on 2026-08-26: the model cited two plain beliefs as
// working_style for hours, and "belongs to another class" never said
// WHICH. The refusal now names where the id actually lives.
func TestCitationRefusalNamesTheActualClass(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`INSERT INTO ledger (seq, prev_hash, timestamp, type, author, ring, payload, content_hash, signature, sig_key_id) VALUES (1, 'h0', '2026-08-26T00:00:00Z', 'belief.create', 'test', 3, '{}', 'c1', 's1', 'k1')`); err != nil {
		t.Fatalf("plant ledger parent: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO beliefs (id, statement, ring, confidence, archived, first_seq, last_seq) VALUES ('item_b1', 'tests are evidence', 3, 0.8, 0, 1, 1)`); err != nil {
		t.Fatalf("plant belief: %v", err)
	}

	if got := s.selfModelClassesLocked("item_b1"); len(got) != 1 || got[0] != "beliefs" {
		t.Fatalf("class probe found %v, want [beliefs]", got)
	}

	payload := &ledger.SelfModelSynthesisPayload{
		ID:               "sms_probe_1",
		SynthesisText:    "a portrait",
		ContinuityThread: "a thread",
		SourceEntityRefs: []ledger.SelfModelSourceRef{{Class: "working_style", ID: "item_b1"}},
	}
	_, _, err := s.validateSelfModelPayloadLocked(payload)
	if err == nil {
		t.Fatal("a mis-classed citation validated")
	}
	if !strings.Contains(err.Error(), "exists under beliefs") {
		t.Fatalf("the refusal does not name the real class: %v", err)
	}

	// An id that exists NOWHERE keeps the honest generic sentence — a
	// probe that names classes for ghosts would be inventing evidence.
	payload.SourceEntityRefs = []ledger.SelfModelSourceRef{{Class: "working_style", ID: "item_ghost"}}
	_, _, err = s.validateSelfModelPayloadLocked(payload)
	if err == nil || strings.Contains(err.Error(), "exists under") {
		t.Fatalf("ghost-id refusal wrong: %v", err)
	}
}
