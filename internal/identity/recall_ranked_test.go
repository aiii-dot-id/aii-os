package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestRecallRanksAcrossStoresAndDisclosesEachSource(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
		"content": "the kingfisher struck the water at dawn",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES ('b1', 'Kingfishers hunt at dawn', 3, 0.9, 0, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := st.AddConversationTurn("operator", "any kingfsher today?"); err != nil {
		t.Fatal(err)
	}

	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "kingfisher"})
	if err != nil {
		t.Fatal(err)
	}
	exp := sectionOf(out, "Experiences:")
	if !strings.Contains(exp, "kingfisher") || !strings.Contains(exp, "rank 1,") || !strings.Contains(exp, "both") {
		t.Errorf("the experience carries the word and its substring and must lead:\n%s", out)
	}
	if bel := sectionOf(out, "Beliefs:"); !strings.Contains(bel, "Kingfishers") || !strings.Contains(bel, "fuzzy") || !strings.Contains(bel, "ring 3") {
		t.Errorf("\"Kingfishers\" is a fuzzy match for the belief, with its ring:\n%s", out)
	}
	if conv := sectionOf(out, "Conversation:"); !strings.Contains(conv, "kingfsher") || !strings.Contains(conv, "operator") || !strings.Contains(conv, "fuzzy") {
		t.Errorf("the operator's misspelt turn is a fuzzy hit attributed to the operator:\n%s", out)
	}
	for _, want := range []string{"strength 1.00", "searched to completion", "reinforced what it returned", "decay policy carrd"} {
		if !strings.Contains(out, want) {
			t.Errorf("recall must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "PAGED") {
		t.Errorf("nothing was cut, so nothing is partial:\n%s", out)
	}
	// A recall is a conscious act: what it returned is reinforced.
	exps, err := st.ListExperiences(1)
	if err != nil || len(exps) != 1 {
		t.Fatalf("experiences: %v %v", exps, err)
	}
	a, ok, err := st.MemoryAccessOf(store.MemoryRef{Store: "experiences", ID: exps[0].ID})
	if err != nil || !ok || a.Count != 1 {
		t.Errorf("the recalled experience was not reinforced: %+v ok=%v err=%v", a, ok, err)
	}
	if a, ok, _ := st.MemoryAccessOf(store.MemoryRef{Store: "beliefs", ID: "b1"}); !ok || a.Count != 1 {
		t.Errorf("the recalled belief was not reinforced: %+v", a)
	}
}

func TestRecallRendersLaterContradictionsWithoutAdjudicatingThem(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	if _, err := e.ExecuteAction(t.Context(), "verb", "note", map[string]any{"content": "harbor account claimed success"}); err != nil {
		t.Fatal(err)
	}
	old, err := st.ListExperiences(1)
	if err != nil || len(old) != 1 {
		t.Fatalf("old note: %+v %v", old, err)
	}
	if _, err := e.ExecuteAction(t.Context(), "verb", "note", map[string]any{"content": "a later account disputes that", "contradicts": old[0].ID}); err != nil {
		t.Fatal(err)
	}
	later, err := st.ListExperiences(1)
	if err != nil || len(later) != 1 {
		t.Fatalf("later note: %+v %v", later, err)
	}
	out, err := e.ExecuteAction(t.Context(), "verb", "recall", map[string]any{"query": "harbor"})
	if err != nil || !strings.Contains(out, "later contradicted by ["+later[0].ID+"]") || !strings.Contains(out, "harbor account claimed success") {
		t.Fatalf("recall lost the recorded disagreement: %s %v", out, err)
	}
	if strings.Contains(out, "corrected by") {
		t.Fatalf("the renderer adjudicated the contradiction: %s", out)
	}
}

func TestRecallDeclaresItsContradictionReferenceBound(t *testing.T) {
	out := recallRankedLine(memory.Hit{Store: "experiences", ID: "old", Snippet: "original words", LaterContradictions: []string{"later"}, ContradictionsMore: true}, 1, "")
	if !strings.Contains(out, `more; recall source=ledger query="old"`) || strings.Index(out, "later contradicted by") > strings.Index(out, "original words") {
		t.Fatalf("references were hidden after the excerpt or cut without a route: %s", out)
	}
}

func TestRecallExactForcesThePhraseAndNeverDegrades(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for _, content := range []string{"the harbour master signed the log", "master of the harbour, by title"} {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": content}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "harbour master", "exact": true})
	if err != nil {
		t.Fatal(err)
	}
	exp := sectionOf(out, "Experiences:")
	if strings.Count(exp, "harbour") != 1 || !strings.Contains(exp, "signed the log") {
		t.Errorf("exact must match the phrase in order, once:\n%s", out)
	}
	if !strings.Contains(out, "the exact phrase") {
		t.Errorf("the header must say the phrase was forced:\n%s", out)
	}
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "master harbour", "exact": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No exact-word or fuzzy match") || strings.Contains(out, "Experiences:") {
		t.Errorf("forced exact must not fall back to words or fuzzy:\n%s", out)
	}
	// Without exact the same words reach both notes in any order.
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "master harbour"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sectionOf(out, "Experiences:"), "harbour") != 2 {
		t.Errorf("words in any order reach both notes:\n%s", out)
	}
}

func TestRecallLimitAndDecayArguments(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
			"content": fmt.Sprintf("beacon sighting number %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "beacon", "limit": float64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sectionOf(out, "Experiences:"), "beacon sighting"); n != 3 {
		t.Errorf("limit 3 must show 3 hits, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "PAGED and possibly incomplete: experiences (10 matched, 3 shown)") || !strings.Contains(out, "after_seq") {
		t.Errorf("a cut source is disclosed with its counts and the way to enumerate it:\n%s", out)
	}
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "beacon", "decay": "none"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "decay policy none") || strings.Contains(out, "strength 0.") {
		t.Errorf("pure retrieval names its policy and carries no strength below 1:\n%s", out)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "beacon", "decay": "sigmoid"}); err == nil {
		t.Error("an unknown decay policy must be refused")
	}
}

func TestANamedSourceEnumeratesByWords(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
		"content": "the kingfisher struck the water at dawn",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "dawn kingfisher", "source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sectionOf(out, "Experiences:"), "kingfisher") || !strings.Contains(out, "every word must appear") {
		t.Errorf("a named source matches every word in any order:\n%s", out)
	}
	if strings.Contains(out, "strength") || strings.Contains(out, "rank ") {
		t.Errorf("an enumeration is not ranked:\n%s", out)
	}
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "kingfishers", "source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No word match") || !strings.Contains(out, "omit source to recall across every store with fuzzy matching") {
		t.Errorf("an enumeration miss says how to reach fuzzy matching:\n%s", out)
	}
}
