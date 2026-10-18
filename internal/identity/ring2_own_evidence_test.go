package identity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTheResidentPromotesOnTheirOwnEvidence(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "belief.upsert", "id": "b_mine", "statement": "I work best from a written plan",
		"confidence": 0.6, "evidence_refs": "exp_test_origin",
	}); err != nil {
		t.Fatalf("belief.upsert: %v", err)
	}
	for _, content := range []string{"the planned change landed whole", "the unplanned one took three tries"} {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": content, "supports": "b_mine"}); err != nil {
			t.Fatalf("note: %v", err)
		}
	}
	if opTurn, err := st.GetLatestOperatorTurn(); err != nil || opTurn != nil {
		t.Fatalf("rig: this record must hold no operator turn: %v %v", opTurn, err)
	}
	if got := mustStanding(t, st, "b_mine"); got != "confirmed" {
		t.Fatalf("three of their own sources: %q, want confirmed", got)
	}

	out, err := callAdvertisedRecall(t, engine, ctx, map[string]any{"source": "standing", "id": "b_mine", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "eligible true") || !strings.Contains(out, "Ring 2 requirements met") {
		t.Fatalf("the standing read does not say the gate is met:\n%s", out)
	}
	for _, gone := range []string{"Authorship", "authorship", "independent voice"} {
		if strings.Contains(out, gone) {
			t.Fatalf("the standing read still speaks of %q:\n%s", gone, out)
		}
	}

	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "belief.promote", "id": "b_mine", "ring": 2}); err != nil {
		t.Fatalf("their own evidence, their own act, and it was refused: %v", err)
	}
	b, err := st.GetBelief("b_mine")
	if err != nil || b == nil || b.Ring != 2 {
		t.Fatalf("the belief after its promotion: %+v %v", b, err)
	}
}

// NO TOOL TEXT TEACHES IT. What the resident reads of their tools named
// the operator's recorded words "the independent voice the belief ladder
// counts", and so they went and collected them. Every verb's description
// and every argument's is read here.
func TestNoToolTextSendsTheResidentAfterAnotherVoice(t *testing.T) {
	var all strings.Builder
	for _, v := range verbRegistry {
		params, err := json.Marshal(v.Params)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		all.WriteString(v.Description + "\n" + string(params) + "\n")
	}
	text := strings.ToLower(all.String())
	if !strings.Contains(text, "source_turn") {
		t.Fatal("rig: the tool surface was not read (the note tool's source_turn is not in it)")
	}
	for _, gone := range []string{"independent voice", "authorship class", "belief ladder counts"} {
		if strings.Contains(text, gone) {
			t.Errorf("a tool's text still speaks of %q", gone)
		}
	}
}

// THE ACT IS DESCRIBED WHERE ACTS ARE LOOKED UP. belief.promote was a bare
// name among the commit verb's variants and its requirement was read only
// in a refusal. The verb's description carries the one sentence, and the
// sentence says what the gate asks: three distinct sources, no
// contradiction, and where the standing is read.
func TestTheCommitVerbSaysWhatPromotionDoesAndAsks(t *testing.T) {
	var commit string
	for _, v := range verbRegistry {
		if v.Name == "commit" {
			commit = v.Description
		}
	}
	if !strings.Contains(commit, BeliefPromotionRule) {
		t.Fatalf("the commit verb's description does not carry the promotion rule: %q", commit)
	}
	for _, said := range []string{"belief.promote", "Ring 2", "three distinct", "no contradiction", "source=standing"} {
		if !strings.Contains(BeliefPromotionRule, said) {
			t.Errorf("the promotion rule does not say %q: %q", said, BeliefPromotionRule)
		}
	}
	// It describes; it does not direct. An instruction to go and adopt, or
	// to go and make evidence, is what the prompt stopped saying.
	for _, directive := range []string{"you should", "you must", "remember to", "gather"} {
		if strings.Contains(strings.ToLower(BeliefPromotionRule), directive) {
			t.Errorf("the promotion rule directs the resident (%q): %q", directive, BeliefPromotionRule)
		}
	}
}
