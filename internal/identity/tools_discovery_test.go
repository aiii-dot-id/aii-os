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
// .
func TestToolsVerbPointsAtTheSeededDocs(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	for _, depth := range []int{1, 2, 3} {
		res, err := engine.ExecuteAction(context.Background(), "verb", "tools",
			map[string]interface{}{"depth": depth})
		if err != nil {
			t.Fatal(err)
		}
		for _, doc := range []string{"SKILLS.md", "METHOD.md"} {
			if !strings.Contains(res, doc) {
				t.Fatalf("depth %d: the discovery organ does not name %s", depth, doc)
			}
		}
		if !strings.Contains(res, "your edits win") {
			t.Fatalf("depth %d: the pointer does not say the docs are theirs", depth)
		}
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestNoteAdvertisesTheProvenanceAndEdgesItsHandlerReads(t *testing.T) {
	var note *Verb
	for i := range VerbRegistry {
		if VerbRegistry[i].Name == "note" {
			note = &VerbRegistry[i]
		}
	}
	if note == nil {
		t.Fatal("no note verb")
	}
	props, _ := note.Params["properties"].(map[string]interface{})
	for _, want := range []string{"source_turn", "source_url", "supports", "derived_from", "reinforces", "contradicts", "private"} {
		if _, ok := props[want]; !ok {
			t.Errorf("note's schema does not show %q, which its handler reads", want)
		}
	}
}
