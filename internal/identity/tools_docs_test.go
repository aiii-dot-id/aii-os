package identity

import (
	"context"
	"strings"
	"testing"
)

// offers is a discoverer that offers exactly these tools.
type offers []string

func (o offers) Discover(int) []ToolInfo {
	out := make([]ToolInfo, 0, len(o))
	for _, name := range o {
		out = append(out, ToolInfo{Name: name})
	}
	return out
}

// "ANNOTATE" IS SAID ONLY WHERE THE IDENTITY CAN. The tools verb ends by
// pointing at SKILLS.md and METHOD.md as "yours to read and annotate; your
// edits win". On a phone the identity is offered no write, edit or shell
// tool, and an operator may switch the three off anywhere: the pointer then
// promised an act no tool of theirs performs. With any one of the three it
// says what it always said, to the byte.
func TestTheToolsVerbSaysAnnotateOnlyWhereTheIdentityCanWriteAFile(t *testing.T) {
	const always = "\nSeeded docs at your root (yours to read and annotate; your edits win):\n  SKILLS.md"
	for _, tc := range []struct {
		name     string
		offered  ToolDiscoverer
		annotate bool
	}{
		{"a desktop's tools", offers{"read", "write", "edit", "shell", "grep", "ls"}, true},
		{"the shell alone", offers{"read", "shell"}, true},
		{"edit alone", offers{"read", "edit"}, true},
		{"a phone's tools", offers{"read", "grep", "ls", "web_fetch"}, false},
		{"no tools at all", offers{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, _, _, _, _ := setupEngine(t)
			engine.toolDisc = tc.offered
			res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(res, "SKILLS.md — ") || !strings.Contains(res, "METHOD.md — ") {
				t.Fatalf("the two documents are not pointed at:\n%s", res)
			}
			if got := strings.Contains(res, always); got != tc.annotate {
				t.Fatalf("says the documents are theirs to annotate = %v, want %v:\n%s", got, tc.annotate, res)
			}
			if !tc.annotate && (strings.Contains(res, "annotate") || !strings.Contains(res, "\nSeeded docs at your root (yours to read):\n  SKILLS.md")) {
				t.Fatalf("AN IDENTITY WITH NO TOOL THAT WRITES A FILE IS TOLD THE DOCUMENTS ARE THEIRS TO ANNOTATE:\n%s", res)
			}
		})
	}
}

// NOR IN SAFE. SAFE does not shorten the offered list: write, edit and the
// shell stay listed and are refused when called, nothing being written
// while SAFE holds. An identity in SAFE on a desktop read "annotate".
func TestTheToolsVerbDoesNotSayAnnotateInSafe(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.toolDisc = offers{"read", "write", "edit", "shell", "grep", "ls"}
	engine.SetSafeMode("the record did not pass its checks")
	res, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "annotate") || !strings.Contains(res, "\nSeeded docs at your root (yours to read):\n  SKILLS.md") {
		t.Fatalf("AN IDENTITY IN SAFE IS TOLD THE DOCUMENTS ARE THEIRS TO ANNOTATE, where every write is refused:\n%s", res)
	}
}
