package identity

import (
	"errors"
	"strings"
	"testing"
)

// A START WHOSE CARD FAILS AFTER ITS SESSION WAS WRITTEN NAMES THAT SESSION,
// so it is resumed and never started again; the session stands.
func TestAStartWhoseCardFailsAfterTheWriteNamesItsSession(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	e.askProposer = func(string, string, []string, string) error { return errors.New("the page is gone") }
	_, err := e.verbWork(t.Context(), map[string]any{"action": "start", "description": "ask late", "decision_needed": "which way?"})
	ws, rerr := st.ActiveWorkSession()
	if rerr != nil || ws == nil {
		t.Fatalf("the session was not written: %v", rerr)
	}
	if err == nil || !strings.HasPrefix(err.Error(), "Started "+ws.ID+", but ") || !strings.Contains(err.Error(), "do not start another") {
		t.Fatalf("the error does not name the session that stands (%s): %v", ws.ID, err)
	}
}
