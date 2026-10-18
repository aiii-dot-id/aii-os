package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// textFault is a source's failure about one text, as a source on this
// machine reports it.
type textFault struct {
	why  string
	ends bool
}

func (f textFault) Error() string     { return f.why }
func (textFault) OfTheText() bool     { return true }
func (f textFault) EndsThePass() bool { return f.ends }

// asideSource is a source on this machine for the tests of what is set
// aside: one text a call unless batch says more, failing for the texts
// that carry a word of fails, the way that word's entry says.
type asideSource struct {
	basis string
	batch int
	epoch any
	fails map[string]error
	asked []string
}

func (s *asideSource) Basis() (string, error) { return s.basis, nil }
func (s *asideSource) Floor() float64         { return 0.3 }
func (s *asideSource) Batch() int             { return s.batch }
func (s *asideSource) Epoch() any             { return s.epoch }
func (s *asideSource) Embed(_ context.Context, _ Kind, inputs []string) (string, [][]float32, error) {
	s.asked = append(s.asked, inputs...)
	out := make([][]float32, len(inputs))
	for i, text := range inputs {
		for word, err := range s.fails {
			if strings.Contains(text, word) {
				return s.basis, nil, err
			}
		}
		out[i] = fakeVector(text)
	}
	return s.basis, out, nil
}

// askedFor counts the calls that carried a word.
func (s *asideSource) askedFor(word string) int {
	n := 0
	for _, text := range s.asked {
		if strings.Contains(text, word) {
			n++
		}
	}
	return n
}

// asideStore is four experiences, the fourth the newest, and a source
// asked one text a call.
func asideStore(t *testing.T) (*Facility, *asideSource) {
	t.Helper()
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 4, now.Add(-48*time.Hour))
	for i, text := range []string{"the first memory, of a harbor", "the second memory, of a ship", "the third memory, of a tide", "the fourth memory, of a lantern"} {
		addExperience(t, s, fmt.Sprintf("e%d", i+1), text, i+1, now.Add(time.Duration(i-10)*time.Hour), false)
	}
	f := New(s)
	src := &asideSource{basis: "org.example.vectors/abababababab/f1", batch: 1, epoch: 1}
	f.SetEmbedder(src)
	return f, src
}

func remaining(rep BackfillReport) int64 {
	var n int64
	for _, s := range rep.Stores {
		n += s.Remaining
	}
	return n
}

// ONE MEMORY THE SOURCE CANNOT EMBED DOES NOT HOLD BACK THE REST. A pass
// takes the newest missing rows first and used to stop at the first
// failure, so a memory whose text the source answered with an error was
// the first row of every pass after, failed every time, and nothing older
// than it was ever embedded. Now the source says the failure was that
// text's own; the memory is set aside, the pass goes on, the report and
// the facility count it with the reason, and it is not asked again while
// the source has not started anew.
func TestAMemoryTheSourceCannotEmbedIsSetAsideAndThePassGoesOn(t *testing.T) {
	f, src := asideStore(t)
	src.fails = map[string]error{"fourth": textFault{why: "the source answered with an error in place of a vector"}}
	rep, err := f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 3 || rep.SetAside != 1 || remaining(rep) != 1 {
		t.Fatalf("the newest fails and the three behind it must still land: %+v %v", rep, err)
	}
	if !strings.Contains(rep.Line(), "1 set aside") {
		t.Fatalf("the receipt does not say a memory was set aside: %s", rep.Line())
	}
	if n, why := f.SetAside(); n != 1 || !strings.Contains(why, "answered with an error") {
		t.Fatalf("the facility must count what is set aside, with the reason: %d %q", n, why)
	}
	// The next passes do not ask for it again, and report nothing new.
	for i := 0; i < 2; i++ {
		rep, err = f.Backfill(context.Background(), 0)
		if err != nil || rep.Embedded != 0 || rep.SetAside != 0 || remaining(rep) != 1 {
			t.Fatalf("a later pass with only the set-aside memory missing: %+v %v", rep, err)
		}
	}
	if src.askedFor("fourth") != 1 {
		t.Fatalf("the set-aside memory was asked %d times, want once", src.askedFor("fourth"))
	}
	// The source starts anew, and can embed it now: it is asked again.
	src.epoch, src.fails = 2, nil
	rep, err = f.Backfill(context.Background(), 0)
	if n, _ := f.SetAside(); err != nil || rep.Embedded != 1 || remaining(rep) != 0 || n != 0 {
		t.Fatalf("after a new start the memory is asked again and lands: %+v %v, %d still aside", rep, err, n)
	}
}

// A MEMORY WHOSE CALL RAN PAST ITS TIME IS SET ASIDE TOO, AND THAT PASS
// STOPS. The source was stopped over it and is starting again, so nothing
// more is asked now; but without setting the memory aside the next pass
// would begin with it again, and cost a stop of the source every pass for
// good. The next pass skips it and makes the rest.
func TestAMemoryThatStoppedTheSourceIsSetAsideAndThatPassStops(t *testing.T) {
	f, src := asideStore(t)
	src.fails = map[string]error{"fourth": textFault{why: "it did not answer in time, was stopped, and starts again", ends: true}}
	rep, err := f.Backfill(context.Background(), 0)
	var fault TextFault
	if !errors.As(err, &fault) || rep.Embedded != 0 || rep.SetAside != 1 || len(src.asked) != 1 {
		t.Fatalf("the pass must stop at the memory that stopped the source, having set it aside: %+v %v, %d calls", rep, err, len(src.asked))
	}
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 3 || src.askedFor("fourth") != 1 {
		t.Fatalf("the next pass must skip it and make the rest: %+v %v", rep, err)
	}
}

