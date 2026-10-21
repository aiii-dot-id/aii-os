package memory

import (
	"context"
	"slices"
	"testing"
	"time"
)

// within_test.go — a short word of a script written without spaces is
// found by its words. The word
// index keeps a run of Chinese or Japanese as one token and the trigram
// index needs three characters, so a word of two, the commonest length
// there, was found by neither: not in a recall, not in a listing by words.

func withinStore(t *testing.T) (*Facility, time.Time) {
	t.Helper()
	s := newStore(t)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 5, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "明日は会議があります", 1, now.Add(-4*time.Hour), false)
	addExperience(t, s, "e2", "会議の議事録を書いた。", 2, now.Add(-3*time.Hour), false)
	addExperience(t, s, "e3", "a quiet day of routine observations", 3, now.Add(-2*time.Hour), false)
	addExperience(t, s, "e4", "会議のあとは quiet な午後", 4, now.Add(-time.Hour), false)
	addBelief(t, s, "b1", "週次の会議は月曜日に行う", 3, 5)
	return New(s), now
}

func sortedIDs(hits []Hit) []string {
	ids := hitIDs(hits)
	slices.Sort(ids)
	return ids
}

// A WORD OF TWO CHARACTERS INSIDE A RUN IS FOUND, AND MARKED AS WHAT IT IS.
// Four memories hold the word for a meeting, each inside a longer run of
// its script. A recall of the word finds all four with the mark
// exact_words, since every word of the cue is in each text; forced exact
// finds the same four, the phrase being that one word; and for a cue of
// that word and an English one, the words layer finds the one memory that
// holds both.
func TestATwoCharacterWordInAScriptWithoutSpacesIsFoundByItsWords(t *testing.T) {
	f, now := withinStore(t)
	ctx := context.Background()
	want := []string{"beliefs/b1", "experiences/e1", "experiences/e2", "experiences/e4"}
	for _, exact := range []bool{false, true} {
		res, err := f.Recall(ctx, Query{Text: "会議", Exact: exact, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedIDs(res.Hits); !slices.Equal(got, want) {
			t.Fatalf("exact=%v: A TWO-CHARACTER WORD INSIDE A RUN WAS FOUND BY NEITHER WORD LAYER: %v, want %v", exact, got, want)
		}
		for _, h := range res.Hits {
			if h.Match != MatchExactWords {
				t.Fatalf("exact=%v: every word of the cue is in the text, and the hit must say so: %+v", exact, h)
			}
		}
		for _, src := range res.Sources {
			if src.Status != StatusFound && src.Status != StatusFoundNothing {
				t.Fatalf("exact=%v: %s answered %s (%s)", exact, src.Store, src.Status, src.Detail)
			}
		}
		if src := sourceOf(res, "experiences"); src.Matched != 3 || src.Shown != 3 {
			t.Fatalf("exact=%v: experiences: %+v, want three matched and shown", exact, src)
		}
	}

	res, err := f.Recall(ctx, Query{Text: "会議 quiet", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	// The fuzzy layer may offer the memory that holds only the English
	// word, as it does for any cue; the words layer offers one.
	var byWords []string
	for _, h := range res.Hits {
		if slices.Contains(h.Layers, MatchExactWords) {
			byWords = append(byWords, h.Store+"/"+h.ID)
		}
	}
	if !slices.Equal(byWords, []string{"experiences/e4"}) {
		t.Fatalf("every word must appear, the one read inside its run and the other by the index: %v of %v", byWords, hitIDs(res.Hits))
	}
}

// A listing by words finds it too, newest first and paged as any other.
func TestAListingByWordsFindsATwoCharacterWordInsideARun(t *testing.T) {
	f, _ := withinStore(t)
	ctx := context.Background()
	page, total, err := f.Enumerate(ctx, "experiences", "会議", false, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(page); total != 4 || !slices.Equal(got, []string{"experiences/e4", "experiences/e2"}) || page[0].Match != MatchExactWords {
		t.Fatalf("page 1: total %d, %v", total, got)
	}
	page, _, err = f.Enumerate(ctx, "experiences", "会議", false, page[1].Seq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(page); !slices.Equal(got, []string{"experiences/e1"}) {
		t.Fatalf("page 2: %v", got)
	}
}

// THE DIALOGUE ANSWERS A SHORT WORD BESIDE AN INDEXED ONE. The dialogue's
// word index is kept over a view of its rows and not over the table
// (conversations_searchable), and the read of a short word asks that index
// for the cue's other words by rowid, in one statement with the substring.
// Three turns hold the word for a meeting and one of them the English word
// too; the output of a tool holds both and is not the dialogue's to recall.
// The dialogue answers the cue of both words, the words layer finds the one
// turn, and a listing by the same words finds it.
func TestAShortWordBesideAnIndexedWordIsReadOverTheDialogue(t *testing.T) {
	f, now := withinStore(t)
	ctx := context.Background()
	const both = "会議のあとは quiet な午後でした"
	for _, turn := range [][2]string{
		{"operator", "会議は長かった"},
		{"resident", both},
		{"operator", "a quiet afternoon at the harbor"},
		{"system", "tool output: 会議 quiet"},
	} {
		if err := f.st.AddConversationTurn(turn[0], turn[1]); err != nil {
			t.Fatal(err)
		}
	}

	res, err := f.Recall(ctx, Query{Text: "会議 quiet", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if src := sourceOf(res, "conversations"); src.Status != StatusFound {
		t.Fatalf("THE DIALOGUE DID NOT ANSWER A SHORT WORD BESIDE AN INDEXED ONE: %+v", src)
	}
	var byWords []string
	for _, h := range res.Hits {
		if h.Store == "conversations" && slices.Contains(h.Layers, MatchExactWords) {
			byWords = append(byWords, h.Text)
		}
	}
	if !slices.Equal(byWords, []string{both}) {
		t.Fatalf("the words layer must find the one turn that holds both words, and no other: %q", byWords)
	}
	for _, src := range res.Sources {
		if src.Status != StatusFound && src.Status != StatusFoundNothing {
			t.Fatalf("%s answered %s (%s)", src.Store, src.Status, src.Detail)
		}
	}

	page, _, err := f.Enumerate(ctx, "conversations", "会議 quiet", false, 0, 10)
	if err != nil || len(page) != 1 || page[0].Text != both {
		t.Fatalf("a listing of the dialogue by the same words finds that turn: %+v %v", page, err)
	}
}

// A SHORT WORD THAT MIXES A LETTER WITH THE SCRIPT IS FOUND IN EITHER CASE.
// The word index folds case, so a cue of "t恤" found a memory whose whole
// run was "T恤". Read as a substring it was compared byte for byte, and
// was found only as the cue happened to be typed.
// It is found in whichever case the memory holds it, alone or inside a
// run, by a recall and by a listing; and a letter outside ASCII, here the
// full-width one that Chinese and Japanese text is typed with, is folded
// as the words beside it fold theirs.
func TestAShortWordThatMixesALetterWithTheScriptIsFoundInEitherCase(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 5, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "T恤 on the line", 1, now.Add(-4*time.Hour), false)
	addExperience(t, s, "e2", "昨天买了一件T恤很合身", 2, now.Add(-3*time.Hour), false)
	addExperience(t, s, "e3", "那件t恤洗过了", 3, now.Add(-2*time.Hour), false)
	addExperience(t, s, "e4", "新的Ｔ恤到了", 4, now.Add(-time.Hour), false)
	addExperience(t, s, "e5", "a quiet day of routine observations", 5, now, false)
	f := New(s)
	ctx := context.Background()
	for cue, want := range map[string][]string{
		"t恤": {"experiences/e1", "experiences/e2", "experiences/e3"},
		"T恤": {"experiences/e1", "experiences/e2", "experiences/e3"},
		"ｔ恤": {"experiences/e4"},
	} {
		res, err := f.Recall(ctx, Query{Text: cue, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedIDs(res.Hits); !slices.Equal(got, want) {
			t.Fatalf("%q: A WORD THE WORD INDEX FOUND IN EITHER CASE IS FOUND ONLY AS TYPED: %v, want %v", cue, got, want)
		}
		for _, h := range res.Hits {
			if h.Match != MatchExactWords {
				t.Fatalf("%q: the hit must say every word of the cue is in the text: %+v", cue, h)
			}
		}
		page, _, err := f.Enumerate(ctx, "experiences", cue, false, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if got := sortedIDs(page); !slices.Equal(got, want) {
			t.Fatalf("%q: a listing by words finds what the recall finds: %v, want %v", cue, got, want)
		}
	}
	if !carriesAllWords([]string{"t恤"}, "昨天买了一件T恤很合身") || carriesAllWords([]string{"t恤"}, "昨天买了一件衬衫") {
		t.Error("a text carries such a word in either case, and not where it is absent")
	}
}

// Which words are read inside a run: one that holds a character of
// Chinese or Japanese writing and is shorter than three characters. A
// longer one has a window in the trigram index; a short word of a spaced
// script is a token of the word index; and Thai is left to the indexes,
// since a Thai word reaches here in pieces (unspaced).
func TestOnlyAShortWordOfAnUnspacedScriptIsReadWithinARun(t *testing.T) {
	for word, want := range map[string]bool{
		"会議": true, "猫": true, "3月": true, "ねこ": true, "カメ": true,
		"会議室": false, "is": false, "a": false, "42": false, "": false, "한국": false, "ไป": false,
	} {
		if got := readWithin(word); got != want {
			t.Errorf("readWithin(%q) = %v, want %v", word, got, want)
		}
	}
	if !carriesAllWords([]string{"会議", "quiet"}, "会議のあとは quiet な午後") || carriesAllWords([]string{"会議"}, "明日は会う") {
		t.Error("a text carries such a word where the word is inside it, and not otherwise")
	}
}
