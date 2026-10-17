package app

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// oneAtATime is the source's child: one lane, every call recorded as it
// is sent, a call of a held text kept in flight until it is let go, and
// a call of a failing text answered with an error.
type oneAtATime struct {
	lane chan struct{}
	sent chan string // "kind: text", as each call is sent

	mu   sync.Mutex
	held map[string]chan struct{}
	fail map[string]int
}

func newOneAtATime() *oneAtATime {
	return &oneAtATime{lane: make(chan struct{}, 1), sent: make(chan string, 64), held: map[string]chan struct{}{}, fail: map[string]int{}}
}

// hold keeps the next call of text in flight until release is called,
// whatever its deadline says: a long memory on a slow machine, or a call
// stuck past its time.
func (c *oneAtATime) hold(text string) (release func()) {
	ch := make(chan struct{})
	c.mu.Lock()
	c.held[text] = ch
	c.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// failNext answers the next call of text with an error, as an engine
// whose worker died answers the call that finds it dead.
func (c *oneAtATime) failNext(text string) {
	c.mu.Lock()
	c.fail[text]++
	c.mu.Unlock()
}

func (c *oneAtATime) answer(ctx context.Context, kind, text string) (tools.Result, error) {
	if err := awaitTheLane(ctx, c.lane); err != nil {
		return tools.Result{}, err
	}
	defer func() { <-c.lane }()
	c.sent <- kind + ": " + text
	c.mu.Lock()
	release := c.held[text]
	delete(c.held, text)
	failing := c.fail[text] > 0
	if failing {
		c.fail[text]--
	}
	c.mu.Unlock()
	if release != nil {
		computing(release)
	}
	if failing {
		return engineDown(ctx, kind, text)
	}
	return working(ctx, kind, text)
}

// awaitTheLane is the supervisor's lane: a call waits for the one turn
// under its own context, and one whose context ends first is handed
// nothing and told so by the bare context error.
func awaitTheLane(ctx context.Context, lane chan struct{}) error {
	select {
	case lane <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// computing is a call the child is computing; it ends when it is let go.
func computing(release <-chan struct{}) { <-release }

// childFor puts the one-at-a-time child behind a running, checked source.
func childFor(t *testing.T, a *App) (*standInSource, *oneAtATime) {
	t.Helper()
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	child := newOneAtATime()
	s.mu.Lock()
	s.answer = child.answer
	s.mu.Unlock()
	return s, child
}

type askedReply struct {
	vectors [][]float32
	err     error
	took    time.Duration
}

// asking asks the source for one text off the test's goroutine, as recall and
// the backfill each ask it from their own.
func asking(ctx context.Context, source memory.Embedder, kind memory.Kind, text string) <-chan askedReply {
	out := make(chan askedReply, 1)
	go askTheSource(ctx, source, kind, text, out)
	return out
}

// askTheSource is a caller of the source: the frame parked looks for.
func askTheSource(ctx context.Context, source memory.Embedder, kind memory.Kind, text string, out chan<- askedReply) {
	began := time.Now()
	_, v, err := source.Embed(ctx, kind, []string{text})
	out <- askedReply{vectors: v, err: err, took: time.Since(began)}
}

// parked waits until n callers of the source are waiting for their turn:
// blocked before their call was sent, wherever that wait is (the meaning
// layer's gate, or the child's lane), and not the call in flight.
func parked(t *testing.T, n int) {
	t.Helper()
	until := time.Now().Add(20 * time.Second)
	for parkedCallers() < n {
		if time.Now().After(until) {
			t.Fatalf("%d callers of the source were never seen waiting for their turn", n)
		}
		time.Sleep(time.Millisecond)
	}
}

func parkedCallers() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		blocked := strings.Contains(header, "[select") || strings.Contains(header, "[chan ")
		if blocked && strings.Contains(g, ".askTheSource(") && !strings.Contains(g, ".computing(") {
			count++
		}
	}
	return count
}

// next is the next call sent to the child.
func next(t *testing.T, sent <-chan string) string {
	t.Helper()
	select {
	case s := <-sent:
		return s
	case <-time.After(20 * time.Second):
		t.Fatal("no call was sent to the source")
		return ""
	}
}

func replied(t *testing.T, r <-chan askedReply, within time.Duration) askedReply {
	t.Helper()
	select {
	case got := <-r:
		return got
	case <-time.After(within):
		t.Fatalf("a caller of the source had no answer within %v", within)
		return askedReply{}
	}
}

// (a) THE FINDING. A cue arrives while a memory is in flight, and the
// memory takes three times the cue's whole deadline. The cue waits, is
// sent when the memory ends, and gets its vector: its deadline counts from
// its own call.
func TestACueThatArrivesWhileAMemoryIsInFlightGetsItsVectorAfterIt(t *testing.T) {
	a := liveApp(t)
	s, child := childFor(t, a)
	const cueDeadline = 100 * time.Millisecond
	source := pluginSource{a: a, ap: s.ap, cue: cueDeadline, memory: time.Minute}
	ctx := context.Background()

	release := child.hold("a long memory")
	defer release()
	long := asking(ctx, source, memory.KindMemory, "a long memory")
	if got := next(t, child.sent); got != "memory: a long memory" {
		t.Fatalf("first sent: %q", got)
	}
	cue := asking(ctx, source, memory.KindCue, "where is the harbor now")
	parked(t, 1)
	select {
	case r := <-cue:
		t.Fatalf("the cue ended while the memory ahead of it was still in flight: %v", r.err)
	case <-time.After(3 * cueDeadline):
	}
	release()
	r := replied(t, cue, 20*time.Second)
	if r.err != nil || len(r.vectors) != 1 {
		t.Fatalf("the cue gave no vector once the memory ahead of it ended: %v", r.err)
	}
	if r.took <= cueDeadline {
		t.Fatalf("rig: the cue waited %v, not longer than its own deadline of %v", r.took, cueDeadline)
	}
	if got := next(t, child.sent); got != "cue: where is the harbor now" {
		t.Fatalf("second sent: %q", got)
	}
	if m := replied(t, long, 20*time.Second); m.err != nil {
		t.Fatalf("the memory: %v", m.err)
	}
}

// (b) AT MOST ONE MEMORY IS AHEAD OF A CUE. A memory is in flight, two more
// wait, and a cue arrives after them: the cue is the next call sent.
func TestAWaitingCueIsSentBeforeEveryQueuedMemory(t *testing.T) {
	a := liveApp(t)
	s, child := childFor(t, a)
	source := pluginSource{a: a, ap: s.ap, cue: time.Minute, memory: time.Minute}
	ctx := context.Background()

	release := child.hold("memory 1")
	defer release()
	first := asking(ctx, source, memory.KindMemory, "memory 1")
	if got := next(t, child.sent); got != "memory: memory 1" {
		t.Fatalf("first sent: %q", got)
	}
	queued := []<-chan askedReply{asking(ctx, source, memory.KindMemory, "memory 2"), asking(ctx, source, memory.KindMemory, "memory 3")}
	parked(t, 2)
	cue := asking(ctx, source, memory.KindCue, "a cue")
	parked(t, 3)
	release()
	if got := next(t, child.sent); got != "cue: a cue" {
		t.Fatalf("after the memory in flight, %q was sent before the cue that was waiting", got)
	}
	for i := 0; i < 2; i++ {
		if got := next(t, child.sent); !strings.HasPrefix(got, "memory: memory ") {
			t.Fatalf("then sent: %q", got)
		}
	}
	for _, r := range append(queued, first, cue) {
		if got := replied(t, r, 20*time.Second); got.err != nil || len(got.vectors) != 1 {
			t.Fatalf("a caller had no vector: %v", got.err)
		}
	}
}

// (c) A CUE WHOSE CALLER GIVES UP LEAVES AT ONCE, and blocks nobody: the
// memory waiting behind it is the next call sent, and a later cue goes
// straight through.
func TestACueWhoseCallerGivesUpLeavesTheGateAndTheNextMemoryGoes(t *testing.T) {
	a := liveApp(t)
	s, child := childFor(t, a)
	source := pluginSource{a: a, ap: s.ap, cue: time.Minute, memory: time.Minute}
	ctx := context.Background()

	release := child.hold("memory 1")
	defer release()
	first := asking(ctx, source, memory.KindMemory, "memory 1")
	if got := next(t, child.sent); got != "memory: memory 1" {
		t.Fatalf("first sent: %q", got)
	}
	gone, giveUp := context.WithCancel(ctx)
	defer giveUp()
	cue := asking(gone, source, memory.KindCue, "a cue")
	parked(t, 1)
	second := asking(ctx, source, memory.KindMemory, "memory 2")
	parked(t, 2)
	giveUp()
	// Answered while the memory ahead of it is still in flight.
	r := replied(t, cue, 20*time.Second)
	if r.err == nil || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("a cue whose caller gave up must end with its caller's word: %v", r.err)
	}
	release()
	if got := next(t, child.sent); got != "memory: memory 2" {
		t.Fatalf("after the cue left, %q was sent; want the memory that waited", got)
	}
	for _, r := range []<-chan askedReply{first, second} {
		if got := replied(t, r, 20*time.Second); got.err != nil {
			t.Fatalf("a memory: %v", got.err)
		}
	}
	if got := replied(t, asking(ctx, source, memory.KindCue, "a later cue"), 20*time.Second); got.err != nil || len(got.vectors) != 1 {
		t.Fatalf("a later cue: %v", got.err)
	}
	if got := next(t, child.sent); got != "cue: a later cue" {
		t.Fatalf("sent: %q", got)
	}
}

