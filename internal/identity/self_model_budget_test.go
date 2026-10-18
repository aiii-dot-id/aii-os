package identity

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// THE PORTRAIT'S BUDGET IS THE OPERATOR'S NUMBER, AND IT IS ASKED OF A NEW
// PORTRAIT ONLY. The budget was a number in the code, checked inside the
// materializer, which runs for a new portrait and again for every recorded
// one when the record is replayed. A check there cannot be an operator's
// to set: the day the number is lowered, a portrait the record already
// holds is refused at replay and the store cannot be rebuilt from its own
// ledger.
//
// Here a portrait over the default budget is refused before anything is
// appended, by a refusal that names the setting. With the budget raised it
// is accepted. Then the operator lowers the budget below it: a new
// portrait over the new number is refused and one under it is accepted,
// and the whole record, the long portrait in it, replays into a fresh
// store that holds the lowered number. A budget of zero is the default.
func TestThePortraitsBudgetIsTheOperatorsAndIsAskedOfANewPortraitOnly(t *testing.T) {
	engine, st, lg, kp, dir := setupEngine(t)
	refs := selfModelSources(t, st, lg, kp)
	ctx := context.Background()
	portrait := func(id, previous, text string) map[string]any {
		args := selfModelArgs(id, refs)
		args["synthesis_text"] = text
		if previous != "" {
			args["previous_synthesis_id"] = previous
		}
		return args
	}
	long := strings.TrimSpace(strings.Repeat("I keep what I learn, and I say where I learned it. ", 150))
	tokens := tokenestimate.Estimate(long)
	if tokens <= store.SelfModelDefaultMaxTokens {
		t.Fatalf("rig: the long portrait is %d tokens, which is not over the default of %d", tokens, store.SelfModelDefaultMaxTokens)
	}

	before := lg.LastSeq()
	_, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_long", "", long))
	if err == nil || lg.LastSeq() != before {
		t.Fatalf("a portrait of %d tokens under the default budget: err %v, ledger %d to %d; it must be refused before anything is appended", tokens, err, before, lg.LastSeq())
	}
	if want := fmt.Sprintf("maximum is %d (prompt.self_model_max_tokens)", store.SelfModelDefaultMaxTokens); !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "bounded narrative") {
		t.Fatalf("the refusal does not name the budget and its setting (%q): %v", want, err)
	}

	// The budget is the most a portrait may be: one token under the
	// portrait refuses it, and the portrait's own size admits it.
	st.SetSelfModelMaxTokens(tokens - 1)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_long", "", long)); err == nil {
		t.Fatalf("a portrait of %d tokens was accepted under a budget of %d", tokens, tokens-1)
	}
	st.SetSelfModelMaxTokens(tokens)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_long", "", long)); err != nil {
		t.Fatalf("with the budget raised to %d the portrait of %d tokens is accepted: %v", tokens, tokens, err)
	}

	// The operator lowers it under the portrait the record now holds.
	st.SetSelfModelMaxTokens(500)
	medium := strings.TrimSpace(strings.Repeat("I am careful with what I claim. ", 60))
	if got := tokenestimate.Estimate(medium); got <= 500 || got > store.SelfModelDefaultMaxTokens {
		t.Fatalf("rig: the medium portrait is %d tokens; it must lie between 500 and the default", got)
	}
	before = lg.LastSeq()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_medium", "syn_long", medium)); err == nil || !strings.Contains(err.Error(), "maximum is 500 (prompt.self_model_max_tokens)") || lg.LastSeq() != before {
		t.Fatalf("a new portrait over the lowered budget must be refused by that number, unappended: %v (ledger %d to %d)", err, before, lg.LastSeq())
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_short", "syn_long", "I am learning to say less, and to mean it.")); err != nil {
		t.Fatalf("a new portrait under the lowered budget is accepted: %v", err)
	}

	// The record replays whole into a store that holds the lowered number.
	rebuilt, err := store.NewForTest(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	rebuilt.SetSelfModelMaxTokens(500)
	if err := rebuilt.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatalf("A PORTRAIT THE RECORD HOLDS WAS REFUSED AT REPLAY BY A BUDGET SET AFTER IT: %v", err)
	}
	current, err := rebuilt.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "syn_short" {
		t.Fatalf("after the replay the current portrait is %+v (%v); want syn_short", current, err)
	}
	all, err := rebuilt.ListSelfModelSyntheses(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	for _, p := range all {
		held = held || (p.ID == "syn_long" && p.SynthesisText == long)
	}
	if !held {
		t.Fatalf("the long portrait is not in the rebuilt record, whole: %d portraits", len(all))
	}

	// Zero is the default, not no budget.
	st.SetSelfModelMaxTokens(0)
	if got := st.SelfModelMaxTokens(); got != store.SelfModelDefaultMaxTokens {
		t.Fatalf("a budget of zero reads %d; it is the default, %d", got, store.SelfModelDefaultMaxTokens)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", portrait("syn_again", "syn_short", long+" Once more.")); err == nil {
		t.Fatal("with the budget back at its default the long portrait is refused again")
	}
}
