package app

import (
	"testing"
)

func TestAdapterCarriesAttributes(t *testing.T) {
	a, _ := focusFixture(t)
	port := projectsAdapter{a}

	created, err := port.Create("board-room", "the pm-board co-build",
		nil, nil, map[string]any{"board": []any{"t1", "t2"}})
	if err != nil {
		t.Fatalf("create with attributes: %v", err)
	}
	if created.Attributes == nil || created.Attributes["board"] == nil {
		t.Fatalf("info() dropped the envelope on create: %#v", created.Attributes)
	}

	// Replace-whole: updating with a new envelope replaces it.
	if _, err := port.Update(created.ID, "", "", "", nil, nil, map[string]any{"board": []any{"t3"}}); err != nil {
		t.Fatalf("update with attributes: %v", err)
	}
	p, err := a.projects.Load(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Attributes == nil || len(p.Attributes) != 1 {
		t.Fatalf("manifest envelope wrong after update: %#v", p.Attributes)
	}
	board, ok := p.Attributes["board"].([]any)
	if !ok || len(board) != 1 || board[0] != "t3" {
		t.Fatalf("replace-whole rule violated: %#v", p.Attributes["board"])
	}

	// Untouched: an update without attributes must leave the envelope
	// alone (nil = no write in ApplyPatch).
	if _, err := port.Update(created.ID, "renamed", "", "", nil, nil, nil); err != nil {
		t.Fatalf("update without attributes: %v", err)
	}
	p2, err := a.projects.Load(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Attributes == nil || p2.Attributes["board"] == nil {
		t.Fatalf("nil attributes must mean untouched; envelope lost: %#v", p2.Attributes)
	}
	if p2.Name != "renamed" {
		t.Fatalf("name update lost: %q", p2.Name)
	}
}

// The list read-back carries the envelope too — both hands see the
// same truth.
func TestAdapterListCarriesAttributes(t *testing.T) {
	a, _ := focusFixture(t)
	port := projectsAdapter{a}
	if _, err := port.Create("attrs", "", nil, nil, map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	ps, err := port.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name == "attrs" && (p.Attributes == nil || p.Attributes["k"] != "v") {
			t.Fatalf("List dropped the envelope: %#v", p.Attributes)
		}
	}
}
