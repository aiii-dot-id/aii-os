package identity

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSpawnStampsTheActualParentAndNestedParent(t *testing.T) {
	e, s, _, _, _ := setupEngine(t)
	e.SetAgencyLimits(3, 4, 20, 60)
	if err := s.StartWorkSession(t.Context(), "parent", "parent work"); err != nil {
		t.Fatal(err)
	}
	spawn := func(ctx context.Context, goal string) SubagentRequest {
		t.Helper()
		if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "spawn", "goal": goal, "parent_work_session_id": "forged"}); err != nil {
			t.Fatal(err)
		}
		var raw string
		if err := s.DB().QueryRow(`SELECT payload FROM work_queue WHERE json_extract(payload,'$.goal')=?`, goal).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var r SubagentRequest
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	child := spawn(t.Context(), "first")
	if child.ParentSessionID != "parent" {
		t.Fatalf("parent = %q", child.ParentSessionID)
	}
	if err := s.StartWorkSession(t.Context(), "unrelated", "unrelated work"); err != nil {
		t.Fatal(err)
	}
	grandchild := spawn(context.WithValue(t.Context(), SubagentWorkSession{}, child.SessionID), "second")
	if grandchild.ParentSessionID != child.SessionID {
		t.Fatalf("nested parent = %q want %q", grandchild.ParentSessionID, child.SessionID)
	}
}
