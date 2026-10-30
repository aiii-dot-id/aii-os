package identity

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestWorkDefaultStartCompatibility(t *testing.T) {
	for _, args := range []map[string]any{
		nil,
		{"description": "implicit start"},
		{"action": "", "description": "explicit empty action"},
		{"action": "start", "description": "explicit start"},
	} {
		st, err := store.NewForTest(filepath.Join(t.TempDir(), "work.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		e := &Engine{store: st}
		if _, err := e.verbWork(t.Context(), args); err != nil {
			t.Fatalf("compatible start failed for %v: %v", args, err)
		}
		active, err := st.ActiveWorkSession()
		if err != nil || active == nil {
			t.Fatalf("compatible start missing: %+v, %v", active, err)
		}
		want, _ := args["description"].(string)
		if active.Description != want {
			t.Fatalf("description changed: got %q want %q", active.Description, want)
		}
	}
}

func TestWorkSchemaSeparatesCommandExecution(t *testing.T) {
	for _, verb := range VerbsAt(conversation.Checkpoint{}, nil) {
		if verb.Name != "work" {
			continue
		}
		props := verb.Params["properties"].(map[string]any)
		if _, ok := props["command"]; ok {
			t.Fatal("work cannot advertise the rejected shell field")
		}
		if !strings.Contains(verb.Description, "Commands go to shell, not here") {
			t.Fatal("work description must distinguish command execution")
		}
		state := props["state"].(map[string]any)["description"].(string)
		result := props["result"].(map[string]any)["description"].(string)
		if !strings.Contains(state, "deliver closes it") || !strings.HasPrefix(result, "(deliver) one line beginning served:") {
			t.Fatalf("authored prose is not a lifecycle transition; schema must distinguish update from deliver: state=%q result=%q", state, result)
		}
		return
	}
	t.Fatal("work schema missing")
}

func TestAuthoredDoneStateIsNotDelivery(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	id := startAndActiveID(t, e, st, "authored state versus delivery")
	if _, err := e.verbWork(t.Context(), map[string]any{"action": "update", "state": "done; session closed"}); err != nil {
		t.Fatal(err)
	}
	ws, err := st.WorkSessionByID(id)
	if err != nil || ws == nil || ws.Status != "active" || ws.State != "done; session closed" {
		t.Fatalf("update must preserve authored text without interpreting it as a transition: %+v %v", ws, err)
	}
	if _, err := e.verbWork(t.Context(), map[string]any{"action": "deliver", "result": "served: verified locally"}); err != nil {
		t.Fatal(err)
	}
	ws, err = st.WorkSessionByID(id)
	if err != nil || ws == nil || ws.Status != "delivered" {
		t.Fatalf("the explicit delivery must close the session: %+v %v", ws, err)
	}
}
