package memory

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// tellingSource reads each text to its first window characters, makes the
// vector of what it read, and says so; tells false makes it the same
// package at @1, which says nothing.
type tellingSource struct {
	fakeEmbedder
	window int
	tells  bool
	// over makes it say it read more than it was handed.
	over bool
}

func (s *tellingSource) Tells() bool { return s.tells }

func (s *tellingSource) EmbedRead(ctx context.Context, kind Kind, inputs []string) (string, [][]float32, []int, error) {
	read := make([]int, len(inputs))
	heads := make([]string, len(inputs))
	for i, in := range inputs {
		r := []rune(in)
		read[i] = min(len(r), s.window)
		heads[i] = string(r[:read[i]])
		if s.over {
			read[i] = len(r) + 1
		}
	}
	basis, vectors, err := s.fakeEmbedder.Embed(ctx, kind, heads)
	return basis, vectors, read, err
}

func (s *tellingSource) Embed(ctx context.Context, kind Kind, inputs []string) (string, [][]float32, error) {
	basis, vectors, _, err := s.EmbedRead(ctx, kind, inputs)
	return basis, vectors, err
}

// The record of meaningStore, with one long experience whose harbor is in
// its first forty characters and whose ship is past them.
const longExperience = "the harbor master wrote it all down, and " + "then, after the long evening, of the ship"

func tellingStore(t *testing.T, tells bool) (*Facility, *tellingSource) {
	t.Helper()
	f, _, now := meaningStore(t)
	seedLedger(t, f.st, 4, now.Add(-48*time.Hour))
	addExperience(t, f.st, "e3", longExperience, 4, now.Add(-time.Hour), false)
	src := &tellingSource{fakeEmbedder: fakeEmbedder{basis: "fake/v1"}, window: 40, tells: tells}
	f.SetEmbedder(src)
	return f, src
}

func hitOf(t *testing.T, res Result, id string) Hit {
	t.Helper()
	for _, h := range res.Hits {
		if h.ID == id {
			return h
		}
	}
	t.Fatalf("no hit %s among %+v", id, res.Hits)
	return Hit{}
}

// WHAT A SOURCE SAYS IT READ REACHES THE HIT AND THE CUE. The long
// experience's vector was made from its first forty characters: found by
// meaning for what is in them, its hit says forty of its length, and a cue
// about what lies past them finds it by its words alone. A short text is
// read whole. A cue past the window says how much of it was read.
func TestWhatTheSourceSaysItReadReachesTheHitAndTheCue(t *testing.T) {
	f, _ := tellingStore(t, true)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 4 {
		t.Fatalf("backfill: %+v %v", rep, err)
	}
	res, err := f.Recall(ctx, Query{Text: "port", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil {
		t.Fatal(err)
	}
	long, short := hitOf(t, res, "e3"), hitOf(t, res, "e1")
	if long.Match != MatchMeaning || long.MeaningRead != 40 {
		t.Fatalf("the long experience, found by meaning, must say its vector was made from its first 40 characters: %+v", long)
	}
	if n := utf8.RuneCountInString(short.Text); short.MeaningRead != n {
		t.Fatalf("a short experience is read whole: %d of %d", short.MeaningRead, n)
	}
	if res.Meaning.CueChars != 4 || res.Meaning.CueRead != 4 {
		t.Fatalf("a cue inside the window is read whole: %+v", res.Meaning)
	}
	// "vessel" is the ship's meaning, which lies past what was read: the
	// long experience is not offered by meaning for it.
	res, err = f.Recall(ctx, Query{Text: "vessel", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.ID == "e3" {
			t.Fatalf("what lies past what the source read was matched by meaning: %+v", h)
		}
	}
	cue := strings.TrimSpace(strings.Repeat("port ", 13))
	res, err = f.Recall(ctx, Query{Text: cue, Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil || res.Meaning.CueChars != 64 || res.Meaning.CueRead != 40 {
		t.Fatalf("a cue of 64 characters read to its first 40 must say so: %+v %v", res.Meaning, err)
	}

	// The measure read counts the one vector made from a beginning alone.
	basis, cov, unavailable, err := f.UsableCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var partial int64
	for _, c := range cov {
		partial += c.Partial
		if c.Untold != 0 {
			t.Fatalf("every vector says how much it read: %+v", c)
		}
	}
	line := RenderCoverage(basis, cov, unavailable)
	if partial != 1 || !strings.Contains(line, "1 of the vectors held was made from the beginning of their text alone, as the source said") {
		t.Fatalf("the measure read must count the one vector made from a beginning: %d\n%s", partial, line)
	}
	_, held, _, err := f.HeldCoverage(ctx)
	partial = 0
	for _, c := range held {
		partial += c.Partial
	}
	if err != nil || partial != 1 {
		t.Fatalf("the card's count, by the row's id, must count it too: %+v %v", held, err)
	}
}

// A VECTOR THAT DOES NOT SAY HOW MUCH IT READ IS ASKED AGAIN of a source
// that says it, and is used until then. The same package at @1 made every
// vector under the same basis; at @2 the next pass makes each again, and
// the measure read says how many wait for it. A source that does not say
// it is asked for nothing more.
func TestAVectorMadeBeforeTheSourceSaidHowMuchItReadIsMadeAgain(t *testing.T) {
	f, src := tellingStore(t, false)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 4 {
		t.Fatalf("backfill at @1: %+v %v", rep, err)
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 0 {
		t.Fatalf("a source that does not say how much it read is asked nothing more: %+v %v", rep, err)
	}
	res, err := f.Recall(ctx, Query{Text: "port", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil || hitOf(t, res, "e3").MeaningRead != 0 || res.Meaning.CueRead != 0 {
		t.Fatalf("a source that does not say how much it read says nothing of it: %+v %v", res, err)
	}

	src.tells = true
	basis, cov, unavailable, err := f.UsableCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if line := RenderCoverage(basis, cov, unavailable); !strings.Contains(line, "4 were made before the source said how much of a text it read, and the backfill makes them again") {
		t.Fatalf("the measure read must say how many vectors do not say how much they read:\n%s", line)
	}
	// Still read meanwhile: nothing was deleted.
	res, err = f.Recall(ctx, Query{Text: "port", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil || hitOf(t, res, "e3").Match != MatchMeaning {
		t.Fatalf("a vector that does not say how much it read is still read until it is made again: %v", err)
	}
	rep, err := f.Backfill(ctx, 0)
	if err != nil || rep.Embedded != 4 {
		t.Fatalf("the pass under a source that says how much it read makes every vector again: %+v %v", rep, err)
	}
	res, err = f.Recall(ctx, Query{Text: "port", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil || hitOf(t, res, "e3").MeaningRead != 40 {
		t.Fatalf("made again, the long experience says how much of it was read: %+v %v", res, err)
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 0 {
		t.Fatalf("once every vector says it, nothing is asked again: %+v %v", rep, err)
	}
}

func TestASourceThatSaysItReadMoreThanItWasHandedIsRefused(t *testing.T) {
	f, src := tellingStore(t, true)
	src.over = true
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err == nil || rep.Embedded != 0 {
		t.Fatalf("a pass whose source said it read more than its text must keep nothing: %+v %v", rep, err)
	}
	res, err := f.Recall(ctx, Query{Text: "port", Stores: []string{"experiences"}, Decay: DecayNone})
	if err != nil || res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, "the source said it read 5 characters of a text of 4") {
		t.Fatalf("the cue's layer must be unavailable, saying why: %+v %v", res.Meaning, err)
	}
}
