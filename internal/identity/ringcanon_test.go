package identity

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
// .
// .
func TestCommitRefusesNonIntegralRingBeforeAppend(t *testing.T) {
	engine, _, lg, _, _ := setupEngine(t)

	seqBefore := lg.LastSeq()
	_, err := engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{
		"variant":    "belief.upsert",
		"statement":  "float ring probe",
		"confidence": 0.5,
		"evidence":   "none",
		"ring":       3.5,
	})
	if err == nil {
		t.Fatal("commit with ring 3.5 must be refused")
	}
	if lg.LastSeq() != seqBefore {
		t.Fatalf("ledger advanced (%d -> %d) on a refused ring — the chain is poisoned: every future replay fails at this event",
			seqBefore, lg.LastSeq())
	}
}

// .
// .
// .
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
		Ring int `json:"ring"`
	}
	if err := json.Unmarshal(last.Payload, &p); err != nil {
		t.Fatalf("appended payload does not replay into the integer ring field: %v (payload %s)", err, string(last.Payload))
	}
	if p.Ring != 3 {
		t.Fatalf("payload ring = %d, want the validated 3", p.Ring)
	}
}
