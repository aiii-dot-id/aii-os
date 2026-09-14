package identity

import (
	"context"
	"errors"
	"testing"
)

// .
// .
// .
// .
func TestWorkUpdateDecisionNeededReachesTheAskProposer(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	type call struct {
		session, text, connector string
		choices                  []string
	}
	var calls []call
	engine.SetAskProposer(func(session, text string, choices []string, connector string) error {
		calls = append(calls, call{session, text, connector, choices})
		return nil
	})
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "start", "description": "ask surface"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	ws, _ := st.ActiveWorkSession()
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":          "update",
			"decision_needed": "Aisle or window for the long leg?",
			"choices":         []interface{}{"aisle", " window ", ""},
			"connector":       "",
		}); err != nil {
		t.Fatalf("work update: %v", err)
	}
	if len(calls) != 1 || calls[0].session != ws.ID || calls[0].text != "Aisle or window for the long leg?" ||
		len(calls[0].choices) != 2 || calls[0].choices[1] != "window" || calls[0].connector != "" {
		t.Fatalf("the decision reached the proposer typed and trimmed: %+v", calls)
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "update", "state": "booking"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("a state-only update does not re-ask: %d calls", len(calls))
	}
	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "update", "decision_needed": "I need your email to read the inbox.", "connector": " email "}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "update", "decision_needed": ""}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[1].connector != "email" || calls[2].text != "" {
		t.Fatalf("connect then withdraw: %+v", calls)
	}
	// .
	engine.SetAskProposer(func(string, string, []string, string) error { return errors.New("the page is not up") })
	_, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "update", "decision_needed": "One or five years?"})
	if err == nil {
		t.Fatal("a proposer failure is told to the identity")
	}
	ws, _ = st.ActiveWorkSession()
	if ws.DecisionNeeded != "One or five years?" {
		t.Fatalf("the decision is stored before the card is placed: %q", ws.DecisionNeeded)
	}
}
