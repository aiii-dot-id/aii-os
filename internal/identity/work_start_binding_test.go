package identity

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestChildWorkStartPreservesTheParentSession(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	parentID := startAndActiveID(t, e, st, "parent task")
	plan := "read the inputs, make the change, verify it"
	if _, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "update", "plan": plan}); err != nil {
		t.Fatal(err)
	}
	subAgentSession(t, st, "ws_child", "bounded child task", "")
	parentBefore, err := st.WorkSessionByID(parentID)
	if err != nil {
		t.Fatal(err)
	}
	childBefore, err := st.WorkSessionByID("ws_child")
	if err != nil {
		t.Fatal(err)
	}
	var sessionsBefore, recordsBefore int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&recordsBefore); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), SubagentWorkSession{}, "ws_child")
	_, err = e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "unrelated session"})
	if err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (start.worker_session)") {
		t.Errorf("child start was not refused before effect: %v", err)
	} else {
		for _, route := range []string{"ws_child", "work update", "work deliver"} {
			if !strings.Contains(err.Error(), route) {
				t.Errorf("refusal omitted %q: %v", route, err)
			}
		}
	}
	active, err := st.ActiveWorkSession()
	if err != nil || active == nil || active.ID != parentID {
		t.Fatalf("child start redirected the parent: active=%+v err=%v; want %s", active, err, parentID)
	}
	parentAfter, err := st.WorkSessionByID(parentID)
	if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) {
		t.Fatalf("parent changed: %+v err=%v", parentAfter, err)
	}
	childAfter, err := st.WorkSessionByID("ws_child")
	if err != nil || !reflect.DeepEqual(childBefore, childAfter) {
		t.Fatalf("child changed: %+v err=%v", childAfter, err)
	}
	var sessionsAfter, recordsAfter int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&sessionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&recordsAfter); err != nil {
		t.Fatal(err)
	}
	if sessionsAfter != sessionsBefore || recordsAfter != recordsBefore {
		t.Fatalf("refused start wrote state: sessions %d→%d, records %d→%d", sessionsBefore, sessionsAfter, recordsBefore, recordsAfter)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "state": "child progress"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "deliver", "result": "served: child completed"}); err != nil {
		t.Fatal(err)
	}
	childAfter, err = st.WorkSessionByID("ws_child")
	if err != nil || childAfter.State != "child progress" || childAfter.Status != "delivered" {
		t.Fatalf("child no longer acts on its bound session: %+v err=%v", childAfter, err)
	}
	active, err = st.ActiveWorkSession()
	if err != nil || active == nil || active.ID != parentID || active.Plan != plan {
		t.Fatalf("child completion changed the parent: %+v err=%v", active, err)
	}
	if _, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "deliver", "result": "served: parent completed"}); err != nil {
		t.Fatal(err)
	}
	parentAfter, err = st.WorkSessionByID(parentID)
	if err != nil || parentAfter.Status != "delivered" {
		t.Fatalf("parent delivery missed its session: %+v err=%v", parentAfter, err)
	}
}
