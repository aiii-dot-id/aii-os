package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestWorkUpdatePlanSurface(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":      "start",
			"description": "CS-4 plan surface",
		}); err != nil {
		t.Fatalf("work start failed: %v", err)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action": "update",
			"state":  "grounding seams",
		}); err != nil {
		t.Fatalf("state-only update failed: %v", err)
	}
	ws, _ := st.ActiveWorkSession()
	if ws.Focus != "" || ws.Plan != "" {
		t.Fatalf("state-only update touched plan fields: focus %q plan %q", ws.Focus, ws.Plan)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action":    "update",
			"state":     "seams grounded",
			"focus":     "land CS-4 plan surface",
			"next_move": "render test",
			"plan":      "## Plan\n- [ ] schema → store → verb → render\n- cite ws_ IDs",
		}); err != nil {
		t.Fatalf("plan update failed: %v", err)
	}
	ws, _ = st.ActiveWorkSession()
	if ws.Focus != "land CS-4 plan surface" || ws.NextMove != "render test" {
		t.Fatalf("plan fields diverged: focus %q next %q", ws.Focus, ws.NextMove)
	}
	if !strings.Contains(ws.Plan, "cite ws_ IDs") {
		t.Fatalf("plan not stored verbatim: %q", ws.Plan)
	}

	// .
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action": "update",
			"plan":   "",
		}); err != nil {
		t.Fatalf("plan clear failed: %v", err)
	}
	ws, _ = st.ActiveWorkSession()
	if ws.Plan != "" {
		t.Fatalf("empty plan did not clear: %q", ws.Plan)
	}
	if ws.Focus != "land CS-4 plan surface" {
		t.Fatalf("clearing plan touched focus: %q", ws.Focus)
	}
	if ws.State != "seams grounded" {
		t.Fatalf("plan-only update erased state: %q", ws.State)
	}

	// .
	// .
	for _, args := range []map[string]interface{}{
		{"action": "update", "steps": 4},
		{"action": "update", "standing": "waiting without abandoning the session"},
	} {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "work", args); err != nil {
			t.Fatalf("presence-only update failed: %v", err)
		}
		ws, _ = st.ActiveWorkSession()
		if ws.State != "seams grounded" {
			t.Fatalf("omitted state cleared by %v: %q", args, ws.State)
		}
	}
}

// .
// .
// .
func TestSpawnReturnCarriesHarvestContract(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(2, 2, 20, 600)

	out, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]interface{}{
			"action": "spawn",
			"goal":   "verify the plan surface round-trips",
		})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	for _, want := range []string{"wakes you to harvest", "record this ID in your plan"} {
		if !strings.Contains(out, want) {
			t.Fatalf("spawn return missing %q:\n%s", want, out)
		}
	}
}
