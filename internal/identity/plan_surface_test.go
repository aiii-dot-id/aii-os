package identity

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/limits"
)

// TestWorkUpdatePlanSurface pins CS-4's verb surface: work.update
// accepts optional focus / next_move / plan independently of state;
// omitted fields are untouched; empty string clears; the render in
// buildWorkState carries them verbatim under the active session.
func TestWorkUpdatePlanSurface(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)

	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{
			"action":      "start",
			"description": "CS-4 plan surface",
		}); err != nil {
		t.Fatalf("work start failed: %v", err)
	}

	// State-only update: plan fields must remain untouched (empty).
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{
			"action": "update",
			"state":  "grounding seams",
		}); err != nil {
		t.Fatalf("state-only update failed: %v", err)
	}
	ws, _ := st.ActiveWorkSession()
	if ws.Focus != "" || ws.Plan != "" {
		t.Fatalf("state-only update touched plan fields: focus %q plan %q", ws.Focus, ws.Plan)
	}

	// Plan fields alongside state.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{
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

	// Clear: empty string clears plan, siblings untouched.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{
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

	// Estimate-only and standing-only updates are also presence-aware:
	// neither field belongs to work_sessions.state.
	for _, args := range []map[string]any{
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

// TestSpawnReturnCarriesHarvestContract pins the linkage convention
// taught at the moment of use (CS-4): spawn's return names the
// harvest contract — outcome returns, wakes you, record the ID.
func TestSpawnReturnCarriesHarvestContract(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(2, 2, 20, 600) // enable sub-agents for this test

	out, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{
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

func TestTheWorkVerbStatesWhatTheAsksUsedTo(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	_, err := engine.ExecuteAction(context.Background(), "verb", "work",
		map[string]any{"action": "update", "steps": 4})
	if err == nil || !strings.Contains(err.Error(), "no active work session") || !strings.Contains(err.Error(), "`work start`") {
		t.Fatalf("a session-bound update with no session must name `work start`: %v", err)
	}
	var props map[string]any
	for _, v := range Verbs() {
		if v.Name == "work" {
			props, _ = v.Params["properties"].(map[string]any)
		}
	}
	desc := func(key string) string {
		p, _ := props[key].(map[string]any)
		s, _ := p["description"].(string)
		return s
	}
	for _, want := range []string{"twice this many calls", "at 32", "four more rounds", "fresh turn"} {
		if !strings.Contains(desc("steps"), want) {
			t.Errorf("the steps schema does not state the cap rule (%q): %s", want, desc("steps"))
		}
	}
	if d := desc("independent"); strings.Contains(d, "will ask") || !strings.Contains(d, "Nothing asks you for it") {
		t.Errorf("the independent schema promises an ask the loop no longer makes: %s", d)
	}
}

func TestTheDefaultStepsSentenceIsUnchanged(t *testing.T) {
	const before = "(update, optional) how many tool calls you expect the rest of this turn to take. It is your budget: the turn checkpoints at twice this many calls (with none declared, at 32), gives you four more rounds to say where it stands, and the work continues in a fresh turn while its session is active. A later declaration revises it. Stated BEFORE the work, compared against what it took, and reported back to you in your rhythm line"
	for name, verbs := range map[string][]Verb{"the registry": Verbs(), "a turn at the defaults": VerbsAt(conversation.Checkpoint{}),
		"a turn handed the defaults": VerbsAt(conversation.Checkpoint{Calls: limits.Of(32), GraceRounds: 4})} {
		if got := stepsOf(t, verbs); got != before {
			t.Errorf("%s: the default steps sentence changed:\n got %s\nwant %s", name, got, before)
		}
	}
}

// stepsOf is the work verb's steps= description among verbs.
func stepsOf(t *testing.T, verbs []Verb) string {
	t.Helper()
	for _, v := range verbs {
		if v.Name == "work" {
			return v.Params["properties"].(map[string]any)["steps"].(map[string]any)["description"].(string)
		}
	}
	t.Fatal("no work verb")
	return ""
}

// THE SENTENCE STATES THE TURN'S OWN CHECKPOINT. The floor and the grace
// are the operator's (agency.turn_checkpoint_calls and its grace rounds),
// so the tool list a turn is sent is written from the pair its loop holds
// it to: VerbsAt changes that one sentence, in a copy; every verb is
// otherwise the registry's, and the registry is not written to.
func TestVerbsAtStatesTheTurnsCheckpoint(t *testing.T) {
	registry := stepsOf(t, Verbs())
	at := VerbsAt(conversation.Checkpoint{Calls: limits.Of(500), GraceRounds: 1})
	got := stepsOf(t, at)
	for _, want := range []string{"twice this many calls", "(with none declared, at 500)", "gives you one more round to say", "fresh turn"} {
		if !strings.Contains(got, want) {
			t.Errorf("the steps sentence at a floor of 500 and a grace of 1 lacks %q: %s", want, got)
		}
	}
	if s := stepsOf(t, VerbsAt(conversation.Checkpoint{Calls: limits.Of(64), GraceRounds: 12})); !strings.Contains(s, "(with none declared, at 64), gives you 12 more rounds to say") {
		t.Errorf("a count past ten is written in figures: %s", s)
	}
	// The slack on a declared estimate, as the sentence has always said
	// the default: "twice".
	for slack, want := range map[int]string{1: "the turn checkpoints at this many calls", 3: "the turn checkpoints at three times this many calls", 12: "the turn checkpoints at 12 times this many calls"} {
		if s := stepsOf(t, VerbsAt(conversation.Checkpoint{Slack: slack})); !strings.Contains(s, want) {
			t.Errorf("at a slack of %d the sentence lacks %q: %s", slack, want, s)
		}
	}
	// Nothing else of any verb differs: name, description, and every
	// parameter but that one sentence.
	without := func(v Verb) map[string]any {
		params := maps.Clone(v.Params)
		if props, ok := params["properties"].(map[string]any); ok && v.Name == "work" {
			props = maps.Clone(props)
			delete(props, "steps")
			params["properties"] = props
		}
		return params
	}
	all := Verbs()
	if len(at) != len(all) {
		t.Fatalf("VerbsAt is %d verbs; the registry has %d", len(at), len(all))
	}
	for i, v := range all {
		if at[i].Name != v.Name || at[i].Description != v.Description || !reflect.DeepEqual(without(at[i]), without(v)) {
			t.Errorf("VerbsAt changed more of %s than its steps sentence", v.Name)
		}
	}
	if again := stepsOf(t, Verbs()); again != registry {
		t.Errorf("VerbsAt wrote to the registry: its steps sentence is now %s", again)
	}
}
