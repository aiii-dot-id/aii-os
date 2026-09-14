package identity

import (
	"context"
	"testing"
)

// .
// .
// .
// .
func TestWorkUpdateEvidenceSurface(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "start", "description": "evidence surface"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":            "update",
			"next_move":         "wire the resume card",
			"expected_evidence": "the card renders once",
			"falsifier":         "two return surfaces render",
			"decision_needed":   "confirm apt is Ubuntu's update path",
		}); err != nil {
		t.Fatalf("work update evidence: %v", err)
	}
	ws, _ := st.ActiveWorkSession()
	if ws.ExpectedEvidence != "the card renders once" || ws.Falsifier != "two return surfaces render" || ws.DecisionNeeded != "confirm apt is Ubuntu's update path" {
		t.Fatalf("evidence not persisted through the verb: %+v", ws)
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "update", "state": "still going"}); err != nil {
		t.Fatalf("state-only update: %v", err)
	}
	ws, _ = st.ActiveWorkSession()
	if ws.Falsifier != "two return surfaces render" {
		t.Fatalf("state-only update cleared the falsifier: %+v", ws)
	}
}
