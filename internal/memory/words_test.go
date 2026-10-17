package memory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
)

// Salience's cost-to-query (memory/salience) says how hard a statement is
// to find again by its words, and counts trigram.Words to say it; recall
// finds by wordsOf. The two are the same runs of letters and digits, one
// case folded. This holds them together, so neither split can drift
// without the feature's meaning going with it.
func TestCostToQueryCountsTheWordsRecallSplits(t *testing.T) {
	for _, s := range []string{
		"", "   ", "it is what it is", "İt IS", "The THE tHe harbour",
		"Ünïcödé wörds and the Ålesund", "it's 42 of 7 — and/or ... the",
		"a\xffthe\xfeb", "日本語 the テスト", "snake_case x1y2",
	} {
		var folded []string
		for _, w := range wordsOf(s) {
			folded = append(folded, strings.ToLower(w))
		}
		if got := trigram.Words(s); !reflect.DeepEqual(got, folded) {
			t.Errorf("%q: salience counts %q, recall splits %q", s, got, folded)
		}
	}
}
