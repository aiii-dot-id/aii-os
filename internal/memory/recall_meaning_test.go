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

// recall_meaning_test.go — the meaning layer (phase 2), each property
// by name: a match by meaning within the floor, "both" when the words
// agree, found_nothing under the floor, the exact override consulting
// nothing, source_unavailable without an endpoint and on a provider
// failure, and the backfill: incremental, invalidated by a basis
// change, pruned with its rows, bounded to the conversation window.

// fakeEmbedder makes vectors from a small concept table: words that
// mean the same thing share a dimension, other words hash to one of the
// remaining dimensions with a small weight. Enough to make "port" mean
// "harbor" and "quantum" mean nothing here.
type fakeEmbedder struct {
	basis    string
	fail     error
	calls    int
	embedded []string
	// floor is the source's own; nil means the provider's.
	floor *float64
	// kinds is what each call was told its texts are.
	kinds []Kind
	// batch and stopAfter make the fake a source on this machine: asked
	// batch texts a call, and asking the pass to stop after stopAfter calls.
	batch, stopAfter int
}

func (f *fakeEmbedder) Floor() float64 {
	if f.floor != nil {
		return *f.floor
	}
	return ProviderFloor
}

var (
	concepts = map[string]int{
		"harbor": 0, "harbour": 0, "port": 0,
		"ship": 1, "vessel": 1, "boat": 1,
		"tide": 2, "tides": 2,
	}
	// Every other word gets a dimension of its own, so two unrelated
	// texts share nothing and the floor is what decides.
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

func (f *fakeEmbedder) Embed(_ context.Context, kind Kind, inputs []string) (string, [][]float32, error) {
	f.kinds = append(f.kinds, kind)
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

// Batch and MayContinue make the fake a source that says how it is asked,
// when a test sets them; unset, it is asked as a provider is.
func (f *fakeEmbedder) Batch() int { return f.batch }

func (f *fakeEmbedder) MayContinue() bool { return f.stopAfter == 0 || f.calls < f.stopAfter }

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

	// "port" is not a word in the record; it means harbor.
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
		if h.Match != MatchMeaning || h.Similarity < ProviderFloor {
			t.Fatalf("a meaning-only hit must say so with its similarity: %+v", h)
		}
		if h.Snippet == "" {
			t.Fatalf("a meaning hit carries a snippet: %+v", h)
		}
	}
	if src := sourceOf(res, "experiences"); src.Status != StatusFound || src.Matched != 1 {
		t.Fatalf("experiences: %+v, want one row matched by meaning", src)
	}
	// The words and the meaning agree: both.
	res, _ = f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	for _, h := range res.Hits {
		if h.Match != MatchBoth {
			t.Fatalf("harbor is a word and a meaning: %+v", h)
		}
	}
	if src := sourceOf(res, "experiences"); src.Matched != 1 {
		t.Fatalf("a row found by every layer is matched once: %+v", src)
	}
	// Under the floor: nothing, and the layer says found_nothing.
	res, _ = f.Recall(context.Background(), Query{Text: "quantum", Now: now})
	if len(res.Hits) != 0 || res.Meaning.Status != StatusFoundNothing {
		t.Fatalf("quantum means nothing here: %v %+v", hitIDs(res.Hits), res.Meaning)
	}
	// The exact override consults no meaning.
	calls := fe.calls
	res, _ = f.Recall(context.Background(), Query{Text: "port", Exact: true, Now: now})
	if len(res.Hits) != 0 || res.Meaning.Status != "" || fe.calls != calls {
		t.Fatalf("exact must not reach the meaning layer: %v %+v calls %d→%d", hitIDs(res.Hits), res.Meaning, calls, fe.calls)
	}
}

