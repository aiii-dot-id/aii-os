package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestARecalledNoteNamesTheRecordRowThatMadeIt(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	ctx := context.Background()
	before := lg.LastSeq()
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": "the kingfisher struck the water at dawn"}); err != nil {
		t.Fatal(err)
	}
	row := lg.LastSeq()
	if row != before+1 {
		t.Fatalf("fixture: a note wrote %d rows of the record; want one", row-before)
	}
	id, err := st.FindExperienceByContent("the kingfisher struck the water at dawn")
	if err != nil || id == "" {
		t.Fatalf("fixture: the note is not in the store: %q %v", id, err)
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "kingfisher"})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("  [seq %d, %s, ", row, id)
	for _, line := range strings.Split(sectionOf(out, "Experiences:"), "\n") {
		if strings.HasPrefix(line, want) {
			return
		}
	}
	t.Fatalf("no hit begins %q, the note's row of the record:\n%s", want, out)
}
