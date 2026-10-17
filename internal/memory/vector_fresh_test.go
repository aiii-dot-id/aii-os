package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// shaOfRow is the hash a vector of this row must carry to speak for it:
// the row's text as recall reads it. A fixture that writes a vector by
// hand takes it from here, because a vector is held to its text.
func shaOfRow(t *testing.T, s *store.Store, d Store, id string) string {
	t.Helper()
	texts := make([]string, len(d.Text))
	dest := make([]any, len(texts))
	for i := range texts {
		dest[i] = &texts[i]
	}
	if err := s.DB().QueryRow("SELECT "+strings.TrimPrefix(textColumns(d), ", ")+" FROM "+d.Name+" b WHERE b.id = ?", id).Scan(dest...); err != nil {
		t.Fatalf("the text of %s/%s: %v", d.Name, id, err)
	}
	return textSHA(joinText(texts))
}

// A VECTOR IS HELD TO THE TEXT IT WAS MADE FROM. Every vector has carried
// the hash of the text that was embedded, and nothing read it: a row was
// embedded for as long as a vector of its id existed. A belief restated
// kept the vector of what it used to say. By meaning it was offered for
// cues it no longer answered, with its new words under the old score, and
// it was not found for what it now says, until the basis changed.
//
// Here a belief about a harbor is embedded, and then restated to speak of
// a ship; and an experience that is nearer the cue than any other row of
// its store is rewritten to speak of a lantern. Before any pass has run, a
// cue that means harbor must not be answered with either: the vectors
// still in the table are the old texts'. The row of the same store that
// ranks behind the rewritten one, and did not change, is still found. The
// next pass drops those two vectors, asks the source for those two rows
// and no other, and says so. After it the belief is found by what it says
// now and not by what it said. A pass over a record that has not changed
// drops nothing and asks nothing.
func TestAMemoryWhoseTextChangesIsHeldToItsNewTextAndEmbeddedAgain(t *testing.T) {
	f, fe, now := meaningStore(t)
	ctx := context.Background()
	addExperience(t, f.st, "e3", "the harbor", 3, now.Add(-time.Hour), false)
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 4 || rep.Stale != 0 {
		t.Fatalf("rig: the four rows are embedded: %+v %v", rep, err)
	}
	found := func(cue string) []string {
		t.Helper()
		res, err := f.Recall(ctx, Query{Text: cue, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return hitIDs(res.Hits)
	}
	if got := found("port"); !slices.Contains(got, "beliefs/b1") || !slices.Contains(got, "experiences/e1") || !slices.Contains(got, "experiences/e3") {
		t.Fatalf("rig: a cue that means harbor finds the belief and both experiences: %v", got)
	}

	const restated, rewritten = "The ship keeps its own counsel", "a lantern in the window"
	if _, err := f.st.DB().Exec(`UPDATE beliefs SET statement = ? WHERE id = 'b1'`, restated); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().Exec(`UPDATE experiences SET content = ? WHERE id = 'e3'`, rewritten); err != nil {
		t.Fatal(err)
	}
	got := found("port")
	if slices.Contains(got, "beliefs/b1") || slices.Contains(got, "experiences/e3") {
		t.Fatalf("a row that no longer speaks of a harbor was offered for one, by the vector of what it used to say: %v", got)
	}
	if !slices.Contains(got, "experiences/e1") {
		t.Fatalf("the experience behind the rewritten one in its store did not change, and is still found: %v", got)
	}

	asked := len(fe.embedded)
	rep, err := f.Backfill(ctx, 0)
	if err != nil || rep.Stale != 2 || rep.Embedded != 2 {
		t.Fatalf("the pass must drop the two vectors of the changed rows and make those rows again: %+v %v", rep, err)
	}
	if again := fe.embedded[asked:]; len(again) != 2 || !slices.Contains(again, restated) || !slices.Contains(again, rewritten) {
		t.Fatalf("the source was asked for %q; only the changed rows are asked again", again)
	}
	if !strings.Contains(rep.Line(), "2 vectors of rows whose text changed dropped") {
		t.Fatalf("the receipt does not say a changed row was made again: %s", rep.Line())
	}
	if got := found("vessel"); !slices.Contains(got, "beliefs/b1") {
		t.Fatalf("the belief is not found by what it says now: %v", got)
	}
	if got := found("port"); slices.Contains(got, "beliefs/b1") {
		t.Fatalf("the belief is still found by what it used to say: %v", got)
	}

	calls := fe.calls
	rep, err = f.Backfill(ctx, 0)
	if err != nil || rep.Stale != 0 || rep.Embedded != 0 || fe.calls != calls {
		t.Fatalf("a record that has not changed drops nothing and asks nothing: %+v %v, calls %d to %d", rep, err, calls, fe.calls)
	}
}

func TestAFreshMeaningMatchIsNotHiddenBehindStaleVectors(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 5, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "harbor", 1, now.Add(-5*time.Hour), false)
	addExperience(t, s, "e2", "harbor", 2, now.Add(-4*time.Hour), false)
	addExperience(t, s, "e3", "harbor", 3, now.Add(-3*time.Hour), false)
	addExperience(t, s, "e4", "the harbor at dusk", 4, now.Add(-2*time.Hour), false)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "fake/v1"})
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 4 {
		t.Fatalf("rig: %+v %v", rep, err)
	}
	for i, id := range []string{"e1", "e2", "e3"} {
		if _, err := s.DB().Exec(`UPDATE experiences SET content = ? WHERE id = ?`, []string{"a lantern", "a candle", "a lamp"}[i], id); err != nil {
			t.Fatal(err)
		}
	}
	res, err := f.Recall(ctx, Query{Text: "port", Limit: 1, Now: now, Stores: []string{"experiences"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); !slices.Equal(got, []string{"experiences/e4"}) || res.Meaning.Status != StatusFound {
		t.Fatalf("the unchanged row that means harbor was hidden behind three stale vectors: %v (%s)", got, res.Meaning.Status)
	}
}

// staleAhead writes n experiences that each still hold the vector of
// "harbor" though their text has changed since, ranked ahead of anything
// that means a harbor less exactly, and after them one unchanged row, "the
// harbor at dusk", holding its own vector: in one transaction for the
// rows and one for the vectors, as a pass would leave them.
func staleAhead(t *testing.T, s *store.Store, n int, now time.Time) string {
	t.Helper()
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fresh := fmt.Sprintf("e%d", n+1)
	var vs []store.MemoryVector
	for i := 1; i <= n+1; i++ {
		id, content, madeFrom := fmt.Sprintf("e%d", i), fmt.Sprintf("a lantern, the %dth", i), "harbor"
		if id == fresh {
			content, madeFrom = "the harbor at dusk", "the harbor at dusk"
		}
		at := now.Add(time.Duration(i-n-2) * time.Minute).UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'test.event', 3, '{}', '')`, i, at); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', ?, ?)`, id, content, i, at); err != nil {
			t.Fatal(err)
		}
		q, scale := vec.Quantize(fakeVector(madeFrom))
		vs = append(vs, store.MemoryVector{Store: "experiences", ID: id, Basis: "fake/v1", ContentSHA: textSHA(madeFrom), Dims: len(q), Scale: scale, Q: q})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMemoryVectors(vs); err != nil {
		t.Fatal(err)
	}
	return "experiences/" + fresh
}

