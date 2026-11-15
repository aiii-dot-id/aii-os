package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A SCHEDULED OPERATION'S RETRY TIME OUTLIVES ITS RUNNER. These run on the live store: a row is read back
// from it, never assumed from a runner's memory. Faults are made by a store
// that fails on purpose, so a write that is refused leaves no row at all.

const holdPlugin, holdOp = "com.example.scan", "scan.advance"

func holdRow(t *testing.T, a *App) (time.Time, string, bool) {
	t.Helper()
	row, ok, err := a.store.AlarmByID(scheduleHoldID(holdPlugin, holdOp))
	if err != nil {
		t.Fatal(err)
	}
	return time.UnixMilli(row.Deadline), row.OwnerName, ok
}

// faultStore is the live store with failures switched on and off, counting
// the writes it is asked for.
type faultStore struct {
	inner                       holdStore
	mu                          sync.Mutex
	failRead, failSet, failDrop bool
	sets, drops                 int
}

var errFault = errors.New("the store refused (test)")

func (f *faultStore) set(read, set, drop bool) {
	f.mu.Lock()
	f.failRead, f.failSet, f.failDrop = read, set, drop
	f.mu.Unlock()
}

func (f *faultStore) AlarmByID(id string) (store.Alarm, bool, error) {
	f.mu.Lock()
	fail := f.failRead
	f.mu.Unlock()
	if fail {
		return store.Alarm{}, false, errFault
	}
	return f.inner.AlarmByID(id)
}

func (f *faultStore) SetAlarm(id, owner, clock string, deadline int64, repeat *int64, payload string) error {
	f.mu.Lock()
	f.sets++
	fail := f.failSet
	f.mu.Unlock()
	if fail {
		return errFault
	}
	return f.inner.SetAlarm(id, owner, clock, deadline, repeat, payload)
}

func (f *faultStore) CancelAlarm(owner, id string) error {
	f.mu.Lock()
	f.drops++
	fail := f.failDrop
	f.mu.Unlock()
	if fail {
		return errFault
	}
	return f.inner.CancelAlarm(owner, id)
}

func faulty(a *App) *faultStore {
	f := &faultStore{inner: a.store}
	a.scheduleRows.store = f
	return f
}

func stopRunners(a *App, stop context.CancelFunc) {
	stop()
	for _, r := range a.scheduling {
		<-r.done
	}
	a.scheduling = nil
}

func firstCallNotBefore(t *testing.T, calls [][2]time.Time, deadline time.Time) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("no call came")
	}
	if calls[0][0].Before(time.UnixMilli(deadline.UnixMilli())) {
		t.Fatalf("CALLED BEFORE THE RETRY TIME: called at %s, not before %s", calls[0][0].Format(time.RFC3339Nano), time.UnixMilli(deadline.UnixMilli()).Format(time.RFC3339Nano))
	}
}

// A saved deadline still ahead is waited out by a new runner, as after a
// restart; one already past costs only the first delay, and one call.
func TestARunnerStartsAfterASavedRetryTime(t *testing.T) {
	op := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
	a := liveApp(t)
	deadline := time.Now().Add(400 * time.Millisecond)
	if err := a.store.SetAlarm(scheduleHoldID(holdPlugin, holdOp), scheduleOwnerName, "wall", deadline.UnixMilli(), nil, ""); err != nil {
		t.Fatal(err)
	}
	startChain(t, a, 30*time.Millisecond, op)
	calls := quiet(t, op, 1)
	if len(calls) != 1 {
		t.Fatalf("calls: %d", len(calls))
	}
	firstCallNotBefore(t, calls, deadline)
}

// D4-R1: an ended chain whose answer asks for less than the interval still
// saves the time; a runner that replaces it waits the rest of it, measured
// against the stored millisecond deadline; and an answer that then names no
// retry time removes the row.
func TestAShortRetryTimeIsSavedAndAReplacementWaitsTheRest(t *testing.T) {
	retry := time.Now().Add(time.Second)
	first := &chainOp{answer: func(int) (tools.Result, error) {
		return answerJSON(map[string]any{"retry_not_before": retry.UTC().Format(time.RFC3339Nano)}), nil
	}}
	a := liveApp(t)
	stop := startChain(t, a, 30*time.Millisecond, first)
	quiet(t, first, 1)
	at, owner, ok := holdRow(t, a)
	if !ok || owner != scheduleOwnerName || at.UnixMilli() != retry.UnixMilli() {
		t.Fatalf("THE RETRY TIME WAS NOT SAVED: row %v %s %s, asked %s", ok, owner, at, retry)
	}
	stopRunners(a, stop)
	a.toolReg.Deregister(first.name)
	next := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
	startChain(t, a, 30*time.Millisecond, next)
	calls := quiet(t, next, 1)
	if len(calls) != 1 {
		t.Fatalf("calls: %d", len(calls))
	}
	firstCallNotBefore(t, calls, at)
	if _, _, ok := holdRow(t, a); ok {
		t.Fatal("an answer that asked for no retry time left the row")
	}
}

