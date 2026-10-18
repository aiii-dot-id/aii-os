package memory

import (
	"context"
	"testing"
)

// A caller that compares the texts itself asks for the words alone: the
// source is not asked, and the layer's status is empty, as under the
// exact override.
func TestARecallByWordsOnlyNeverAsksTheSource(t *testing.T) {
	f, e, now := meaningStore(t)
	if _, err := f.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	asked := e.calls
	res, err := f.Recall(context.Background(), Query{Text: "port", WordsOnly: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if e.calls != asked {
		t.Fatalf("a recall by words only asked the source %d time(s)", e.calls-asked)
	}
	if res.Meaning.Status != "" {
		t.Fatalf("a recall by words only reports the layer as %q; it was not consulted", res.Meaning.Status)
	}
	for _, h := range res.Hits {
		if h.Match == MatchMeaning || h.Match == MatchBoth {
			t.Fatalf("a recall by words only brought a hit by meaning: %+v", h)
		}
	}
	// The same cue with the layer consulted does find by meaning, so the
	// assertion above is not vacuous.
	res, err = f.Recall(context.Background(), Query{Text: "port", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if e.calls != asked+1 || res.Meaning.Status != StatusFound {
		t.Fatalf("the same cue with the layer consulted: %d call(s), status %q; want one call and found", e.calls-asked, res.Meaning.Status)
	}
}