func TestAFreshMeaningMatchIsFoundBehindMoreStaleVectorsThanAnyMultipleOfThePool(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	fresh := staleAhead(t, s, 50, now)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "fake/v1"})
	res, err := f.Recall(context.Background(), Query{Text: "port", Limit: 1, Now: now, Stores: []string{"experiences"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); !slices.Equal(got, []string{fresh}) || res.Meaning.Status != StatusFound {
		t.Fatalf("the unchanged row that means harbor was hidden behind fifty stale vectors: %v (%s %s)", got, res.Meaning.Status, res.Meaning.Detail)
	}
}

// AND WHERE THE BOUND STOPS THE READ, THE ANSWER SAYS SO. Past
// MeaningReadBound candidates the layer reads no further; with every one of
// them stale it has found nothing, and it says it read short — partial,
// naming the store and why — never that nothing scored above the floor.
func TestAReadStoppedByStaleVectorsSaysSoAndNeverClaimsNoMatch(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	staleAhead(t, s, MeaningReadBound, now)
	f := New(s)
	f.SetEmbedder(&fakeEmbedder{basis: "fake/v1"})
	res, err := f.Recall(context.Background(), Query{Text: "port", Limit: 1, Now: now, Stores: []string{"experiences"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meaning.Status != StatusPartial || !strings.Contains(res.Meaning.Detail, "experiences") || !strings.Contains(res.Meaning.Detail, "changed") {
		t.Fatalf("a meaning read stopped by its bound must say so, never found_nothing: %s %q", res.Meaning.Status, res.Meaning.Detail)
	}
	for _, h := range res.Hits {
		if h.Match == MatchMeaning || h.Match == MatchBoth {
			t.Fatalf("a stale row was offered by meaning: %+v", h)
		}
	}
}

// WHAT IS SET ASIDE IS SET ASIDE AS THE TEXT IT WAS. A memory the source
// could not embed is not asked again while the source has not started
// anew. The fault was that text's own, so when the memory's text changes
// it is a text the source has not been asked for, and it is asked.
func TestAMemorySetAsideIsAskedAgainWhenItsTextChanges(t *testing.T) {
	f, src := asideStore(t)
	ctx := context.Background()
	src.fails = map[string]error{"lantern": textFault{why: "the source answered with an error in place of a vector"}}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.SetAside != 1 || rep.Embedded != 3 {
		t.Fatalf("rig: the newest memory is set aside and the rest land: %+v %v", rep, err)
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 0 || src.askedFor("lantern") != 1 {
		t.Fatalf("rig: a pass later it is not asked again: %+v %v, asked %d times", rep, err, src.askedFor("lantern"))
	}
	if _, err := f.st.DB().Exec(`UPDATE experiences SET content = 'the fourth memory, of a tide at night' WHERE id = 'e4'`); err != nil {
		t.Fatal(err)
	}
	rep, err := f.Backfill(ctx, 0)
	if n, _ := f.SetAside(); err != nil || rep.Embedded != 1 || remaining(rep) != 0 || n != 0 {
		t.Fatalf("the memory's new text is asked for and lands: %+v %v, %d still set aside", rep, err, n)
	}
	if src.askedFor("lantern") != 1 || src.askedFor("tide at night") != 1 {
		t.Fatalf("the old text was asked %d times and the new one %d; want once each", src.askedFor("lantern"), src.askedFor("tide at night"))
	}
}
