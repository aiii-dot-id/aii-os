package memory

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// vector_length_test.go — a vector is held to its length as it is to its
// basis. A provider's basis is its
// name and its model's name, and nothing held the kept vectors to one
// number of dimensions: a row of another length made the cosine an error,
// the error failed the meaning read of that row's whole store at every
// recall, and no pass removed the row.

// lengthSource is a source that answers one basis at the length a test
// names, and at two lengths in one answer when told to. turns are the
// lengths of its next answers, one a call, before it answers at dims again.
type lengthSource struct {
	dims  int
	mixed bool
	turns []int
}

func (*lengthSource) Basis() (string, error) { return "Local/embed", nil }
func (*lengthSource) Floor() float64         { return ProviderFloor }

func (s *lengthSource) Embed(_ context.Context, _ Kind, inputs []string) (string, [][]float32, error) {
	dims := s.dims
	if len(s.turns) > 0 {
		dims, s.turns = s.turns[0], s.turns[1:]
	}
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = fakeVector(in)[:dims]
		if s.mixed && i > 0 {
			out[i] = out[i][:dims/2]
		}
	}
	return "Local/embed", out, nil
}

func lengthOf(t *testing.T, s *store.Store, storeName, id string) int {
	t.Helper()
	var dims int
	if err := s.DB().QueryRow(`SELECT dims FROM memory_vectors WHERE store = ? AND id = ?`, storeName, id).Scan(&dims); err != nil {
		t.Fatalf("%s/%s holds no vector: %v", storeName, id, err)
	}
	return dims
}

