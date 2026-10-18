package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/project"
)

// Project focus may move while work remains alive. A spawn keeps its bound
// parent's project, including through the runner's host-stamped child context.
func TestSpawnProjectFollowsBoundParentAcrossFocusSwitch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		nested      bool
		switchFocus bool
	}{
		{"nested_unchanged_focus_control", true, false},
		{"resident_parent_after_focus_switch", false, true},
		{"nested_parent_after_focus_switch", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := standingApp(t)
			a.engine.SetAgencyLimits(3, 4, 20, 60)
			a.projects = project.NewManager(t.TempDir())
			projectA, err := a.projects.Create("project-a", "original task", "operator", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			projectB, err := a.projects.Create("project-b", "unrelated work", "operator", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.selectProject(projectA.ID); err != nil {
				t.Fatal(err)
			}
			if text, failed := workCall(t, a, `{"action":"start","description":"parent task"}`); failed {
				t.Fatal(text)
			}
			parent, err := a.store.ActiveWorkSession()
			if err != nil || parent == nil || parent.Project != projectA.ID {
				t.Fatalf("parent=%+v error=%v", parent, err)
			}
			spawn := func(ctx context.Context, goal string) identity.SubagentRequest {
				t.Helper()
				args, err := json.Marshal(map[string]any{"action": "spawn", "goal": goal, "parent_work_session_id": "forged-parent", "project_id": "forged-project"})
				if err != nil {
					t.Fatal(err)
				}
				var call llm.ToolCall
				call.ID, call.Type, call.Function.Name, call.Function.Arguments = "spawn-"+goal, "function", "work", string(args)
				obs := a.executeToolCall(ctx, call)
				if obs.Failed || obs.Refused {
					t.Fatalf("spawn: %+v", obs)
				}
				var payload string
				if err := a.store.DB().QueryRow(`SELECT payload FROM work_queue WHERE json_extract(payload,'$.goal')=?`, goal).Scan(&payload); err != nil {
					t.Fatal(err)
				}
				var result identity.SubagentRequest
				if err := json.Unmarshal([]byte(payload), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			ctx := t.Context()
			parentID := parent.ID
			if tc.nested {
				child := spawn(ctx, "first child")
				if child.ParentSessionID != parent.ID {
					t.Fatalf("first child parent=%s want=%s", child.ParentSessionID, parent.ID)
				}
				row, err := a.store.WorkSessionByID(child.SessionID)
				if err != nil || row == nil || row.Project != projectA.ID {
					t.Fatalf("first child=%+v error=%v", row, err)
				}
				parentID = child.SessionID
				ctx = context.WithValue(ctx, identity.SubagentWorkSession{}, child.SessionID)
				ctx = context.WithValue(ctx, identity.SubagentDepth{}, 1)
			}
			if tc.switchFocus {
				if _, err := a.selectProject(projectB.ID); err != nil {
					t.Fatal(err)
				}
				if got := a.store.ActiveProjectID(); got != projectB.ID {
					t.Fatalf("focus=%s want=%s", got, projectB.ID)
				}
			}
			next := spawn(ctx, "delegated after focus selection")
			if next.ParentSessionID != parentID {
				t.Fatalf("parent=%s want=%s", next.ParentSessionID, parentID)
			}
			persistedParent, err := a.store.WorkSessionByID(parentID)
			if err != nil || persistedParent == nil || persistedParent.Project != projectA.ID {
				t.Fatalf("parent changed=%+v error=%v", persistedParent, err)
			}
			persistedChild, err := a.store.WorkSessionByID(next.SessionID)
			if err != nil || persistedChild == nil {
				t.Fatalf("child=%+v error=%v", persistedChild, err)
			}
			t.Logf("host_parent=%s parent_project=%s child=%s child_project=%s active_project=%s", parentID, persistedParent.Project, next.SessionID, persistedChild.Project, a.store.ActiveProjectID())
			if persistedChild.Project != persistedParent.Project {
				t.Errorf("queued child adopted unrelated mutable focus: child project %q, bound parent project %q", persistedChild.Project, persistedParent.Project)
			}
		})
	}
}
