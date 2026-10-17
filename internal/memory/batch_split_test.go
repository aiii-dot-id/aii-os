package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// refusedText is what a source that takes many texts a call says of a
// request it will not take for what is in it.
type refusedText struct{ error }

func (refusedText) OfTheText() bool   { return true }
func (refusedText) EndsThePass() bool { return false }

// batchStore is n experiences, the last the newest, and a source that
// takes four texts a call. The text of the one numbered bad holds a word
// the source refuses.
func batchStore(t *testing.T, n int, bad ...int) (*Facility, *asideSource) {
	t.Helper()
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, n, now.Add(-48*time.Hour))
	isBad := map[int]bool{}
	for _, b := range bad {
		isBad[b] = true
	}
	for i := 1; i <= n; i++ {
		text := "a memory of a harbor"
		if isBad[i] {
			text = "a memory of a lantern"
		}
		addExperience(t, s, fmt.Sprintf("e%03d", i), text, i, now.Add(time.Duration(i-n-1)*time.Hour), false)
	}
	f := New(s)
	src := &asideSource{basis: "org.example.vectors/abababababab/f1", batch: 4, epoch: 1,
		fails: map[string]error{"lantern": refusedText{errors.New("the request holds a text past the model's limit")}}}
	f.SetEmbedder(src)
	return f, src
}

// A BATCH A SOURCE REFUSES FOR ONE OF ITS TEXTS DOES NOT STOP THE RECORD. A
// provider takes many texts a call and refuses the whole call for one of
// them. The pass stopped at that batch, and so did every pass after it:
// nothing older was embedded, for as long as the text stood in the record.
//
// Ten memories, four a call, and the seventh is one the source refuses.
// One pass embeds the nine it takes and sets the seventh aside; nothing
// is left waiting but what is set aside; and the next pass asks the
// source for nothing.
func TestABatchRefusedForOneTextIsAskedAgainOneTextAtATime(t *testing.T) {
	f, src := batchStore(t, 10, 7)
	rep, err := f.Backfill(context.Background(), 64)
	if err != nil {
		t.Fatalf("one text a source refuses stopped the pass: %v", err)
	}
	if rep.Embedded != 9 || rep.SetAside != 1 {
		t.Fatalf("ten memories and one the source refuses: %d embedded, %d set aside; want nine and one (%s)", rep.Embedded, rep.SetAside, rep.Line())
	}
	if got := remaining(rep); got != 1 {
		t.Fatalf("what is still without a vector is the one set aside, and the pass says %d", got)
	}
	asked := len(src.asked)
	if rep, err = f.Backfill(context.Background(), 64); err != nil || rep.Embedded != 0 || len(src.asked) != asked {
		t.Fatalf("the next pass asked the source again for what it set aside: %+v %v (%d calls' texts, then %d)", rep, err, asked, len(src.asked))
	}
}

// AND A SOURCE THAT REFUSES EVERYTHING HAS SET NOTHING ASIDE. When every
// text of a refused batch is refused alone, in a pass that has embedded
// nothing, the fault is the request's (a model the provider does not
// serve, a form it does not take) and not a text's. Setting each aside
// would empty the layer one memory at a time and call it the memories'
// fault. The pass ends with what the source said.
func TestASourceThatRefusesEveryTextAloneHasSetNothingAside(t *testing.T) {
	f, src := batchStore(t, 6, 1, 2, 3, 4, 5, 6)
	rep, err := f.Backfill(context.Background(), 64)
	if err == nil || !strings.Contains(err.Error(), "refused every text of a batch when asked for each alone") {
		t.Fatalf("a source that refuses every text must end the pass saying so: %v", err)
	}
	if rep.SetAside != 0 || rep.Embedded != 0 {
		t.Fatalf("nothing is a text's own fault here: %d set aside, %d embedded", rep.SetAside, rep.Embedded)
	}
	// And the next pass asks again: nothing was put out of its reach.
	asked := len(src.asked)
	if _, err = f.Backfill(context.Background(), 64); err == nil || len(src.asked) == asked {
		t.Fatalf("the next pass did not ask again: %v", err)
	}
}

// A WHOLE BATCH OF REFUSED TEXTS, AFTER OTHERS WERE EMBEDDED, IS THE TEXTS'.
// Once the source has taken texts in this pass, a batch refused text by
// text is that many texts it will not take, and each is set aside.
func TestABatchOfRefusedTextsAfterOthersWereEmbeddedIsSetAside(t *testing.T) {
	// The newest four are asked first and are taken; the four before them
	// are each refused.
	f, _ := batchStore(t, 8, 1, 2, 3, 4)
	rep, err := f.Backfill(context.Background(), 64)
	if err != nil || rep.Embedded != 4 || rep.SetAside != 4 {
		t.Fatalf("four taken and four refused: %d embedded, %d set aside, %v", rep.Embedded, rep.SetAside, err)
	}
}
