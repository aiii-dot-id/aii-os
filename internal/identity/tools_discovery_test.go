package identity

import (
	"context"
	"strings"
	"testing"
)

func TestToolsVerbPointsAtTheSeededDocs(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	for _, depth := range []int{1, 2, 3} {
		res, err := engine.ExecuteAction(context.Background(), "verb", "tools",
			map[string]any{"depth": depth})
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

func TestNoteAdvertisesTheProvenanceAndEdgesItsHandlerReads(t *testing.T) {
	var note *Verb
	for i := range verbRegistry {
		if verbRegistry[i].Name == "note" {
			note = &verbRegistry[i]
		}
	}
	if note == nil {
		t.Fatal("no note verb")
	}
	props, _ := note.Params["properties"].(map[string]any)
	for _, want := range []string{"source_turn", "source_url", "supports", "derived_from", "reinforces", "contradicts", "unmetabolized"} {
		if _, ok := props[want]; !ok {
			t.Errorf("note's schema does not show %q, which its handler reads", want)
		}
	}
}

// homeDocs is a discoverer that also knows what this identity's home holds.
type homeDocs struct {
	discovererAdapter
	docs []string
}

func (h homeDocs) HomeDocs() []string { return h.docs }

// WHAT ONLY THIS IDENTITY'S HOME HOLDS IS SAID WHILE IT IS THERE, AND NOT
// OTHERWISE. A discoverer that names a document has it listed under the
// seeded docs, at every depth; one that names none adds no heading: a
// heading over nothing is machinery the identity learns to read past.
func TestToolsVerbNamesWhatThisHomeHoldsOnlyWhileItIsThere(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	inner := engine.toolDisc.(discovererAdapter)
	line := "other-agents/README.md — notes other AI agents kept on this machine"
	for _, depth := range []int{1, 2, 3} {
		engine.toolDisc = homeDocs{inner, []string{line}}
		res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"depth": depth})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(res, "\nAlso at your root:\n  "+line) || strings.Index(res, "METHOD.md") > strings.Index(res, "Also at your root") {
			t.Fatalf("depth %d: the home's own document is not listed after the seeded docs:\n%s", depth, res)
		}
		for _, d := range []ToolDiscoverer{homeDocs{inner, nil}, inner} {
			engine.toolDisc = d
			res, err = engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"depth": depth})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(res, "Also at your root") {
				t.Fatalf("depth %d: a heading stands over nothing:\n%s", depth, res)
			}
		}
	}
}
