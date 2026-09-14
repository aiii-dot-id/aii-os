package identity

import (
	"context"
	"testing"
)

// .
// .
// .
func TestReview2UnservedIsNotPartialSuccess(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "start", "description": "unserved leg"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	ws, _ := st.ActiveWorkSession()
	id := ws.ID
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{"action": "deliver", "result": "unserved: nothing landed"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	got, _ := st.WorkSessionByID(id)
	if got.Evidence == "partial_or_mixed" {
		t.Fatalf("an unserved delivery must not be stamped partial_or_mixed (partial success); got %q", got.Evidence)
	}
	if got.Evidence != "" {
		t.Fatalf("an unspecified unserved delivery should be unclassified, got %q", got.Evidence)
	}
	// .
	for _, tc := range []struct{ result, want string }{
		{"served: did it here", "completed_locally"},
		{"partial: some landed", "partial_or_mixed"},
	} {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
			map[string]interface{}{"action": "start", "description": "leg"}); err != nil {
			t.Fatal(err)
		}
		w, _ := st.ActiveWorkSession()
		if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
			map[string]interface{}{"action": "deliver", "result": tc.result}); err != nil {
			t.Fatal(err)
		}
		g, _ := st.WorkSessionByID(w.ID)
		if g.Evidence != tc.want {
			t.Fatalf("%q → %q, want %q", tc.result, g.Evidence, tc.want)
		}
	}
}
