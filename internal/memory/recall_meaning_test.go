package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
type fakeEmbedder struct {
	basis    string
	fail     error
	calls    int
	embedded []string
}

var (
	concepts = map[string]int{
		"harbor": 0, "harbour": 0, "port": 0,
		"ship": 1, "vessel": 1, "boat": 1,
		"tide": 2, "tides": 2,
	}
	// .
	// .
	wordDims = map[string]int{}
)

const fakeDims = 256

func fakeVector(text string) []float32 {
	v := make([]float32, fakeDims)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,;:!?")
		if c, ok := concepts[w]; ok {
			v[c] += 1
			continue
		}
		d, ok := wordDims[w]
		if !ok {
			d = 3 + len(wordDims)
			if d >= fakeDims {
				d = fakeDims - 1
			}
			wordDims[w] = d
		}
		v[d] += 0.2
	}
	return v
}

func (f *fakeEmbedder) Basis() (string, error) {
	if f.basis == "" {
		return "", errors.New("no embeddings model is named")
	}
	return f.basis, nil
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []string) (string, [][]float32, error) {
	f.calls++
	if f.fail != nil {
		return f.basis, nil, f.fail
	}
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = fakeVector(in)
		f.embedded = append(f.embedded, in)
	}
	return f.basis, out, nil
}

func meaningStore(t *testing.T) (*Facility, *fakeEmbedder, time.Time) {
	t.Helper()
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 3, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "the ship reached the harbor at dusk", 1, now.Add(-24*time.Hour), false)
	addExperience(t, s, "e2", "a quiet day of routine observations", 2, now.Add(-2*time.Hour), false)
	addBelief(t, s, "b1", "Tides govern the harbor", 3, 3)
	f := New(s)
	fe := &fakeEmbedder{basis: "fake/v1"}
	f.SetEmbedder(fe)
	return f, fe, now
}

func TestMeaningLayerMatchesWithinTheFloorAndSaysSo(t *testing.T) {
	f, fe, now := meaningStore(t)
	rep, err := f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 3 || rep.Basis != "fake/v1" {
		t.Fatalf("backfill = %+v %v, want 3 rows under fake/v1", rep, err)
	}
	if fe.calls != 2 {
		t.Fatalf("two stores hold rows, so two provider calls, got %d", fe.calls)
	}
	basis, cov, unavailable, err := f.VectorCoverage(context.Background())
	if err != nil || unavailable != "" || basis != "fake/v1" {
		t.Fatalf("coverage: %v %q %q", err, basis, unavailable)
	}
	for _, c := range cov {
		if (c.Store == "experiences" && (c.Rows != 2 || c.Embedded != 2)) || (c.Store == "beliefs" && (c.Rows != 1 || c.Embedded != 1)) {
			t.Fatalf("coverage %+v", c)
		}
	}
	if line := RenderCoverage(basis, cov, unavailable); !strings.Contains(line, "complete") || !strings.Contains(line, "experiences 2/2") {
		t.Fatalf("coverage line: %s", line)
	}

	// .
	res, err := f.Recall(context.Background(), Query{Text: "port", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meaning.Status != StatusFound || res.Meaning.Basis != "fake/v1" {
		t.Fatalf("meaning status = %+v", res.Meaning)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits = %v, want the experience and the belief that mean harbor", hitIDs(res.Hits))
	}
	for _, h := range res.Hits {
		if h.Match != MatchMeaning || h.Similarity < SemanticFloor {
			t.Fatalf("a meaning-only hit must say so with its similarity: %+v", h)
		}
		if h.Snippet == "" {
			t.Fatalf("a meaning hit carries a snippet: %+v", h)
		}
	}
	if src := sourceOf(res, "experiences"); src.Status != StatusFound || src.Matched != 1 {
		t.Fatalf("experiences: %+v, want one row matched by meaning", src)
	}
	// .
	res, _ = f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	for _, h := range res.Hits {
		if h.Match != MatchBoth {
			t.Fatalf("harbor is a word and a meaning: %+v", h)
		}
	}
	if src := sourceOf(res, "experiences"); src.Matched != 1 {
		t.Fatalf("a row found by every layer is matched once: %+v", src)
	}
	// .
	res, _ = f.Recall(context.Background(), Query{Text: "quantum", Now: now})
	if len(res.Hits) != 0 || res.Meaning.Status != StatusFoundNothing {
		t.Fatalf("quantum means nothing here: %v %+v", hitIDs(res.Hits), res.Meaning)
	}
	// .
	calls := fe.calls
	res, _ = f.Recall(context.Background(), Query{Text: "port", Exact: true, Now: now})
	if len(res.Hits) != 0 || res.Meaning.Status != "" || fe.calls != calls {
		t.Fatalf("exact must not reach the meaning layer: %v %+v calls %d→%d", hitIDs(res.Hits), res.Meaning, calls, fe.calls)
	}
}

func TestMeaningLayerIsUnavailableNotSilent(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 1, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "the ship reached the harbor at dusk", 1, now.Add(-24*time.Hour), false)
	f := New(s)

	// .
	res, err := f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	if err != nil || len(res.Hits) != 1 || res.Hits[0].Match == MatchMeaning {
		t.Fatalf("the words must still answer: %v %v", err, hitIDs(res.Hits))
	}
	if res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, "no embeddings model") {
		t.Fatalf("no endpoint must be source_unavailable with the remedy: %+v", res.Meaning)
	}
	rep, err := f.Backfill(context.Background(), 0)
	if err != nil || rep.Unavailable == "" || rep.Embedded != 0 {
		t.Fatalf("backfill without an endpoint idles and says so: %+v %v", rep, err)
	}
	if !strings.Contains(rep.Line(), "idle") {
		t.Fatalf("receipt: %s", rep.Line())
	}
	if _, _, unavailable, err := f.VectorCoverage(context.Background()); err != nil || unavailable == "" {
		t.Fatalf("coverage without an endpoint: %q %v", unavailable, err)
	}
	if line := RenderCoverage("", nil, "no embeddings model is named"); !strings.Contains(line, "unavailable") {
		t.Fatalf("coverage line: %s", line)
	}

	// .
	fe := &fakeEmbedder{basis: "fake/v1", fail: errors.New("API returned 503")}
	f.SetEmbedder(fe)
	res, err = f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("the words must still answer: %v %v", err, hitIDs(res.Hits))
	}
	if res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, "503") {
		t.Fatalf("a failing provider must be source_unavailable with its error: %+v", res.Meaning)
	}
	if _, err := f.Backfill(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("a failing provider stops the pass with its error: %v", err)
	}
}

