package memory

import (
	"testing"
)

func TestAnyWordsRetrievesShortLessonForFullGoalWithoutExpansion(t *testing.T) {
	st := newStore(t)
	addPluginMemory(t, st, "lesson", "p", "compiler fixture recipe: inspect the real schema before editing", "")
	addPluginMemory(t, st, "distractor", "p", "compiler history from an unrelated project", "")
	addPluginMemory(t, st, "unrelated", "p", "maritime tides shipping", "")
	source := &fakeEmbedder{basis: "fixture/v1"}
	f := New(st)
	f.SetEmbedder(source)
	goal := "Inspect the compiler fixture and schema, repair the missing argument, run the test suite, preserve unrelated files, and summarize verification results in the requested output format."
	q := Query{Text: goal, Stores: []string{"plugin_memories"}, PluginID: "p", WordsOnly: true, Decay: DecayNone, Limit: 5}
	before, e := f.Recall(t.Context(), q)
	if e != nil || len(before.Hits) != 0 {
		t.Fatal("default query changed", before, e)
	}
	q.AnyWords = true
	q.WordsOnly = false
	got, e := f.Recall(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]bool{}
	for _, h := range got.Hits {
		ids[h.ID] = true
		if h.Match != MatchExactWords {
			t.Fatal("AnyWords used a fuzzy layer", h.Match)
		}
	}
	if !ids["lesson"] || ids["unrelated"] || source.calls != 0 {
		t.Fatal(ids, source.calls)
	}
	q.Text = "compiller"
	got, e = f.Recall(t.Context(), q)
	if e != nil || len(got.Hits) != 0 || source.calls != 0 {
		t.Fatal("AnyWords expanded a miss", got, e, source.calls)
	}
}
func TestAnyWordsQuotesTermsAndRejectsConflictingModes(t *testing.T) {
	if got := anyWordsQuery([]string{"compiler", "OR", `a"b`}); got != `"compiler" OR "OR" OR "a""b"` {
		t.Fatal(got)
	}
	f := New(newStore(t))
	for _, q := range []Query{{Text: "compiler", AnyWords: true, Exact: true}, {AnyWords: true, Recent: true}} {
		if _, e := f.Recall(t.Context(), q); e == nil {
			t.Fatal("conflicting modes accepted", q)
		}
	}
}
