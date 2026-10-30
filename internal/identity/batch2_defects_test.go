package identity

import (
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
)

func TestAStartCarryingUpdateFieldsAppliesThem(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	out, err := e.verbWork(t.Context(), map[string]any{"action": "start", "description": "check the bundle", "family": "bundle-check",
		"state": "reading", "focus": "the bundle", "next_move": "read it", "plan": "1. read\n2. check", "expected_evidence": "the digest matches",
		"falsifier": "the digest differs", "standing": "checking the bundle", "steps": float64(6)})
	if err != nil {
		t.Fatalf("a start with update's fields was refused: %v", err)
	}
	ws, err := st.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no session started: %+v %v", ws, err)
	}
	if !strings.Contains(out, ws.ID) {
		t.Errorf("the start does not name its session: %s", out)
	}
	for field, got := range map[string][2]string{
		"description": {ws.Description, "check the bundle"}, "family": {ws.Family, "bundle-check"}, "state": {ws.State, "reading"},
		"focus": {ws.Focus, "the bundle"}, "next_move": {ws.NextMove, "read it"}, "plan": {ws.Plan, "1. read\n2. check"},
		"expected_evidence": {ws.ExpectedEvidence, "the digest matches"}, "falsifier": {ws.Falsifier, "the digest differs"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s was not applied with the start: %q, want %q", field, got[0], got[1])
		}
	}
	if standing, err := st.StandingState(); err != nil || standing != "checking the bundle" {
		t.Errorf("standing was not applied with the start: %q %v", standing, err)
	}

	// A refused field refuses the start: nothing is written.
	e.workLineMaxChars = func() int { return 10 }
	if _, err := e.verbWork(t.Context(), map[string]any{"action": "start", "description": "a second", "next_move": "a line longer than ten"}); err == nil || !strings.Contains(err.Error(), "next_move") {
		t.Fatalf("a start with a refused field was not refused naming it: %v", err)
	}
	if again, _ := st.ActiveWorkSession(); again == nil || again.ID != ws.ID {
		t.Fatalf("a refused start changed the active session: %+v", again)
	}
	// Fields neither start nor update reads are refused, naming them.
	if _, err := e.verbWork(t.Context(), map[string]any{"action": "start", "description": "a third", "goal": "x", "result": "served: y"}); err == nil || !strings.Contains(err.Error(), "not goal, result;") {
		t.Fatalf("a start carrying another action's fields was not refused naming them: %v", err)
	}
}

func TestCommitOffersNoVariantOrArgumentThatOnlyRefuses(t *testing.T) {
	for _, v := range VerbsAt(conversation.Checkpoint{}, nil) {
		if v.Name != "commit" {
			continue
		}
		props := v.Params["properties"].(map[string]any)
		if _, ok := props["duplicate_ok"]; ok {
			t.Error("commit offers duplicate_ok, which it refuses")
		}
		variants := props["variant"].(map[string]any)["enum"].([]string)
		if slices.Contains(variants, "belief.attest") {
			t.Error("commit offers belief.attest, which was removed")
		}
	}
	e, _, _, _, _ := setupEngine(t)
	_, err := e.ExecuteAction(t.Context(), "verb", "commit", map[string]any{"variant": "belief.attest", "id": "b_x"})
	if err == nil || !strings.Contains(err.Error(), "rejected_before_effect (commit.variant)") || !strings.Contains(err.Error(), "belief.attest was removed") {
		t.Fatalf("belief.attest is not refused, typed and named: %v", err)
	}
}

// RECALL'S MEANING LAYER IS NOT THE PROVIDER'S ALONE. A plugin can be the
// meaning source, and the description said meaning matches join only when
// the provider names an embeddings model.
func TestRecallDoesNotSayOnlyTheProviderMakesMeaning(t *testing.T) {
	for _, v := range Verbs() {
		if v.Name == "recall" && strings.Contains(v.Description, "provider names an embeddings model") {
			t.Fatalf("recall's description says only the provider makes meaning: %s", v.Description)
		}
	}
}
