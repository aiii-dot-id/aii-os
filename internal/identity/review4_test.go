package identity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A RESUMED TURN'S RECALL AND SPAWN ARE ITS OWN SESSION'S (a review,
// 2026-11-06, round 4): with the older session resumed while a newer one
// is active, recall source=work lists the older's calls and a spawned child
// hangs under the older.
func TestAResumedTurnsRecallAndSpawnAreItsOwnSessions(t *testing.T) {
	r := newWorkRig(t)
	ctx := t.Context()
	if err := r.s.StartWorkSession(ctx, "ws_old", "the older work"); err != nil {
		t.Fatal(err)
	}
	r.call("main", "shell", `{"command":"old-cmd"}`, "old-output", interaction.Succeeded, store.ToolMeasurement{})
	if err := r.s.StartWorkSession(ctx, "ws_new", "the newer work"); err != nil {
		t.Fatal(err)
	}
	r.call("main", "shell", `{"command":"new-cmd"}`, "new-output", interaction.Succeeded, store.ToolMeasurement{})
	resumed := interaction.WithResumeSession(ctx, "ws_old")
	out, err := r.recall(resumed, map[string]any{"source": "work", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ws_old") || !strings.Contains(out, "old-output") || strings.Contains(out, "new-output") {
		t.Fatalf("the resumed turn's recall describes the newer session:\n%s", out)
	}
	plain, err := r.recall(ctx, map[string]any{"source": "work", "query": ""})
	if err != nil || !strings.Contains(plain, "new-output") {
		t.Fatalf("a plain turn's recall: %v\n%s", err, plain)
	}
	r.e.SetAgencyLimits(3, 3, 20, 600)
	if _, err := r.e.ExecuteAction(resumed, "verb", "work", map[string]any{"action": "spawn", "goal": "carry the older work on"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := r.s.DB().QueryRow(`SELECT payload FROM work_queue WHERE kind = ?`, SubagentWorkKind).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var request SubagentRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatal(err)
	}
	if request.ParentSessionID != "ws_old" {
		t.Fatalf("the resumed turn's child hangs under %q, want the resumed session", request.ParentSessionID)
	}
}

// THE REFLECTION CADENCE IS THE IDENTITY'S OWN: work
// update reflect_every=N writes it on the session; a count outside 0..1000
// is refused in the parameter's words.
func TestTheReflectionCadenceIsTheIdentitysOwn(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn-1")
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a long task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "reflect_every": float64(5)}); err != nil {
		t.Fatal(err)
	}
	if ws, _ := st.ActiveWorkSession(); ws.ReflectEvery != 5 {
		t.Fatalf("the cadence was not written: %+v", ws)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "reflect_every": float64(5000)}); err == nil || !strings.Contains(err.Error(), "a count of calls from 0 (none) to 1000") {
		t.Fatalf("an impossible cadence was accepted: %v", err)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "reflect_every": float64(0)}); err != nil {
		t.Fatal(err)
	}
	if ws, _ := st.ActiveWorkSession(); ws.ReflectEvery != 0 {
		t.Fatalf("the cadence was not cleared: %+v", ws)
	}
}
