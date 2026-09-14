package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
func TestReview3VerifiedRequiresServedOutcome(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	start := func(desc string) string {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
			map[string]interface{}{"action": "start", "description": desc}); err != nil {
			t.Fatalf("start: %v", err)
		}
		ws, _ := st.ActiveWorkSession()
		return ws.ID
	}

	// .
	start("partial verify")
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "deliver", "result": "partial: some landed",
		"evidence": "locally_verified", "evidence_readback": "ran the check",
	}); err == nil {
		t.Fatal("locally_verified on a partial: outcome must be refused")
	} else if !strings.Contains(err.Error(), "served:") {
		t.Fatalf("the refusal must name the served: requirement: %v", err)
	}

	// .
	id := start("served verify")
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{
		"action": "deliver", "result": "served: did and checked it",
		"evidence": "locally_verified", "evidence_readback": "ran go test, saw PASS",
	}); err != nil {
		t.Fatalf("served verified deliver: %v", err)
	}
	g, _ := st.WorkSessionByID(id)
	if g.Evidence != "locally_verified" {
		t.Fatalf("verified served delivery not persisted: %q", g.Evidence)
	}
}
