package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// seedBelief writes a belief straight into the projection, dated by the
// record's first event, which the caller has made.
func seedBelief(t *testing.T, st *store.Store, n int, statement string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES (?, ?, 3, 0.9, 0, 1, 1)`, fmt.Sprintf("b%02d", n), statement); err != nil {
		t.Fatal(err)
	}
}

func TestACutSectionOfBeliefsCanBeReadToItsEnd(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	places := []string{"cape", "reef", "inlet", "shoal", "bar", "spit", "sound", "bight", "strait", "narrows", "roads", "bank"}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "we passed a lighthouse before noon"}); err != nil {
		t.Fatal(err)
	}
	for i, p := range places {
		seedBelief(t, st, i+1, "the lighthouse on the "+p+" is manned")
	}

	// Across every store with a small limit, the beliefs are cut, and the
	// answer names a way on that the tool takes.
	cut := recallOut(t, engine, map[string]any{"query": "lighthouse", "limit": 3})
	if !regexp.MustCompile(`PAGED and possibly incomplete: [^.]*beliefs \(12 matched, [0-9] shown\)`).MatchString(cut) {
		t.Fatalf("with limit 3 the beliefs must be reported cut, with both counts:\n%s", cut)
	}
	if !strings.Contains(cut, "source=beliefs") {
		t.Fatalf("a cut section must name a way to read it on:\n%s", cut)
	}

	// The way it names works: the beliefs alone, ranked, to their end.
	whole := recallOut(t, engine, map[string]any{"query": "lighthouse", "source": "beliefs"})
	first := strings.SplitN(whole, "\n", 2)[0]
	if !strings.Contains(first, "in beliefs only") {
		t.Fatalf("the first line must say the answer is of beliefs alone:\n%s", whole)
	}
	if got := strings.Count(sectionOf(whole, "Beliefs:"), "\n  [") + 1; got != len(places) {
		t.Fatalf("source=beliefs shows %d of the %d beliefs that match:\n%s", got, len(places), whole)
	}
	if !strings.Contains(whole, "searched to completion: beliefs (12 matched, 12 shown)") || strings.Contains(whole, "PAGED") {
		t.Fatalf("read alone, the beliefs are complete and say so with their counts:\n%s", whole)
	}
	if strings.Contains(whole, "Experiences:") {
		t.Fatalf("one source was asked for and another answered:\n%s", whole)
	}

	// With no cue, every belief is listed, and the listing says it is whole.
	all := recallOut(t, engine, map[string]any{"query": "", "source": "beliefs"})
	if got := strings.Count(all, "\n  [b"); got != len(places) {
		t.Fatalf("source=beliefs with no query lists %d of %d beliefs:\n%s", got, len(places), all)
	}
	if !strings.Contains(all, "searched to completion: beliefs") {
		t.Fatalf("the whole listing must say it is whole:\n%s", all)
	}

	// What these sources do not take is refused in a sentence that says
	// what to do instead.
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"query": "lighthouse", "source": "beliefs", "after_seq": 5}); err == nil || !strings.Contains(err.Error(), "does not page source=beliefs") {
		t.Fatalf("after_seq on a ranked source: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"query": "", "source": "intentions"}); err == nil || !strings.Contains(err.Error(), "needs a query") {
		t.Fatalf("a ranked source with no cue: %v", err)
	}
}

func TestAMarkNamesTheLayersThatFoundAHit(t *testing.T) {
	// No source of vectors: a hit found by its words and by the fuzzy
	// layer is "both", and the mark says which two. Meaning is not one.
	plain, _, _, _, _ := setupEngine(t)
	if _, err := plain.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "the kingfisher struck the water at dusk"}); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, plain, map[string]any{"query": "kingfisher"})
	if exp := sectionOf(out, "Experiences:"); !strings.Contains(exp, "both (exact_words + fuzzy)") || strings.Contains(exp, "meaning") {
		t.Fatalf("found by its words twice over, the hit must say so and must not name meaning:\n%s", out)
	}
	if !strings.Contains(out, "Marks — exact_words:") || !strings.Contains(out, "only a mark that names meaning says the meaning layer found it") {
		t.Fatalf("the marks must be defined under the hits:\n%s", out)
	}

	// With a source: found by words and by meaning names both layers; found
	// by meaning alone is "meaning", with its similarity.
	e, _, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	if _, err := e.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "the ferry left the harbor late"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	both := sectionOf(recallOut(t, e, map[string]any{"query": "ferry harbor"}), "Experiences:")
	if !regexp.MustCompile(`both \([a-z_ +]*meaning [0-9.]+\), score [0-9.]+`).MatchString(both) {
		t.Fatalf("found by its words and by meaning, the mark must name meaning and carry the similarity:\n%s", both)
	}
	alone := sectionOf(recallOut(t, e, map[string]any{"query": "boat port"}), "Experiences:")
	if !regexp.MustCompile(`, meaning, similarity [0-9.]+, score [0-9.]+`).MatchString(alone) {
		t.Fatalf("found by meaning alone, the mark is meaning with its similarity:\n%s", alone)
	}
}

func TestTheFirstLineSaysWhetherTheMeaningLayerReadTheCue(t *testing.T) {
	head := func(out string) string { return strings.SplitN(out, "\n", 2)[0] }
	e, _, _, _, _ := setupEngine(t)
	if _, err := e.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "the ferry left the harbor late"}); err != nil {
		t.Fatal(err)
	}
	if h := head(recallOut(t, e, map[string]any{"query": "ferry"})); !strings.Contains(h, "meaning layer unavailable") {
		t.Fatalf("with nothing making vectors the first line must say the layer is unavailable: %s", h)
	}
	e.instruments.SetEmbedder(currencyEmbedder{})
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, e, map[string]any{"query": "ferry"})
	if h := head(out); !strings.Contains(h, "meaning layer consulted, floor 0.600") {
		t.Fatalf("with a source the first line must say the layer read the cue and under which floor: %s", h)
	}
	if h := head(recallOut(t, e, map[string]any{"query": "ferry", "exact": true})); !strings.Contains(h, "meaning layer not consulted") {
		t.Fatalf("under the exact override the first line must say the layer was not consulted: %s", h)
	}
	// Every source that answered states what it matched and what it shows,
	// in the same words whether or not it was cut.
	cov := out[strings.Index(out, "Coverage — "):]
	cov = cov[:strings.Index(cov, ".")]
	for _, part := range strings.Split(strings.TrimPrefix(cov, "Coverage — searched to completion: "), ", ") {
		if !regexp.MustCompile(`^[a-z_]+ \([0-9]+ matched$|^[0-9]+ shown\)$`).MatchString(part) {
			t.Fatalf("a source in the coverage line states no counts (%q):\n%s", part, out)
		}
	}
}
