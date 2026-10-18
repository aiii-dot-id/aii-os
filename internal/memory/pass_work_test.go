package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// workSource is a source on this machine whose every call takes a stated
// time by the pass's own clock: asked one text a call, given its pass in
// time, and able to ask the pass to stop.
type workSource struct {
	asideSource
	clock   *time.Time
	perCall time.Duration
	work    time.Duration
	stopAt  int // MayContinue says no once this many texts were asked; 0 never
}

func (s *workSource) PassWork() time.Duration { return s.work }
func (s *workSource) MayContinue() bool       { return s.stopAt == 0 || len(s.asked) < s.stopAt }
func (s *workSource) Embed(ctx context.Context, kind Kind, inputs []string) (string, [][]float32, error) {
	*s.clock = s.clock.Add(s.perCall)
	return s.asideSource.Embed(ctx, kind, inputs)
}

// workStore is n experiences and a source whose calls take perCall each.
func workStore(t *testing.T, n int, perCall, work time.Duration) (*Facility, *workSource) {
	t.Helper()
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, n, now.Add(-48*time.Hour))
	for i := 1; i <= n; i++ {
		// The same few words in every row: the fake's table of words is
		// shared by the package's tests and holds a bounded number.
		addExperience(t, s, fmt.Sprintf("e%03d", i), "a memory of a harbor", i, now.Add(time.Duration(i-n-1)*time.Hour), false)
	}
	f := New(s)
	clock := now
	f.clock = func() time.Time { return clock }
	src := &workSource{asideSource: asideSource{basis: "org.example.vectors/abababababab/f1", batch: 1, epoch: 1}, clock: &clock, perCall: perCall, work: work}
	f.SetEmbedder(src)
	return f, src
}

// A SOURCE ON THIS MACHINE IS GIVEN ITS PASS IN TIME. Every source had 256
// memories a pass. For a provider that bounds what is sent. For a source
// on this machine it bounded nothing about the machine: 256 short notes
// are seconds of its processors and 256 long ones are minutes.
//
// Here each call takes fifty seconds and the pass is 120 seconds of work.
// The first pass makes three calls: the third crosses the bound and is
// finished, and no fourth is begun. It says the time it worked and that
// its time was up. The count given to the pass, one memory, does not bound
// it. The next pass goes on from there.
func TestASourceOnThisMachineIsGivenItsPassInTimeAndNotInMemories(t *testing.T) {
	f, src := workStore(t, 10, 50*time.Second, 120*time.Second)
	rep, err := f.Backfill(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded != 3 || len(src.asked) != 3 || remaining(rep) != 7 {
		t.Fatalf("a pass of 120 s with calls of 50 s: %d embedded in %d calls, %d remaining; want three calls, the third finished past the bound, and seven left", rep.Embedded, len(src.asked), remaining(rep))
	}
	if !rep.TimeUp || rep.Worked != 150*time.Second {
		t.Fatalf("the pass must say its time was up and what it worked: up %v, worked %s; want 2m30s", rep.TimeUp, rep.Worked)
	}
	if !strings.Contains(rep.Line(), "the source worked 2m30s, which is its pass") {
		t.Fatalf("the receipt does not say the pass's time was up: %s", rep.Line())
	}
	rep, err = f.Backfill(context.Background(), 1)
	if err != nil || rep.Embedded != 3 || remaining(rep) != 4 {
		t.Fatalf("the next pass goes on: %+v %v", rep, err)
	}

	// Calls that add up to the bound exactly have used it: no call more.
	f, src = workStore(t, 10, 60*time.Second, 120*time.Second)
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 2 || len(src.asked) != 2 || !rep.TimeUp {
		t.Fatalf("two calls of sixty seconds in a pass of 120: %d embedded in %d calls, up %v, %v; want two and the time up", rep.Embedded, len(src.asked), rep.TimeUp, err)
	}
}

// THE TIME IS THE CALLS' OWN, AND A PASS THAT COVERS THE RECORD ENDS THERE.
// A pass with time to spare stops when nothing is missing, and does not
// say its time was up. A pass the source asks to stop has worked only what
// its calls took: it stops early, says so, and says nothing of time. And a
// pass reads further than one round of rows when its time allows.
func TestAPassInTimeEndsWhenTheRecordIsCoveredAndCountsOnlyItsCalls(t *testing.T) {
	f, _ := workStore(t, 4, time.Second, 120*time.Second)
	rep, err := f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 4 || remaining(rep) != 0 || rep.TimeUp || rep.Worked != 4*time.Second {
		t.Fatalf("four calls of a second in a pass of 120: %+v %v; want all four, nothing remaining, the time not up, four seconds worked", rep, err)
	}

	f, src := workStore(t, 6, 10*time.Second, 120*time.Second)
	src.stopAt = 2
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != 2 || !rep.Yielded || rep.TimeUp || rep.Worked != 20*time.Second {
		t.Fatalf("a pass asked to stop after two calls of ten seconds: %+v %v; want two embedded, yielded, the time not up, twenty seconds worked", rep, err)
	}

	// More rows than one round reads at a time, and time for all of them.
	f, _ = workStore(t, backfillRound+20, time.Millisecond, time.Hour)
	rep, err = f.Backfill(context.Background(), 0)
	if err != nil || rep.Embedded != backfillRound+20 || remaining(rep) != 0 || rep.TimeUp {
		t.Fatalf("a pass with time for every row stopped at a round: %d of %d embedded, %d remaining, %v", rep.Embedded, backfillRound+20, remaining(rep), err)
	}
}

// A SOURCE THAT STATES NO TIME IS BOUNDED BY THE COUNT, AS BEFORE. A
// provider's pass is its count of memories, however little time they take.
func TestASourceThatStatesNoTimeIsBoundedByTheCount(t *testing.T) {
	f, src := workStore(t, 10, time.Millisecond, 0)
	rep, err := f.Backfill(context.Background(), 4)
	if err != nil || rep.Embedded != 4 || len(src.asked) != 4 || remaining(rep) != 6 || rep.TimeUp {
		t.Fatalf("a pass of four memories: %+v %v, %d asked", rep, err, len(src.asked))
	}
}
