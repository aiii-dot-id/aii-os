package identity

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

func TestTheChartersBudgetIsTheOperatorsAndIsAskedOfANewCharterOnly(t *testing.T) {
	engine, st, lg, _, dir := setupEngine(t)
	ctx := context.Background()
	// Each call records the operator's word for the charter it is about to
	// offer, as the verb asks: a charter is said to them first.
	charter := func(text string, more map[string]any) map[string]any {
		t.Helper()
		if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
			t.Fatal(err)
		}
		args := map[string]any{
			"variant": "relationship.upsert", "id": "rel_budget_01",
			"counterpart_name": "Quinn", "relationship_type": "operator", "charter_text": text,
		}
		for k, v := range more {
			args[k] = v
		}
		return args
	}
	held := func() string {
		t.Helper()
		id, err := st.PromptIdentity()
		if err != nil {
			t.Fatal(err)
		}
		return id.Charter
	}
	long := strings.TrimSpace(strings.Repeat("Quinn rules; I keep the record honest and say where I learned a thing. ", 100))
	tokens := tokenestimate.EstimateWire(long)
	if tokens <= store.CharterDefaultMaxTokens {
		t.Fatalf("rig: the long charter is %d tokens, which is not over the default of %d", tokens, store.CharterDefaultMaxTokens)
	}

	before := lg.LastSeq()
	_, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long, nil))
	var refusal *store.CharterBudgetRefusal
	if !errors.As(err, &refusal) || refusal.Tokens != tokens || refusal.Max != store.CharterDefaultMaxTokens || lg.LastSeq() != before {
		t.Fatalf("a charter of %d tokens under the default budget: err %v, ledger %d to %d; it must be refused by its type, with both numbers, before anything is appended", tokens, err, before, lg.LastSeq())
	}
	if !strings.Contains(refusal.Error(), "(Settings → Technical → Reflection)") {
		t.Fatalf("the refusal names the wrong place for the budget: %s", refusal.Error())
	}
	for _, want := range []string{
		fmt.Sprintf("maximum is %d (prompt.charter_max_tokens)", store.CharterDefaultMaxTokens),
		"say the shorter charter to your operator, and record it once they have affirmed it",
		"ask them to raise the budget",
		"The charter you hold now, sent again unchanged, is admitted",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := held(); got != "" {
		t.Fatalf("the refused charter reached the projection: %d characters of it", len(got))
	}

	// The budget is the most a charter may be: one token under the charter
	// refuses it, and the charter's own size admits it.
	st.SetCharterMaxTokens(tokens - 1)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long, nil)); err == nil {
		t.Fatalf("a charter of %d tokens was recorded under a budget of %d", tokens, tokens-1)
	}
	st.SetCharterMaxTokens(tokens)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long, nil)); err != nil {
		t.Fatalf("with the budget raised to %d the charter of %d tokens is recorded: %v", tokens, tokens, err)
	}
	if held() != long {
		t.Fatal("the recorded charter is not the one written, whole")
	}

	// The operator lowers it under the charter the record now holds. That
	// charter said again is not a new one: the verb asks for the charter
	// with every operator upsert, so an upsert that only moves another
	// field carries it.
	st.SetCharterMaxTokens(500)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long, map[string]any{"trust_level": "established"})); err != nil {
		t.Fatalf("THE RECORDED CHARTER, SAID AGAIN, WAS REFUSED BY A BUDGET SET AFTER IT: %v", err)
	}
	// Nor is it new when a succession carries it to a new id: the same
	// charter, a new operator relationship superseding the old one.
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long, map[string]any{"id": "rel_budget_02", "supersedes": "rel_budget_01"})); err != nil {
		t.Fatalf("THE RECORDED CHARTER, CARRIED TO A SUCCESSOR, WAS REFUSED AS A NEW ONE: %v", err)
	}
	if held() != long {
		t.Fatal("the succession did not carry the charter")
	}
	medium := strings.TrimSpace(strings.Repeat("I say plainly when we disagree. ", 60))
	if got := tokenestimate.EstimateWire(medium); got <= 500 || got > store.CharterDefaultMaxTokens {
		t.Fatalf("rig: the medium charter is %d tokens; it must lie between 500 and the default", got)
	}
	before = lg.LastSeq()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(medium, map[string]any{"id": "rel_budget_02"})); err == nil || !strings.Contains(err.Error(), "maximum is 500 (prompt.charter_max_tokens)") || lg.LastSeq() != before {
		t.Fatalf("a new charter over the lowered budget must be refused by that number, unappended: %v (ledger %d to %d)", err, before, lg.LastSeq())
	}
	if held() != long {
		t.Fatal("a refused charter replaced the recorded one")
	}
	const short = "Quinn rules; I keep the record honest."
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(short, map[string]any{"id": "rel_budget_02"})); err != nil {
		t.Fatalf("a new charter under the lowered budget is recorded: %v", err)
	}
	if held() != short {
		t.Fatal("the shorter charter is not the one the prompt now carries")
	}

	// The record replays whole into a store that holds the lowered number.
	rebuilt, err := store.NewForTest(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	rebuilt.SetCharterMaxTokens(500)
	if err := rebuilt.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatalf("A CHARTER THE RECORD HOLDS WAS REFUSED AT REPLAY BY A BUDGET SET AFTER IT: %v", err)
	}
	if id, err := rebuilt.PromptIdentity(); err != nil || id.Charter != short {
		t.Fatalf("after the replay the charter is %d characters (%v); want the last one recorded", len(id.Charter), err)
	}

	// Zero is the default, not no budget.
	st.SetCharterMaxTokens(0)
	if got := st.CharterMaxTokens(); got != store.CharterDefaultMaxTokens {
		t.Fatalf("a budget of zero reads %d; it is the default, %d", got, store.CharterDefaultMaxTokens)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", charter(long+" Once more.", map[string]any{"id": "rel_budget_02"})); !errors.As(err, &refusal) {
		t.Fatalf("with the budget back at its default a long new charter is refused again: %v", err)
	}

	// A peer's charter is no prompt's Ring 1, and is not held.
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "relationship.upsert", "id": "rel_peer_long_01",
		"counterpart_name": "Rue", "counterpart_role": "peer", "relationship_type": "peer", "charter_text": long,
	}); err != nil {
		t.Fatalf("a peer's long charter was refused by the operator charter's budget: %v", err)
	}
}