// D4-F1: a write the store did not take, with no row left behind, is kept by
// the App: a runner that replaces the one that held it, while the host goes
// on, still waits the deadline out.
func TestADeadlineTheStoreRefusedIsKeptAcrossAReplacement(t *testing.T) {
	a := liveApp(t)
	f := faulty(a)
	f.set(false, true, false)
	retry := time.Now().Add(1500 * time.Millisecond)
	first := &chainOp{answer: func(int) (tools.Result, error) {
		return answerJSON(map[string]any{"retry_not_before": retry.UTC().Format(time.RFC3339Nano)}), nil
	}}
	stop := startChain(t, a, 30*time.Millisecond, first)
	quiet(t, first, 1)
	if _, _, ok := holdRow(t, a); ok {
		t.Fatal("precondition: the refused write left a row")
	}
	stopRunners(a, stop)
	a.toolReg.Deregister(first.name)
	f.set(false, false, false) // reads and writes work again; there is still no row
	next := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
	startChain(t, a, 30*time.Millisecond, next)
	calls := quiet(t, next, 1)
	firstCallNotBefore(t, calls, retry)
}

// D4-R2: a row of another owner at the id calls nothing and is not removed;
// a failed read calls nothing until a read works.
func TestARetryRowThatCannotBeReadOrIsAnotherOwnersCallsNothing(t *testing.T) {
	t.Run("another owner's", func(t *testing.T) {
		op := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
		a := liveApp(t)
		if err := a.store.SetAlarm(scheduleHoldID(holdPlugin, holdOp), "timers", "wall", time.Now().Add(time.Hour).UnixMilli(), nil, ""); err != nil {
			t.Fatal(err)
		}
		startChain(t, a, 30*time.Millisecond, op)
		time.Sleep(300 * time.Millisecond)
		if n := len(op.calls()); n != 0 {
			t.Fatalf("A FOREIGN ROW AT THE RETRY ID WAS READ AS ABSENT: %d calls", n)
		}
		if _, owner, ok := holdRow(t, a); !ok || owner != "timers" {
			t.Fatalf("the foreign row was touched: %v %s", ok, owner)
		}
	})
	t.Run("a failed read", func(t *testing.T) {
		op := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
		a := liveApp(t)
		f := faulty(a)
		f.set(true, false, false)
		startChain(t, a, 30*time.Millisecond, op)
		time.Sleep(300 * time.Millisecond)
		if n := len(op.calls()); n != 0 {
			t.Fatalf("A FAILED READ WAS TAKEN AS NO DEADLINE: %d calls", n)
		}
		f.set(false, false, false)
		if calls := quiet(t, op, 1); len(calls) != 1 {
			t.Fatalf("once the read works: %d calls, want 1", len(calls))
		}
	})
}