// (d) THE WAIT IS BOUNDED. A memory call stuck past its own deadline and
// the margin makes the cue behind it fail, saying it waited behind the
// source's other call, and not hang; the call is not the cue's own fault,
// and once the stuck call ends the next goes through.
func TestACueBehindACallStuckPastItsDeadlineFailsAndDoesNotHang(t *testing.T) {
	a := liveApp(t)
	s, child := childFor(t, a)
	const memoryDeadline = 100 * time.Millisecond
	source := pluginSource{a: a, ap: s.ap, cue: time.Minute, memory: memoryDeadline}
	ctx := context.Background()

	release := child.hold("a stuck memory")
	defer release()
	stuck := asking(ctx, source, memory.KindMemory, "a stuck memory")
	if got := next(t, child.sent); got != "memory: a stuck memory" {
		t.Fatalf("first sent: %q", got)
	}
	r := replied(t, asking(ctx, source, memory.KindCue, "a cue"), 10*time.Second)
	if r.err == nil || !strings.Contains(r.err.Error(), "waited behind the source's other call") {
		t.Fatalf("a cue behind a stuck call must fail saying so: %v", r.err)
	}
	var call *pluginhost.EmbedCallError
	var fault memory.TextFault
	if errors.As(r.err, &call) || errors.As(r.err, &fault) {
		t.Fatalf("the wait is the source's, not a call's failure nor the text's: %v", r.err)
	}
	release()
	if m := replied(t, stuck, 20*time.Second); m.err != nil {
		t.Fatalf("the stuck memory, let go: %v", m.err)
	}
	if got := replied(t, asking(ctx, source, memory.KindCue, "a later cue"), 20*time.Second); got.err != nil {
		t.Fatalf("a later cue: %v", got.err)
	}
	if got := next(t, child.sent); got != "cue: a later cue" {
		t.Fatalf("sent: %q", got)
	}
}