// operatorCharter returns the arguments of an operator relationship.upsert
// carrying text as its charter, after recording the operator's word for it
// as the verb asks: a charter is said to them first.
func operatorCharter(t *testing.T, engine *Engine, id, text string, more map[string]any) map[string]any {
	t.Helper()
	if err := engine.RecordConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{
		"variant": "relationship.upsert", "id": id,
		"counterpart_name": "Quinn", "relationship_type": "operator", "charter_text": text,
	}
	for k, v := range more {
		args[k] = v
	}
	return args
}

// THE BUDGET IS COUNTED AS THE PROMPT COUNTS THE CHARTER
// (tokenestimate.EstimateWire): a charter of the budget costs the budget
// in a request. A JSON string carries a line break as two bytes, so a
// charter of many short lines costs more there than its plain bytes say.
// Here the budget is the charter's plain estimate: the charter is refused,
// by its cost in the request, and admitted at that cost.
func TestTheChartersBudgetCountsTheCharterAsThePromptDoes(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", operatorCharter(t, engine, "rel_wire_0001", "Quinn rules.", nil)); err != nil {
		t.Fatalf("rig: the founding charter: %v", err)
	}
	text := strings.Repeat("I keep my word.\n", 400) + "Always."
	plain, wire := tokenestimate.Estimate(text), tokenestimate.EstimateWire(text)
	if wire <= plain {
		t.Fatalf("rig: the charter costs %d tokens in a request and %d as plain bytes; it must cost more in the request", wire, plain)
	}
	st.SetCharterMaxTokens(plain)
	_, err := engine.ExecuteAction(ctx, "verb", "commit", operatorCharter(t, engine, "rel_wire_0001", text, nil))
	var refusal *store.CharterBudgetRefusal
	if !errors.As(err, &refusal) || refusal.Tokens != wire || refusal.Max != plain {
		t.Fatalf("a charter of %d tokens in a request under a budget of %d: %v; it must be refused by its cost in the request", wire, plain, err)
	}
	st.SetCharterMaxTokens(wire)
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", operatorCharter(t, engine, "rel_wire_0001", text, nil)); err != nil {
		t.Fatalf("at a budget of its cost in the request the charter is recorded: %v", err)
	}
}

// AN UPSERT THE RELATIONSHIP'S OWN RULES REFUSE IS REFUSED IN THEIR WORDS.
// The budget is asked last of the relationship's rules: an identity sent
// back to shorten a charter that would then be refused for another reason
// is sent back for the wrong one. Here a second operator relationship that
// names no predecessor carries a charter over the budget, and the refusal
// is the fork of Ring 1, not the budget.
func TestTheChartersBudgetYieldsToTheRelationshipsOwnRules(t *testing.T) {
	engine, _, lg, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", operatorCharter(t, engine, "rel_rules_01", "Quinn rules.", nil)); err != nil {
		t.Fatalf("rig: the founding charter: %v", err)
	}
	long := strings.TrimSpace(strings.Repeat("Quinn rules; I keep the record honest and say where I learned a thing. ", 100))
	if tokenestimate.EstimateWire(long) <= store.CharterDefaultMaxTokens {
		t.Fatal("rig: the long charter is not over the default budget")
	}
	before := lg.LastSeq()
	_, err := engine.ExecuteAction(ctx, "verb", "commit", operatorCharter(t, engine, "rel_rules_02", long, nil))
	var refusal *store.CharterBudgetRefusal
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "must supersede the current one") || lg.LastSeq() != before {
		t.Fatalf("a second operator relationship naming no predecessor, its charter over the budget: %v (ledger %d to %d); it must be refused by the relationship's rule, unappended", err, before, lg.LastSeq())
	}
}