// A FAILURE THAT IS NOT ABOUT A MEMORY SETS NOTHING ASIDE. The source is
// not running, may not be asked now, was replaced: none of that is the
// memory's doing. The pass stops as it always did, nothing is set aside,
// and the next pass begins with the same memory.
func TestAFailureOfTheSourceSetsNothingAside(t *testing.T) {
	f, src := asideStore(t)
	down := errors.New("the source is not running")
	src.fails = map[string]error{"fourth": down}
	for i := 1; i <= 2; i++ {
		rep, err := f.Backfill(context.Background(), 0)
		if n, _ := f.SetAside(); !errors.Is(err, down) || rep.Embedded != 0 || rep.SetAside != 0 || n != 0 {
			t.Fatalf("pass %d: a failure of the source must stop the pass and set nothing aside: %+v %v, %d aside", i, rep, err, n)
		}
		if src.askedFor("fourth") != i {
			t.Fatalf("pass %d: the same memory is asked first again, got %d asks", i, src.askedFor("fourth"))
		}
	}
	// A fault that says it is not the text's own is the source's too.
	g, gsrc := asideStore(t)
	gsrc.fails = map[string]error{"fourth": notOfTheText{}}
	rep, err := g.Backfill(context.Background(), 0)
	if n, _ := g.SetAside(); err == nil || rep.SetAside != 0 || n != 0 {
		t.Fatalf("a fault that is not the text's own was set aside: %+v %v", rep, err)
	}
}

type notOfTheText struct{}

func (notOfTheText) Error() string     { return "not this text's" }
func (notOfTheText) OfTheText() bool   { return false }
func (notOfTheText) EndsThePass() bool { return false }

// A BATCH THAT FAILS DOES NOT SAY WHICH OF ITS TEXTS IT WAS. A batch of
// several that fails for a reason that is not a text's stops the pass and
// sets nothing aside. Where the source says the fault is in what it was
// handed, the batch's texts are asked again one at a time, and only a
// text refused alone is set aside (batch_split_test.go; the first rule,
// that such a batch stopped the pass whatever the error claimed, left a
// provider's record unread behind one long text).
func TestAFailedBatchSetsNothingAsideUntilItsTextsAreAskedAlone(t *testing.T) {
	f, src := asideStore(t)
	src.batch = 64
	// The oldest of the four is the one the call fails on, so that a pass
	// which asked the texts alone would have embedded the other three.
	src.fails = map[string]error{"first": notOfTheText{}}
	rep, err := f.Backfill(context.Background(), 0)
	if n, _ := f.SetAside(); err == nil || rep.Embedded != 0 || rep.SetAside != 0 || n != 0 {
		t.Fatalf("a batch that fails for a reason that is not a text's must stop the pass and set nothing aside: %+v %v, %d aside", rep, err, n)
	}
	src.fails = map[string]error{"fourth": textFault{why: "one of these"}}
	rep, err = f.Backfill(context.Background(), 0)
	if n, _ := f.SetAside(); err != nil || rep.Embedded != 3 || rep.SetAside != 1 || n != 1 {
		t.Fatalf("a batch refused for one of its texts: %+v %v, %d aside; want the three it takes embedded and the fourth set aside", rep, err, n)
	}
}

// WHAT IS SET ASIDE DOES NOT USE UP A PASS, AND DOES NOT OUTLIVE ITS
// BASIS. A set-aside memory is still missing a vector, so it is still
// among the newest missing rows: a pass whose budget is two, with the two
// newest set aside, must still reach the two behind them. A call that set
// a memory aside counts against the pass it was made in. And under a new
// basis, which is another model, everything is asked again.
func TestWhatIsSetAsideDoesNotUseUpAPassAndDoesNotOutliveItsBasis(t *testing.T) {
	f, src := asideStore(t)
	// A row in the store after this one, which a pass reaches only with
	// budget left.
	addBelief(t, f.st, "b1", "Tides govern the harbor", 3, 3)
	src.fails = map[string]error{
		"fourth": textFault{why: "an error in place of a vector"},
		"third":  textFault{why: "an error in place of a vector"},
	}
	rep, err := f.Backfill(context.Background(), 3)
	if err != nil || rep.SetAside != 2 || rep.Embedded != 1 || len(src.asked) != 3 {
		t.Fatalf("a budget of three is three calls, two of them set aside, and none left for the next store: %+v %v, %d calls", rep, err, len(src.asked))
	}
	rep, err = f.Backfill(context.Background(), 2)
	if err != nil || rep.Embedded != 2 || rep.SetAside != 0 || remaining(rep) != 2 {
		t.Fatalf("with the two newest set aside, a pass of two must still reach the row behind them and the next store: %+v %v", rep, err)
	}
	if src.askedFor("fourth") != 1 || src.askedFor("third") != 1 {
		t.Fatalf("set-aside memories were asked again: %v", src.asked)
	}
	src.basis, src.fails = "org.example.vectors/cdcdcdcdcdcd/f1", nil
	rep, err = f.Backfill(context.Background(), 0)
	if n, _ := f.SetAside(); err != nil || rep.Embedded != 5 || n != 0 {
		t.Fatalf("under a new basis every memory is asked again: %+v %v, %d aside", rep, err, n)
	}
}