// (e) THE PROBE THAT JUDGES A CALL IS PART OF ITS TURN. A memory fails, as
// one does when the engine's worker died, while a cue waits: the source's
// own probe and the memory's second ask are sent before the cue, under the
// turn the memory already holds, and nobody waits on a gate it holds.
func TestTheProbeThatJudgesACallIsPartOfThatCallsTurn(t *testing.T) {
	a := liveApp(t)
	s, child := childFor(t, a)
	source := pluginSource{a: a, ap: s.ap, cue: time.Minute, memory: time.Minute}
	ctx := context.Background()

	release := child.hold("a memory")
	defer release()
	child.failNext("a memory")
	m := asking(ctx, source, memory.KindMemory, "a memory")
	if got := next(t, child.sent); got != "memory: a memory" {
		t.Fatalf("first sent: %q", got)
	}
	cue := asking(ctx, source, memory.KindCue, "a cue")
	parked(t, 1)
	release()
	want := []string{"memory: the ship reached the harbor at dusk", "memory: a memory", "cue: a cue"}
	for _, w := range want {
		if got := next(t, child.sent); got != w {
			t.Fatalf("sent %q, want %q: the probe and the second ask belong to the memory's turn", got, w)
		}
	}
	for _, r := range []<-chan askedReply{m, cue} {
		if got := replied(t, r, 20*time.Second); got.err != nil || len(got.vectors) != 1 {
			t.Fatalf("a caller had no vector: %v", got.err)
		}
	}
}
