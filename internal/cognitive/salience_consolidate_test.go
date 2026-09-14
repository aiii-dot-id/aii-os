package cognitive

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type fakeDecisionLog struct{ decisions []store.MemoryDecision }

func (f *fakeDecisionLog) RecordMemoryDecision(d store.MemoryDecision) error {
	f.decisions = append(f.decisions, d)
	return nil
}

// .
// .
// .
// .
// .
func TestConsolidateSalienceKeepsAWeakCandidateAsAMemo(t *testing.T) {
	weak := strings.Repeat("it is what it is and that is that ", 14)
	st, lg, _, c := consolidateBench(`{
		"operations": [
			{"op": "upsert", "id": "n1", "statement": "The operator ships at midnight", "confidence": 0.9, "evidence": ["e1", "e2", "e3"]},
			{"op": "upsert", "id": "n2", "statement": "` + weak + `", "confidence": 0.1, "evidence": ["e1"]}
		],
		"ring3_view": "You believe the operator ships at midnight."
	}`)
	log := &fakeDecisionLog{}
	c.SetDecisionLog(log)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	acts := withoutEdges(lg.appended)
	if len(acts) != 2 || acts[0] != ledger.EventBeliefUpsert || acts[1] != ledger.EventConsolidationRun {
		t.Fatalf("want [upsert, run]: the weak candidate mints nothing, got %v", lg.appended)
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("the memo's experiences are consumed like the rest, %d remain", st.unprocessedCnt)
	}
	if len(log.decisions) != 2 {
		t.Fatalf("two decisions logged, got %+v", log.decisions)
	}
	var belief, memo *store.MemoryDecision
	for i := range log.decisions {
		switch log.decisions[i].Decision {
		case "belief":
			belief = &log.decisions[i]
		case "memo":
			memo = &log.decisions[i]
		}
	}
	if belief == nil || belief.Seq == 0 || belief.Kind != "salience" || belief.Facility != "consolidate" {
		t.Fatalf("the minted belief's decision carries its seq: %+v", log.decisions)
	}
	if memo == nil || memo.Seq != 0 || memo.Score >= belief.Score || memo.Record["candidate"] != strings.TrimSpace(weak) {
		t.Fatalf("the memo's decision carries no seq and a lower score: %+v", log.decisions)
	}
	if _, ok := memo.Record["features"]; !ok {
		t.Fatalf("a decision is logged with its features: %+v", memo.Record)
	}
}
