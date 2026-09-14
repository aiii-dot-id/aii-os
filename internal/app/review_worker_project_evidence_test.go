package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/project"
)

// .
// .
// .
// .
// .
func TestReview877WorkerMustNotVerifyProject(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		blank       bool
	}{
		{"worker_locally_verified", "locally_verified", false},
		{"worker_host_receipted", "host_receipted", false},
		{"blank_ref_control", "locally_verified", true},
		{"ordinary_support_control", "worker_report_only", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := standingApp(t)
			root := t.TempDir()
			a.projects = project.NewManager(root)
			a.engine.SetProjects(projectsAdapter{a})
			p, err := a.projects.Create("synthetic-worker-boundary", "review only", "identity", nil,
				&project.Contract{Acceptance: []string{"external check passed"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			ref := filepath.Join(root, "nonexistent-receipt")
			if _, err := os.Stat(ref); !os.IsNotExist(err) {
				t.Fatalf("fake ref must not exist: %v", err)
			}
			if tc.blank {
				ref = " "
			}
			call := func(ctx context.Context, args map[string]interface{}) (string, bool) {
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				var c llm.ToolCall
				c.Type, c.Function.Name, c.Function.Arguments = "function", "work", string(raw)
				obs := a.executeToolCall(ctx, c)
				return obs.Text, obs.Failed
			}
			worker := context.WithValue(t.Context(), identity.SubagentWorkSession{}, "ws_synthetic_worker")
			worker = context.WithValue(worker, identity.SubagentDepth{}, 1)
			text, refused := call(worker, map[string]interface{}{
				"action": "project.evidence", "project": p.ID, "item": "external check passed",
				"class": tc.class, "ref": ref, "note": "synthetic assertion; no check executed",
			})
			// .
			stored, err := project.NewManager(root).Load(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			prog := project.DeriveContractProgress(stored.Contract, stored.Observations)
			t.Logf("evidence refused=%v response=%q stored_observations=%+v derived=%+v", refused, text, stored.Observations, prog)
			closeText, closeRefused := call(t.Context(), map[string]interface{}{"action": "project.close", "project": p.ID})
			closed, err := project.NewManager(root).Load(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("primary close refused=%v response=%q persisted_state=%s", closeRefused, closeText, closed.State)
			if tc.class == "worker_report_only" {
				if refused || len(stored.Observations) != 1 || prog.Items[0].State != project.AcceptanceSupported || !closeRefused || closed.State != "open" {
					t.Fatal("ordinary worker support must remain accepted without authorizing close")
				}
				return
			}
			if !refused || len(stored.Observations) != 0 || prog.ClosureAllowed || !closeRefused || closed.State != "open" {
				t.Errorf("worker verified evidence must be refused before mutation: refused=%v observations=%d derived=%s closureAllowed=%v closeRefused=%v persisted=%s", refused, len(stored.Observations), prog.Items[0].State, prog.ClosureAllowed, closeRefused, closed.State)
			}
		})
	}
}