func TestBackfillIsIncrementalInvalidatedByBasisAndPruned(t *testing.T) {
	f, fe, _ := meaningStore(t)
	rep, err := f.Backfill(context.Background(), 2)
	if err != nil || rep.Embedded != 2 {
		t.Fatalf("a budget of two embeds two: %+v %v", rep, err)
	}
	var remaining int64
	for _, s := range rep.Stores {
		remaining += s.Remaining
	}
	if remaining != 1 {
		t.Fatalf("one row remains: %+v", rep.Stores)
	}
	if !strings.Contains(rep.Line(), "2 embedded, 1 remaining") {
		t.Fatalf("receipt: %s", rep.Line())
	}
	rep, _ = f.Backfill(context.Background(), 2)
	if rep.Embedded != 1 {
		t.Fatalf("the next pass finishes: %+v", rep)
	}
	rep, _ = f.Backfill(context.Background(), 2)
	if rep.Embedded != 0 || rep.Dropped != 0 {
		t.Fatalf("a complete record embeds nothing: %+v", rep)
	}
	// .
	fe.basis = "fake/v2"
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Dropped != 3 || rep.Embedded != 3 || rep.Basis != "fake/v2" {
		t.Fatalf("a basis change invalidates and refills: %+v %v", rep, err)
	}
	if n, _ := f.st.MemoryVectorCount("experiences", "fake/v1"); n != 0 {
		t.Fatalf("old-basis vectors remain: %d", n)
	}
	// .
	if _, err := f.st.DB().Exec(`DELETE FROM experiences WHERE id = 'e2'`); err != nil {
		t.Fatal(err)
	}
	rep, _ = f.Backfill(context.Background(), 0)
	if rep.Pruned != 1 || rep.Embedded != 0 {
		t.Fatalf("a gone row is pruned: %+v", rep)
	}
	// .
	long := strings.Repeat("x", MaxEmbedChars+100)
	if got := clipForEmbedding(long); len([]rune(got)) != MaxEmbedChars {
		t.Fatalf("clip = %d runes", len([]rune(got)))
	}
}

func TestConversationVectorsAreBoundedToTheWindow(t *testing.T) {
	s := newStore(t)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "fake/v1"})
	for i := 0; i < ConversationVectorWindow+10; i++ {
		role := "operator"
		if i%2 == 1 {
			role = "resident"
		}
		if err := s.AddConversationTurn(role, fmt.Sprintf("turn %d about the harbor", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddConversationTurn("system", "tool output: never embedded"); err != nil {
		t.Fatal(err)
	}
	rep, err := f.Backfill(context.Background(), ConversationVectorWindow+100)
	if err != nil {
		t.Fatal(err)
	}
	var conv BackfillStore
	for _, bs := range rep.Stores {
		if bs.Store == "conversations" {
			conv = bs
		}
	}
	if conv.Embedded != ConversationVectorWindow || conv.Remaining != 0 {
		t.Fatalf("conversations = %+v, want the newest %d embedded and nothing remaining", conv, ConversationVectorWindow)
	}
	_, cov, _, err := f.VectorCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cov {
		if c.Store == "conversations" && (c.Rows != ConversationVectorWindow || c.Embedded != ConversationVectorWindow) {
			t.Fatalf("coverage %+v", c)
		}
	}
	// .
	var oldest int64
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_vectors v JOIN conversations c ON c.id = v.id WHERE v.store = 'conversations' AND c.turn_seq <= 10`).Scan(&oldest); err != nil || oldest != 0 {
		t.Fatalf("the oldest turns must hold no vector: %d %v", oldest, err)
	}
	if math.Abs(float64(len(cov))-float64(len(Stores()))) > 0 {
		t.Fatalf("coverage covers every store: %d of %d", len(cov), len(Stores()))
	}
}
