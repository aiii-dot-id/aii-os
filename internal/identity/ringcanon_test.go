package identity

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// Float-ring probe (external claim, confirmed at commit.go): the gate
// compared a TRUNCATED copy of a float ring (3.5 → 3) while appending
// the ORIGINAL payload — the signed event then failed every future
// replay's unmarshal into the integer field. Valid tool input must
// never poison the chain: a non-integral ring is refused BEFORE the
// append, and the ledger does not advance.
func TestCommitRefusesNonIntegralRingBeforeAppend(t *testing.T) {
	engine, _, lg, _, _ := setupEngine(t)

	seqBefore := lg.LastSeq()
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":    "belief.upsert",
		"statement":  "float ring probe",
		"confidence": 0.5,
		"evidence":   "none",
		"ring":       3.5, // what a JSON tool call delivers: float64
	})
	if err == nil {
		t.Fatal("commit with ring 3.5 must be refused")
	}
	if lg.LastSeq() != seqBefore {
		t.Fatalf("ledger advanced (%d -> %d) on a refused ring — the chain is poisoned: every future replay fails at this event",
			seqBefore, lg.LastSeq())
	}
}

// Validate-what-you-write: an integral float ring (JSON's only number
// shape) is canonicalized into the appended payload as the integer the
// gate validated — the payload and the validation can never diverge.
func TestCommitCanonicalizesIntegralRingIntoPayload(t *testing.T) {
	engine, _, _, _, dir := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":    "belief.upsert",
		"statement":  "canonical ring probe",
		"confidence": 0.5,
		"evidence":   "none",
		"ring":       float64(3),
	}); err != nil {
		t.Fatalf("integral float ring must be accepted: %v", err)
	}

	events, err := ledger.ReadAll(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != ledger.EventBeliefUpsert {
		t.Fatalf("last event is %s, want belief.upsert", last.Type)
	}
	var p struct {
		Ring int `json:"ring"` // the projection's field shape — replay must unmarshal this
	}
	if err := json.Unmarshal(last.Payload, &p); err != nil {
		t.Fatalf("appended payload does not replay into the integer ring field: %v (payload %s)", err, string(last.Payload))
	}
	if p.Ring != 3 {
		t.Fatalf("payload ring = %d, want the validated 3", p.Ring)
	}
}