// D4-F2: a malformed row of ours holds calls back until it is removed, and a
// removal that fails is not absence. Once the removal succeeds, the live
// runner calls at its own first delay, not at the row's old time: the hold's deadline is read beside the loop's floor,
// never copied into it. (Giving up at the row's own time is in
// persistLocked; this test does not wait an hour to see it.)
func TestAMalformedRowHoldsCallsBackUntilItIsRemoved(t *testing.T) {
	op := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
	a := liveApp(t)
	a.scheduleRows.retryFor = 100 * time.Millisecond
	every := int64(1000)
	if err := a.store.SetAlarm(scheduleHoldID(holdPlugin, holdOp), scheduleOwnerName, "wall", time.Now().Add(time.Hour).UnixMilli(), &every, "x"); err != nil {
		t.Fatal(err)
	}
	f := faulty(a)
	f.set(false, false, true)
	startChain(t, a, 30*time.Millisecond, op)
	time.Sleep(400 * time.Millisecond)
	if n := len(op.calls()); n != 0 {
		t.Fatalf("A MALFORMED ROW WHOSE REMOVAL FAILED WAS READ AS ABSENT: %d calls", n)
	}
	if _, _, ok := holdRow(t, a); !ok {
		t.Fatal("the row is gone though its removal failed")
	}
	released := time.Now()
	f.set(false, false, false) // the removal succeeds at the runner's next retry
	deadline := time.Now().Add(10 * time.Second)
	for len(op.calls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(op.calls()) == 0 {
		t.Fatal("A RUNNER HELD BY A MALFORMED ROW DID NOT RESUME ONCE THE ROW WAS REMOVED: it still waits the row's old time")
	}
	if took := op.calls()[0][0].Sub(released); took > 5*time.Second {
		t.Fatalf("the runner resumed %s after the removal could succeed; the retry is %s", took, a.holdRetry())
	}
	if _, _, ok := holdRow(t, a); ok {
		t.Fatal("a removal that succeeded left the row")
	}
}

// The cap: a saved row past a week from its read is normalized once and
// persisted, and a later read keeps that absolute time, not a new week. A
// normalization the store refuses holds the capped time in memory.
func TestAnOverlongSavedRetryTimeIsNormalizedOnce(t *testing.T) {
	a := liveApp(t)
	now := time.Now()
	id := scheduleHoldID(holdPlugin, holdOp)
	if err := a.store.SetAlarm(id, scheduleOwnerName, "wall", now.Add(30*24*time.Hour).UnixMilli(), nil, ""); err != nil {
		t.Fatal(err)
	}
	h := newScheduleHold(a, holdPlugin, holdOp)
	if err := h.load(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	want := time.UnixMilli(now.Add(scheduleRetryCap).UnixMilli())
	if at, _, _ := holdRow(t, a); at.UnixMilli() != want.UnixMilli() || !h.stored {
		t.Fatalf("normalized to %s (stored %v), want %s", at, h.stored, want)
	}
	later := newScheduleHold(a, holdPlugin, holdOp)
	if err := later.load(t.Context(), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !later.want.Equal(want) {
		t.Fatalf("A LATER READ CAPPED AGAIN FROM ITS OWN TIME: %s, kept %s", later.want, want)
	}

	b := liveApp(t)
	if err := b.store.SetAlarm(id, scheduleOwnerName, "wall", now.Add(30*24*time.Hour).UnixMilli(), nil, ""); err != nil {
		t.Fatal(err)
	}
	f := faulty(b)
	f.set(false, true, false)
	refused := newScheduleHold(b, holdPlugin, holdOp)
	if err := refused.load(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if refused.stored || refused.want.UnixMilli() != now.Add(scheduleRetryCap).UnixMilli() {
		t.Fatalf("a refused normalization: want %s stored %v", refused.want, refused.stored)
	}
}

// Explicit degradation, and its throttle: a refused write keeps the deadline
// in memory, is not called stored, wakes the runner once a minute, and an
// unchanged request does not retry sooner than that.
func TestARefusedWriteIsKeptInMemoryAndRetriedAtMostOnceAMinute(t *testing.T) {
	a := liveApp(t)
	f := faulty(a)
	now := time.Now()
	h := newScheduleHold(a, holdPlugin, holdOp)
	if err := h.load(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	f.set(false, true, false)
	h.set(t.Context(), now.Add(time.Hour), now)
	if h.stored || !h.want.Equal(now.Add(time.Hour)) {
		t.Fatalf("A REFUSED WRITE WAS TAKEN AS STORED: stored %v, want %s", h.stored, h.want)
	}
	if got := nextWake(h, now.Add(time.Hour), now); got != scheduleHoldRetry {
		t.Fatalf("a runner whose row is not stored wakes to retry in %s, not %s", got, scheduleHoldRetry)
	}
	sets := f.sets
	h.set(t.Context(), now.Add(time.Hour), now.Add(time.Second)) // the same request again
	h.persist(t.Context(), now.Add(2*time.Second))
	if f.sets != sets {
		t.Fatalf("an unchanged request retried within the minute: %d writes, was %d", f.sets, sets)
	}
	h.persist(t.Context(), now.Add(scheduleHoldRetry+time.Second))
	if f.sets != sets+1 {
		t.Fatalf("a due retry was not made: %d writes, was %d", f.sets, sets)
	}
	// A replacement's load inherits the refused write and keeps its throttle.
	sets = f.sets
	if err := h.load(t.Context(), now.Add(scheduleHoldRetry+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if f.sets != sets || h.stored {
		t.Fatalf("a load within the minute retried the refused write, or called it stored: %d writes, was %d, stored %v", f.sets, sets, h.stored)
	}
	// A changed request is tried at once.
	h.set(t.Context(), now.Add(2*time.Hour), now.Add(scheduleHoldRetry+3*time.Second))
	if f.sets != sets+1 {
		t.Fatalf("a changed request was not tried at once: %d writes, was %d", f.sets, sets)
	}
}

// A runner whose context is done changes nothing: its late answer, whatever
// it asks, leaves the hold and the row as they were.
func TestASetUnderADoneContextChangesNothing(t *testing.T) {
	a := liveApp(t)
	h := a.scheduleHoldFor(holdPlugin, holdOp)
	if err := h.load(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h.set(ctx, time.Now().Add(time.Hour), time.Now())
	if want, _ := h.state(); !want.IsZero() {
		t.Fatalf("A RETIRING RUNNER CHANGED THE HOLD: %s", want)
	}
	if _, _, ok := holdRow(t, a); ok {
		t.Fatal("A RETIRING RUNNER WROTE THE ROW")
	}
}

// The interleaving a review named: a retiring runner's call returns, with
// a retry time, after its successor has read the row. The late answer writes
// nothing, and the successor's promise is untouched.
func TestARetiringRunnersLateAnswerDoesNotTouchItsSuccessorsPromise(t *testing.T) {
	a := liveApp(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	late := time.Now().Add(time.Hour)
	old := &chainOp{answer: func(int) (tools.Result, error) {
		entered <- struct{}{}
		<-release
		return answerJSON(map[string]any{"retry_not_before": late.UTC().Format(time.RFC3339Nano)}), nil
	}}
	stop := startChain(t, a, 30*time.Millisecond, old)
	<-entered // the old runner is inside its call
	var oldRunner *scheduleRunner
	for _, r := range a.scheduling {
		oldRunner = r
	}
	stop()             // retired: its context is done
	a.scheduling = nil // its successor starts while its call is still out
	a.toolReg.Deregister(old.name)
	next := &chainOp{answer: func(int) (tools.Result, error) { return answerJSON(map[string]any{}), nil }}
	startChain(t, a, 30*time.Millisecond, next)
	quiet(t, next, 1) // the successor has read the row and made its call
	close(release)    // now the old call returns its retry time
	<-oldRunner.done
	if _, _, ok := holdRow(t, a); ok {
		t.Fatal("A RETIRING RUNNER'S LATE ANSWER WROTE THE ROW")
	}
	if want, _ := a.scheduleHoldFor(holdPlugin, holdOp).state(); !want.IsZero() {
		t.Fatalf("A RETIRING RUNNER'S LATE ANSWER CHANGED ITS SUCCESSOR'S PROMISE: %s", want)
	}
}

// A firing of an old deadline cannot remove a row armed since: the facility
// removes by compare-and-swap on the deadline it fired. This is the store's
// contract, read directly; no TIME facility runs here.
func TestAFiringOfAnOldRetryTimeLeavesARowArmedSince(t *testing.T) {
	a := liveApp(t)
	id := scheduleHoldID(holdPlugin, holdOp)
	old, rearmed := time.Now().Add(-time.Minute).UnixMilli(), time.Now().Add(time.Hour).UnixMilli()
	if err := a.store.SetAlarm(id, scheduleOwnerName, "wall", old, nil, ""); err != nil {
		t.Fatal(err)
	}
	if res := (scheduleOwner{}).OnAlarm(t.Context(), id, "wall", old, ""); !res.Accepted {
		t.Fatal("the owner did not accept its firing")
	}
	if err := a.store.SetAlarm(id, scheduleOwnerName, "wall", rearmed, nil, ""); err != nil {
		t.Fatal(err)
	}
	if removed, err := a.store.DeleteAlarmCAS(id, old); err != nil || removed {
		t.Fatalf("the old firing's removal took the re-armed row: %v %v", removed, err)
	}
	if at, _, ok := holdRow(t, a); !ok || at.UnixMilli() != rearmed {
		t.Fatalf("the re-armed row is gone: %v %s", ok, at)
	}
}
