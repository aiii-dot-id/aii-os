package identity

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestWorkRejectsCommandBeforeDispatch(t *testing.T) {

	for _, action := range []string{"", "start", "update", "deliver", "spawn", "status", "yield", "project.create", "alarm.set", "backup.take"} {
		for _, command := range []any{"printf private-command", "", nil, 17} {
			args := map[string]any{"command": command, "cwd": "/private-working-directory"}
			if action != "" {
				args["action"] = action
			}
			out, err := (&Engine{}).verbWork(t.Context(), args)
			if err == nil || out != "" {
				t.Fatalf("action %q, command %T: output=%q error=%v", action, command, out, err)
			}
			for _, part := range []string{"rejected_before_effect (work.arguments)", "sent to work", "No command was executed", "no work state was changed", "Use shell with command and optional cwd"} {
				if !strings.Contains(err.Error(), part) {
					t.Fatalf("diagnostic missing %q: %v", part, err)
				}
			}
			if strings.Contains(err.Error(), "private-command") || strings.Contains(err.Error(), "private-working-directory") || strings.Contains(err.Error(), "start.worker_session") {
				t.Fatalf("diagnostic exposes input or gives the wrong cause: %v", err)
			}
		}
	}
}

func TestWorkMisroutedShellPreservesState(t *testing.T) {
	e, st, lg, _, root := setupEngine(t)
	parentID := startAndActiveID(t, e, st, "preserve parent work")
	subAgentSession(t, st, "ws_child", "preserve child work", "")
	parentBefore, err := st.WorkSessionByID(parentID)
	if err != nil {
		t.Fatal(err)
	}
	childBefore, err := st.WorkSessionByID("ws_child")
	if err != nil {
		t.Fatal(err)
	}
	counts := func() []int {
		t.Helper()
		var result []int
		for _, table := range []string{"work_sessions", "work_queue", "conversations"} {
			var count int
			if err := st.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			result = append(result, count)
		}
		return result
	}
	before, seq := counts(), lg.LastSeq()
	marker := filepath.Join(root, "command-must-not-run")

	args := map[string]any{"command": "cat > '" + marker + "' <<'END'\nsynthetic input\nEND", "cwd": root}
	for _, ctx := range []context.Context{t.Context(), context.WithValue(t.Context(), SubagentWorkSession{}, "ws_child")} {
		if _, err := e.ExecuteAction(ctx, "verb", "work", args); err == nil || !strings.Contains(err.Error(), "work.arguments") {
			t.Fatalf("wrong-tool command was not diagnosed: %v", err)
		}
	}
	parentAfter, err := st.WorkSessionByID(parentID)
	if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) {
		t.Fatalf("parent changed: %+v, %v", parentAfter, err)
	}
	childAfter, err := st.WorkSessionByID("ws_child")
	if err != nil || !reflect.DeepEqual(childBefore, childAfter) {
		t.Fatalf("child changed: %+v, %v", childAfter, err)
	}
	active, err := st.ActiveWorkSession()
	if err != nil || active == nil || active.ID != parentID {
		t.Fatalf("active parent changed: %+v, %v", active, err)
	}
	if after := counts(); !reflect.DeepEqual(before, after) || lg.LastSeq() != seq {
		t.Fatalf("refusal wrote session/queue/conversation/ledger state: %v to %v; seq %d to %d", before, after, seq, lg.LastSeq())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command marker exists or cannot be checked: %v", err)
	}
}

func TestWorkDefaultStartCompatibility(t *testing.T) {
	for _, args := range []map[string]any{
		nil,
		{"description": "implicit start"},
		{"action": "", "description": "explicit empty action"},
		{"action": "start", "description": "explicit start"},
		{"description": "unknown metadata remains ignored", "cwd": "metadata-only", "attributes": map[string]any{"command": "nested metadata"}},
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
	for _, verb := range VerbsAt(conversation.Checkpoint{}) {
		if verb.Name != "work" {
			continue
		}
		props := verb.Params["properties"].(map[string]any)
		if _, ok := props["command"]; ok {
			t.Fatal("work cannot advertise the rejected shell field")
		}
		if !strings.Contains(verb.Description, "Command execution belongs to shell") {
			t.Fatal("work description must distinguish command execution")
		}
		state := props["state"].(map[string]any)["description"].(string)
		result := props["result"].(map[string]any)["description"].(string)
		if !strings.Contains(state, "Does not close the session; use deliver to finish") || !strings.Contains(result, "closes the active session") {
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