// lengths is how many vectors the store holds at each length.
func lengths(t *testing.T, s *store.Store) map[int]int {
	t.Helper()
	rows, err := s.DB().Query(`SELECT dims, COUNT(*) FROM memory_vectors GROUP BY dims`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var dims, n int
		if err := rows.Scan(&dims, &n); err != nil {
			t.Fatal(err)
		}
		out[dims] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// ONE ROW OF ANOTHER LENGTH DOES NOT FAIL ITS STORE'S MEANING READ, AND THE
// NEXT PASS MAKES ITS VECTOR AGAIN. Three memories are embedded and one
// vector is then replaced by one of three dimensions under the same basis
// and the same text's hash, as a server that answered once at another
// length would leave it. The recall of a word that is in no memory still
// finds the experience and the belief that mean it, with no warning; and
// to the pass after it that row is one without a vector: it is embedded
// again, and the vector made for it replaces the one of another length.
func TestAVectorOfAnotherLengthIsNotReadAndIsMadeAgain(t *testing.T) {
	f, _, now := meaningStore(t)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}
	if err := f.st.PutMemoryVectors([]store.MemoryVector{{
		Store: "experiences", ID: "e2", Basis: "fake/v1", ContentSHA: textSHA("a quiet day of routine observations"),
		Dims: 3, Scale: 1, Q: []byte{1, 2, 3},
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := f.Recall(ctx, Query{Text: "port", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); !slices.Contains(got, "experiences/e1") || !slices.Contains(got, "beliefs/b1") || res.Meaning.Status != StatusFound {
		t.Fatalf("ONE VECTOR OF ANOTHER LENGTH FAILED THE MEANING READ OF ITS STORE: hits %v, meaning %+v", got, res.Meaning)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("a vector of another length is not read, and is no failure of the read: %v", res.Warnings)
	}

	rep, err := f.Backfill(ctx, 0)
	if err != nil || rep.Embedded != 1 {
		t.Fatalf("the pass must embed again the one row whose vector is of another length: %+v %v", rep, err)
	}
	if got := lengthOf(t, f.st, "experiences", "e2"); got != fakeDims {
		t.Fatalf("e2's vector is %d dimensions after the pass, want the source's %d", got, fakeDims)
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 0 {
		t.Fatalf("a record held to one length embeds nothing: %+v %v", rep, err)
	}
}

// A SOURCE THAT COMES TO ANSWER ITS BASIS AT ANOTHER LENGTH HAS EVERY
// VECTOR MADE AGAIN. The server behind one model name is changed: 256
// dimensions were kept and 128 are answered now. A recall's cue is
// compared with none of the kept vectors, and its words still answer;
// that cue is how the facility learns the length, so coverage no longer
// counts the three as held, and the next pass embeds them again, although
// no row was without a vector; each new vector replaces the old one of its
// row, and nothing is deleted for its length.
func TestASourceThatChangesLengthUnderOneBasisHasItsVectorsMadeAgain(t *testing.T) {
	f, _, now := meaningStore(t)
	src := &lengthSource{dims: fakeDims}
	f.SetEmbedder(src)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}

	src.dims = fakeDims / 2
	res, err := f.Recall(ctx, Query{Text: "harbor", Now: now})
	if err != nil || len(res.Hits) != 2 {
		t.Fatalf("the words must still answer: %v %v", hitIDs(res.Hits), err)
	}
	if len(res.Warnings) != 0 || res.Meaning.Status != StatusFoundNothing {
		t.Fatalf("a cue is compared with no vector of another length, and that is no failure of the read: %+v %v", res.Meaning, res.Warnings)
	}

	// Both reads of the coverage: by id (the Memory card) and by the
	// row's present text (the measure read).
	for name, read := range map[string]func(context.Context) (string, []Coverage, string, error){"held": f.HeldCoverage, "usable": f.UsableCoverage} {
		_, cov, _, err := read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cov {
			if c.Embedded != 0 {
				t.Fatalf("%s coverage counts as held a vector no cue of the source can be compared with: %+v", name, c)
			}
		}
	}

	rep, err := f.Backfill(ctx, 0)
	if err != nil || rep.Embedded != 3 {
		t.Fatalf("THE PASS AFTER A CUE OF ANOTHER LENGTH LEFT THE OLD VECTORS IN PLACE: %+v %v", rep, err)
	}
	for _, row := range [][2]string{{"experiences", "e1"}, {"experiences", "e2"}, {"beliefs", "b1"}} {
		if got := lengthOf(t, f.st, row[0], row[1]); got != fakeDims/2 {
			t.Fatalf("%s/%s is %d dimensions, want %d", row[0], row[1], got, fakeDims/2)
		}
	}
	res, err = f.Recall(ctx, Query{Text: "port", Now: now})
	if err != nil || res.Meaning.Status != StatusFound || !slices.Contains(hitIDs(res.Hits), "experiences/e1") {
		t.Fatalf("after the pass the meaning layer reads the record again: %v %+v %v", hitIDs(res.Hits), res.Meaning, err)
	}
}

// A pass that learns of the change from its own answer stops there and
// says so, writes nothing of the new length beside the old, and the next
// pass starts over at the new one.
func TestAPassWhoseAnswerIsOfAnotherLengthStopsAndTheNextStartsOver(t *testing.T) {
	f, _, now := meaningStore(t)
	src := &lengthSource{dims: fakeDims}
	f.SetEmbedder(src)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}
	src.dims = fakeDims / 2
	addExperience(t, f.st, "e3", "the tide turned before the ship left", 3, now, false)

	rep, err := f.Backfill(ctx, 0)
	if err == nil || !strings.Contains(err.Error(), "the length of the source's vectors changed (256 → 128 dimensions)") || rep.Embedded != 0 {
		t.Fatalf("a pass answered at another length than its vectors are held to must stop and say so: %+v %v", rep, err)
	}
	if n, _ := f.st.MemoryVectorCount("experiences", "Local/embed"); n != 2 {
		t.Fatalf("the stopped pass wrote a vector of the new length beside the old: %d vectors of experiences", n)
	}
	rep, err = f.Backfill(ctx, 0)
	if err != nil || rep.Embedded != 4 {
		t.Fatalf("the next pass starts over at the new length: %+v %v", rep, err)
	}
	if got := lengths(t, f.st); got[fakeDims/2] != 4 || len(got) != 1 {
		t.Fatalf("each row's new vector replaces its old one: %v by length", got)
	}
}

// ONE STRAY ANSWER OF ANOTHER LENGTH COSTS A REQUEST AND NO VECTOR. A cue
// comes back once at 128 dimensions from a source that answers at 256
// before and after. The pass that follows asks for the rows it now takes
// to be without a vector, is answered at 256, stops and says so, and has
// changed nothing: a vector is replaced only by an answer of the length the
// pass holds, made for its own row. The pass after that finds the record
// whole.
func TestOneStrayAnswerOfAnotherLengthDropsNoVector(t *testing.T) {
	f, _, now := meaningStore(t)
	src := &lengthSource{dims: fakeDims}
	f.SetEmbedder(src)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}
	src.dims = fakeDims / 2
	if _, err := f.Recall(ctx, Query{Text: "harbor", Now: now}); err != nil {
		t.Fatal(err)
	}
	src.dims = fakeDims

	rep, err := f.Backfill(ctx, 0)
	if err == nil || !strings.Contains(err.Error(), "the length of the source's vectors changed (128 → 256 dimensions)") || rep.Embedded != 0 {
		t.Fatalf("a pass answered at another length than the cue's must stop, say so and keep nothing: %+v %v", rep, err)
	}
	for _, row := range [][2]string{{"experiences", "e1"}, {"experiences", "e2"}, {"beliefs", "b1"}} {
		if got := lengthOf(t, f.st, row[0], row[1]); got != fakeDims {
			t.Fatalf("ONE STRAY CUE COST %s/%s ITS VECTOR: %d dimensions, want %d", row[0], row[1], got, fakeDims)
		}
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 0 {
		t.Fatalf("the next pass finds the record whole: %+v %v", rep, err)
	}
	res, err := f.Recall(ctx, Query{Text: "port", Now: now})
	if err != nil || res.Meaning.Status != StatusFound || !slices.Contains(hitIDs(res.Hits), "experiences/e1") {
		t.Fatalf("the meaning layer reads the record as before: %v %+v %v", hitIDs(res.Hits), res.Meaning, err)
	}
}

// THE FIRST ANSWER AFTER A START COSTS NO VECTOR BUT ITS OWN. The length
// the source last answered at is kept in memory, so a facility that has
// just started holds none. Its first pass is answered once at 128
// dimensions by a source that answers at 256 before and after, for the one
// memory that had no vector. With no length held that answer was taken for
// the source's, and every vector of the record's own length was dropped on
// its word: one answer after a restart cost the whole store's vectors.
// Now the three kept vectors stand; the
// answer is kept for its own row; the next pass, answered at 256, stops and
// says so; and the pass after that makes the one odd vector again.
func TestTheFirstAnswerAfterAStartCostsNoVectorButItsOwn(t *testing.T) {
	f, _, now := meaningStore(t)
	src := &lengthSource{dims: fakeDims}
	f.SetEmbedder(src)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}

	// The process starts again: a facility over the same store, which has
	// heard no answer yet.
	started := New(f.st)
	started.SetEmbedder(src)
	addExperience(t, f.st, "e3", "the tide turned before the ship left", 3, now, false)
	src.dims = fakeDims / 2
	rep, err := started.Backfill(ctx, 0)
	src.dims = fakeDims
	if got := lengths(t, f.st); got[fakeDims] != 3 {
		t.Fatalf("ONE ANSWER AFTER A START COST THE RECORD ITS VECTORS: %v by length, want the three of %d dimensions kept (%+v %v)", got, fakeDims, rep, err)
	}
	if err != nil || rep.Embedded != 1 || lengthOf(t, f.st, "experiences", "e3") != fakeDims/2 {
		t.Fatalf("the answer is kept for the one row it was asked for: %+v %v", rep, err)
	}

	rep, err = started.Backfill(ctx, 0)
	if err == nil || !strings.Contains(err.Error(), "the length of the source's vectors changed (128 → 256 dimensions)") || rep.Embedded != 0 {
		t.Fatalf("the pass after it, answered at the record's own length, must stop and say so: %+v %v", rep, err)
	}
	if got := lengths(t, f.st); got[fakeDims] != 3 || got[fakeDims/2] != 1 {
		t.Fatalf("a stopped pass changes no vector: %v by length", got)
	}
	if rep, err := started.Backfill(ctx, 0); err != nil || rep.Embedded != 1 {
		t.Fatalf("the next pass makes the one odd vector again: %+v %v", rep, err)
	}
	if got := lengths(t, f.st); got[fakeDims] != 4 || len(got) != 1 {
		t.Fatalf("the record is whole and of one length: %v by length", got)
	}
	res, err := started.Recall(ctx, Query{Text: "port", Now: now})
	if err != nil || res.Meaning.Status != StatusFound || !slices.Contains(hitIDs(res.Hits), "experiences/e1") {
		t.Fatalf("the meaning layer reads the record as before: %v %+v %v", hitIDs(res.Hits), res.Meaning, err)
	}
}

// TWO ANSWERS RUNNING AT ANOTHER LENGTH COST THE ROWS THEY WERE ASKED FOR,
// AND NO OTHER. A source that answers at two lengths by turns answers a cue
// and then the pass's first call at 128 dimensions, and at 256 again from
// its next. The first draft took two answers that agreed for a change of
// the source's length and dropped every vector of the other: here the
// belief's, which neither answer was asked for. Now the two experiences the
// call was asked for hold what it answered, the call for the belief stops
// the pass, and the belief's vector stands; the next pass, answered at 256,
// makes the two again.
func TestTwoAnswersRunningAtAnotherLengthCostOnlyTheRowsTheyWereAskedFor(t *testing.T) {
	f, _, now := meaningStore(t)
	src := &lengthSource{dims: fakeDims}
	f.SetEmbedder(src)
	ctx := context.Background()
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 3 {
		t.Fatalf("rig: backfill: %+v %v", rep, err)
	}
	src.turns = []int{fakeDims / 2, fakeDims / 2}
	if _, err := f.Recall(ctx, Query{Text: "harbor", Now: now}); err != nil {
		t.Fatal(err)
	}

	rep, err := f.Backfill(ctx, 0)
	if err == nil || !strings.Contains(err.Error(), "the length of the source's vectors changed (128 → 256 dimensions)") || rep.Embedded != 2 {
		t.Fatalf("the call answered at the length held is kept, and the next, of another, stops the pass: %+v %v", rep, err)
	}
	if got := lengths(t, f.st); got[fakeDims] != 1 || got[fakeDims/2] != 2 || lengthOf(t, f.st, "beliefs", "b1") != fakeDims {
		t.Fatalf("TWO ANSWERS COST A VECTOR NEITHER WAS ASKED FOR: %v by length, want the belief's of %d dimensions kept", got, fakeDims)
	}
	if rep, err := f.Backfill(ctx, 0); err != nil || rep.Embedded != 2 {
		t.Fatalf("the next pass makes the two again: %+v %v", rep, err)
	}
	if got := lengths(t, f.st); got[fakeDims] != 3 || len(got) != 1 {
		t.Fatalf("the record is whole and of one length: %v by length", got)
	}
}

// An answer whose vectors are of two lengths is refused whole: neither
// length can be the basis's, and nothing of it is kept.
func TestAnAnswerOfTwoLengthsIsRefusedWhole(t *testing.T) {
	f, _, _ := meaningStore(t)
	f.SetEmbedder(&lengthSource{dims: fakeDims, mixed: true})
	rep, err := f.Backfill(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "vectors of 256 and 128 dimensions in one answer") || rep.Embedded != 0 {
		t.Fatalf("an answer of two lengths must stop the pass: %+v %v", rep, err)
	}
	if bases, err := f.st.MemoryVectorBases(context.Background()); err != nil || len(bases) != 0 {
		t.Fatalf("a refused answer left vectors behind: %v %v", bases, err)
	}
}