func TestTheFloorIsTheSourcesOwn(t *testing.T) {
	f, fe, now := meaningStore(t)
	if _, err := f.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	ask := func() Result {
		t.Helper()
		res, err := f.Recall(context.Background(), Query{Text: "port quantum", Stores: []string{"experiences"}, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := ask()
	if res.Meaning.Status != StatusFound || res.Meaning.Floor != ProviderFloor || len(res.Hits) != 1 || res.Hits[0].ID != "e1" {
		t.Fatalf("under the provider's floor: %+v, hits %v", res.Meaning, hitIDs(res.Hits))
	}
	sim := res.Hits[0].Similarity
	if sim <= 0.6 || sim >= 0.95 {
		t.Fatalf("the fixture's similarity moved (%.3f); the floors below no longer bracket it", sim)
	}
	high, low, bad := 0.95, 0.3, math.NaN()
	fe.floor = &high
	if res = ask(); res.Meaning.Status != StatusFoundNothing || res.Meaning.Floor != high || len(res.Hits) != 0 {
		t.Fatalf("under a source's floor of %.2f a match at %.2f must not be reported: %+v, hits %v", high, sim, res.Meaning, hitIDs(res.Hits))
	}
	fe.floor = &low
	if res = ask(); res.Meaning.Status != StatusFound || res.Meaning.Floor != low || len(res.Hits) != 1 {
		t.Fatalf("under a source's floor of %.2f: %+v, hits %v", low, res.Meaning, hitIDs(res.Hits))
	}
	for _, f2 := range []float64{bad, -0.1, 1.5} {
		v := f2
		fe.floor = &v
		if res = ask(); res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, "no usable floor") || len(res.Hits) != 0 {
			t.Fatalf("a floor of %v is not a cosine; the layer must be unavailable and say why: %+v, hits %v", f2, res.Meaning, hitIDs(res.Hits))
		}
	}
}

// tableEmbedder makes the vector a test names for each text, under the
// floor the test names.
type tableEmbedder struct {
	floor   float64
	vectors map[string][]float32
}

func (tableEmbedder) Basis() (string, error) { return "table/v1", nil }
func (e tableEmbedder) Floor() float64       { return e.floor }

func (e tableEmbedder) Embed(_ context.Context, _ Kind, inputs []string) (string, [][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v, ok := e.vectors[in]
		if !ok {
			return "table/v1", nil, fmt.Errorf("the table holds no vector for %q", in)
		}
		out[i] = v
	}
	return "table/v1", out, nil
}

func TestTheMeaningFloorIsComparedOnTheNormalisedCosine(t *testing.T) {
	const dims = 202
	cue, long, short := make([]float32, dims), make([]float32, dims), make([]float32, dims)
	cue[0] = 1
	long[0], long[1] = 39.6, 127
	short[0], short[1] = 40.4, 127
	for i := 2; i < 102; i++ {
		long[i] = 0.6 // rounds up to one
	}
	for i := 102; i < dims; i++ {
		short[i] = 0.45 // rounds down to nothing
	}
	s := newStore(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 2, now.Add(-48*time.Hour))
	addExperience(t, s, "lengthened", "a lengthened memory", 1, now.Add(-24*time.Hour), false)
	addExperience(t, s, "shortened", "a shortened memory", 2, now.Add(-24*time.Hour), false)
	f := New(s)
	f.SetEmbedder(tableEmbedder{floor: 0.30, vectors: map[string][]float32{
		"lantern": cue, "a lengthened memory": long, "a shortened memory": short,
	}})
	if rep, err := f.Backfill(context.Background(), 0); err != nil || rep.Embedded != 2 {
		t.Fatalf("backfill: %+v %v", rep, err)
	}
	res, err := f.Recall(context.Background(), Query{Text: "lantern", Stores: []string{"experiences"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ID != "shortened" || res.Meaning.Status != StatusFound {
		t.Fatalf("under a floor of 0.30 the shortened memory (normalised 0.3004) is offered and the lengthened (0.2996) is not; got %v %+v", hitIDs(res.Hits), res.Meaning)
	}
	if sim := res.Hits[0].Similarity; math.Abs(sim-0.30041) > 5e-5 {
		t.Fatalf("the hit carries the cosine it was compared on, the normalised 0.3004; got %.5f", sim)
	}
}

func TestACueWithNoSearchableWordsIsStillReadByMeaning(t *testing.T) {
	same := []float32{1, 0, 0, 0}
	s := newStore(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 1, now.Add(-48*time.Hour))
	addExperience(t, s, "gouda", "a wedge of aged gouda from the market", 1, now.Add(-24*time.Hour), false)
	f := New(s)
	f.SetEmbedder(tableEmbedder{floor: ProviderFloor, vectors: map[string][]float32{
		"a wedge of aged gouda from the market": same, "cheese": same, "🧀": same,
	}})
	if rep, err := f.Backfill(context.Background(), 0); err != nil || rep.Embedded != 1 {
		t.Fatalf("backfill: %+v %v", rep, err)
	}
	for _, cue := range []string{"cheese", "🧀"} {
		res, err := f.Recall(context.Background(), Query{Text: cue, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if res.Meaning.Status != StatusFound || len(res.Hits) != 1 || res.Hits[0].ID != "gouda" || res.Hits[0].Match != MatchMeaning || math.Abs(res.Hits[0].Similarity-1) > 1e-3 {
			t.Fatalf("cue %q: meaning %+v, hits %v %+v; want the one memory, by meaning, at similarity 1", cue, res.Meaning, hitIDs(res.Hits), res.Hits)
		}
		if src := sourceOf(res, "experiences"); src.Status != StatusFound || src.Matched != 1 || src.Shown != 1 {
			t.Fatalf("cue %q: experiences %+v; want the row matched by meaning and shown", cue, src)
		}
	}
	for _, q := range []Query{{Text: "🧀", Exact: true, Now: now}, {Text: "🧀", WordsOnly: true, Now: now}} {
		res, err := f.Recall(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		src := sourceOf(res, "experiences")
		if len(res.Hits) != 0 || res.Meaning.Status != "" || src.Status != StatusFoundNothing || !strings.Contains(src.Detail, "no searchable words") {
			t.Fatalf("exact %v, words only %v: hits %v, meaning %+v, experiences %+v; want nothing searched and the layer not consulted", q.Exact, q.WordsOnly, hitIDs(res.Hits), res.Meaning, src)
		}
	}
}

func TestMeaningLayerIsUnavailableNotSilent(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 1, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "the ship reached the harbor at dusk", 1, now.Add(-24*time.Hour), false)
	f := New(s)

	// No endpoint at all.
	res, err := f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	if err != nil || len(res.Hits) != 1 || res.Hits[0].Match == MatchMeaning {
		t.Fatalf("the words must still answer: %v %v", err, hitIDs(res.Hits))
	}
	if res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, NoSource) {
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

	// A provider that fails: the words answer, the layer names the failure.
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

	// Vectors that cannot be read: the source answers the cue and the read
	// fails in every store. No memory was scored, so the layer is not
	// found_nothing; it is unavailable, and says why.
	fe.fail = nil
	if rep, err := f.Backfill(context.Background(), 0); err != nil || rep.Embedded != 1 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}
	if _, err := s.DB().Exec(`DROP TABLE memory_vectors`); err != nil {
		t.Fatal(err)
	}
	res, err = f.Recall(context.Background(), Query{Text: "harbor", Now: now})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("the words must still answer: %v %v", err, hitIDs(res.Hits))
	}
	if res.Meaning.Status != StatusSourceUnavailable || !strings.Contains(res.Meaning.Detail, "could not be read") || !strings.Contains(res.Meaning.Detail, "memory_vectors") {
		t.Fatalf("a meaning read that failed in every store must be source_unavailable with its error: %+v", res.Meaning)
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
	// A new basis: everything the old one made is dropped and remade.
	fe.basis = "fake/v2"
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Dropped != 3 || rep.Embedded != 3 || rep.Basis != "fake/v2" {
		t.Fatalf("a basis change invalidates and refills: %+v %v", rep, err)
	}
	if n, _ := f.st.MemoryVectorCount("experiences", "fake/v1"); n != 0 {
		t.Fatalf("old-basis vectors remain: %d", n)
	}
	// A row that is gone loses its vector.
	if _, err := f.st.DB().Exec(`DELETE FROM experiences WHERE id = 'e2'`); err != nil {
		t.Fatal(err)
	}
	rep, _ = f.Backfill(context.Background(), 0)
	if rep.Pruned != 1 || rep.Embedded != 0 {
		t.Fatalf("a gone row is pruned: %+v", rep)
	}
	// A long input is embedded by its head.
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
	// The window is the newest turns: the oldest ten hold no vector.
	var oldest int64
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM memory_vectors v JOIN conversations c ON c.id = v.id WHERE v.store = 'conversations' AND c.turn_seq <= 10`).Scan(&oldest); err != nil || oldest != 0 {
		t.Fatalf("the oldest turns must hold no vector: %d %v", oldest, err)
	}
	if math.Abs(float64(len(cov))-float64(len(Stores()))) > 0 {
		t.Fatalf("coverage covers every store: %d of %d", len(cov), len(Stores()))
	}
}

// THE SOURCE IS TOLD WHAT A TEXT IS. A model may read a cue and a memory
// differently (the first local model prefixes a cue and not a memory), so
// a vector made for the wrong kind is a wrong vector that looks like a
// right one. The backfill hands memories; a recall hands its cue.
func TestTheSourceIsToldAMemoryFromACue(t *testing.T) {
	f, fe, now := meaningStore(t)
	if _, err := f.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if len(fe.kinds) == 0 {
		t.Fatal("rig: the backfill asked for nothing")
	}
	for i, kind := range fe.kinds {
		if kind != KindMemory {
			t.Fatalf("backfill call %d handed its texts over as %q, want %q", i, kind, KindMemory)
		}
	}
	fe.kinds = nil
	if _, err := f.Recall(context.Background(), Query{Text: "port", Now: now}); err != nil {
		t.Fatal(err)
	}
	if len(fe.kinds) != 1 || fe.kinds[0] != KindCue {
		t.Fatalf("a recall handed its cue over as %v, want one call of %q", fe.kinds, KindCue)
	}
}

// A SOURCE ON THIS MACHINE IS ASKED ONE TEXT A CALL, AND MAY STOP A PASS.
// A provider is asked in batches because a call costs a request. A source
// on this machine takes one call at a time, so its backfill asks for one
// memory in each: a cue then waits behind one memory at most. And the
// source may say, before any call, that the pass should stop: the text in
// flight was finished, none follows, what landed stays, and the report
// says the pass stopped at the source's word. The next pass continues.
func TestASourceOnThisMachineIsAskedOneTextACallAndMayStopAPass(t *testing.T) {
	f, fe, _ := meaningStore(t)
	fe.batch = 1
	rep, err := f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 3 || fe.calls != 3 || rep.Yielded {
		t.Fatalf("three rows, one a call, none stopped: %+v, %d calls, %v", rep, fe.calls, err)
	}

	g, ge, _ := meaningStore(t)
	ge.batch, ge.stopAfter = 1, 2
	rep, err = g.Backfill(context.Background(), 0)
	if err != nil || !rep.Yielded || rep.Embedded != 2 || ge.calls != 2 {
		t.Fatalf("stopped before the third call, with two landed: %+v, %d calls, %v", rep, ge.calls, err)
	}
	if !strings.Contains(rep.Line(), "stopped early at the source's word") {
		t.Fatalf("the receipt does not say the pass stopped: %s", rep.Line())
	}
	var remaining int64
	for _, s := range rep.Stores {
		remaining += s.Remaining
	}
	if remaining != 1 {
		t.Fatalf("one row is still to do, and the report must say so: %+v", rep.Stores)
	}
	ge.stopAfter = 0
	rep, err = g.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 1 || rep.Yielded {
		t.Fatalf("the next pass finishes what was left: %+v %v", rep, err)
	}

	// A provider, which says nothing, is asked for a store's rows together.
	h, he, _ := meaningStore(t)
	if _, err := h.Backfill(context.Background(), 0); err != nil || he.calls != 2 {
		t.Fatalf("a source that says nothing is asked a batch a store: %d calls, %v", he.calls, err)
	}
}
