package identity

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// readingEmbedder is currencyEmbedder reading each text to its first
// window characters, and saying so.
type readingEmbedder struct {
	currencyEmbedder
	window int
}

func (readingEmbedder) Tells() bool { return true }

func (e readingEmbedder) EmbedRead(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, []int, error) {
	heads, read := make([]string, len(inputs)), make([]int, len(inputs))
	for i, in := range inputs {
		r := []rune(in)
		read[i] = min(len(r), e.window)
		heads[i] = string(r[:read[i]])
	}
	basis, vectors, err := e.currencyEmbedder.Embed(ctx, kind, heads)
	return basis, vectors, read, err
}

func (e readingEmbedder) Embed(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, error) {
	basis, vectors, _, err := e.EmbedRead(ctx, kind, inputs)
	return basis, vectors, err
}

// A HIT MATCHED ON ITS BEGINNING ALONE SAYS SO, and how much, with where
// the rest is found; a hit read whole says nothing more than it did; a cue
// read in part is said in the meaning layer's line; and the measure read
// counts the vector made from a beginning.
func TestAHitMatchedOnItsBeginningAloneSaysHowMuchMeaningRead(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(readingEmbedder{window: 40})
	long := "the ferry waited at the pier all night, " + strings.Repeat("and the keeper kept the log of the weather, ", 3) + "and the lantern stayed lit."
	mustVerb(t, e, "note", map[string]any{"content": long})
	mustVerb(t, e, "note", map[string]any{"content": "a ferry crossed at dawn"})
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, e, map[string]any{"query": "boat"})
	exp := sectionOf(out, "Experiences:")
	n := len([]rune(long))
	want := ", meaning read the first 40 of its " + strconv.Itoa(n) + " characters (what lies past them is found by words alone), score "
	var longLine, shortLine string
	for _, l := range strings.Split(exp, "\n") {
		switch {
		case strings.Contains(l, "the ferry waited"):
			longLine = l
		case strings.Contains(l, "a ferry crossed"):
			shortLine = l
		}
	}
	if !strings.Contains(longLine, want) {
		t.Fatalf("the long note, matched by meaning on its first 40 characters, must say so:\n%s", out)
	}
	if shortLine == "" || strings.Contains(shortLine, "meaning read") {
		t.Fatalf("a note read whole says nothing of how much was read:\n%s", out)
	}
	// "lamp" means the lantern, which lies past what was read: the long
	// note is not offered by meaning for it.
	if lamp := recallOut(t, e, map[string]any{"query": "lamp"}); strings.Contains(lamp, "the ferry waited") {
		t.Fatalf("what lies past what the source read was matched by meaning:\n%s", lamp)
	}
	cue := "boat " + strings.Repeat("and then the night went on ", 2)
	cued := recallOut(t, e, map[string]any{"query": cue})
	if !strings.Contains(cued, "Of the cue, meaning read the first 40 of its "+strconv.Itoa(len([]rune(strings.TrimSpace(cue))))+" characters; the rest of it was matched by words alone.") {
		t.Fatalf("a cue read in part must be said in the meaning layer's line:\n%s", cued)
	}
	if strings.Contains(out, "Of the cue") {
		t.Fatalf("a cue read whole says nothing of it:\n%s", out)
	}
	measure, err := e.verbMeasure(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(measure, "1 of the vectors held was made from the beginning of their text alone, as the source said; a recalled memory among them says how much, and what lies past it is found by its words") {
		t.Fatalf("the measure read must count the vector made from a beginning:\n%s", measure)
	}
}

func TestAHitFromASourceThatDoesNotSayHowMuchItReadSaysWhatWasHanded(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	long := strings.TrimSpace(strings.Repeat("the ferry waited. ", 500))
	mustVerb(t, e, "note", map[string]any{"content": long})
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, e, map[string]any{"query": "boat"})
	if want := ", meaning was handed the first 8000 of its " + strconv.Itoa(len([]rune(long))) + " characters (what lies past them is found by words alone), score "; !strings.Contains(out, want) {
		t.Fatalf("a memory past what a source is handed must say how much was handed:\n%s", out)
	}
}
