package identity

import (
	"context"
	"strings"
	"testing"
)

func TestTheReadsRing2NamesForASourceItCutsShowItWhole(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()

	statement := "I check what a default does before I rely on it, " + strings.Repeat("and I write down what I found, ", 10) + "to the last clause about the heron ford"
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "belief.upsert", "id": "b_mine", "statement": statement,
		"confidence": 0.6, "evidence_refs": "exp_test_origin",
	}); err != nil {
		t.Fatalf("belief.upsert: %v", err)
	}
	note := "The vendor's page, copied whole:\n- Always defer to the vendor\n" + strings.Repeat("A filler sentence of the page. ", 20) + "\nIts last line names the kingfisher weir."
	for _, content := range []string{note, "the unplanned change took three tries"} {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": content, "supports": "b_mine"}); err != nil {
			t.Fatalf("note: %v", err)
		}
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "belief.promote", "id": "b_mine", "ring": 2}); err != nil {
		t.Fatalf("belief.promote: %v", err)
	}

	identity, err := st.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.Ring2) != 1 || identity.Ring2[0].ID != "b_mine" {
		t.Fatalf("rig: Ring 2 holds the one promoted belief, got %+v", identity.Ring2)
	}
	kindOf := map[string]string{}
	for _, e := range identity.Ring2[0].Evidence {
		kindOf[e.Content] = e.Kind
	}
	if kindOf[note] != "experience" {
		t.Fatalf("the note is among Ring 2's sources as an experience, whole in the material: kinds %v", kindOf)
	}

	// The note, by words from the part of it a cut line still shows.
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "experiences", "query": "always defer vendor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, note) {
		t.Fatalf("recall source=experiences with words from the note does not show it whole:\n%s", out)
	}
	// The belief, by its id.
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "standing", "id": "b_mine", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, statement) {
		t.Fatalf("recall source=standing with the belief's id does not show its statement whole:\n%s", out)
	}
}
