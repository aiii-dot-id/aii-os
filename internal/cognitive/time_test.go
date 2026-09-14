package cognitive

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type failingDueStore struct {
	SetAlarmer
	err error
}

func (s failingDueStore) DueAlarms(string, int64, int) ([]store.Alarm, error) {
	return nil, s.err
}

// .
// .

type recordingOwner struct {
	name     string
	accepted bool
	next     *int64
	delay    time.Duration
	fired    chan int64
}

type blockingPlatformWake struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	last    int64
}

func (w *blockingPlatformWake) WakeAt(at time.Time) error {
	w.once.Do(func() { close(w.started) })
	<-w.release
	w.mu.Lock()
	w.last = at.UnixMilli()
	w.mu.Unlock()
	return nil
}

func (w *blockingPlatformWake) WakeClear() {
	w.mu.Lock()
	w.last = -1
	w.mu.Unlock()
}

func (w *blockingPlatformWake) lastWake() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

func (r *recordingOwner) Name() string { return r.name }
func (r *recordingOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	if r.delay > 0 {
		select {
		case <-ctx.Done():
			return AlarmResult{}
		case <-time.After(r.delay):
		}
	}
	if r.fired != nil {
		select {
		case r.fired <- deadline:
		case <-ctx.Done():
			return AlarmResult{}
		}
	}
	return AlarmResult{Accepted: r.accepted, NextDeadline: r.next}
}

func newTIME(t *testing.T) (*TIME, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "time.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewTIME(st, st), st
}

func TestEvaluateAllReturnsStoreFailure(t *testing.T) {
	_, st := newTIME(t)
	want := errors.New("projection unavailable")
	tm := NewTIME(failingDueStore{SetAlarmer: st, err: want}, st)
	if err := tm.EvaluateAll(context.Background()); !errors.Is(err, want) {
		t.Fatalf("EvaluateAll error = %v, want %v", err, want)
	}
}

// .
// .
// .
func TestAcceptedOneShotReschedules(t *testing.T) {
	tm, st := newTIME(t)
	next := int64(1_000_000)
	tm.RegisterOwner(&recordingOwner{name: "daily", accepted: true, next: &next})
	st.SetAlarm("daily", "daily", "wall", 1, nil, "")

	tm.EvaluateAll(context.Background())

	var deadline int64
	var repeat interface{}
	if err := st.QueryRowForTest(`SELECT deadline, repeat_every FROM alarms WHERE alarm_id = 'daily'`).Scan(&deadline, &repeat); err != nil {
		t.Fatalf("alarm must SURVIVE an accepted+NextDeadline firing: %v", err)
	}
	if deadline != next {
		t.Fatalf("alarm must be rescheduled to owner's deadline %d, got %d", next, deadline)
	}
}

// .
// .
// .
func TestDispatchOutsideStateLock(t *testing.T) {
	tm, _ := newTIME(t)
	slow := &recordingOwner{name: "slow", accepted: true, delay: 150 * time.Millisecond}
	tm.RegisterOwner(slow)

	// .
	// .
	st := tm.store
	if err := st.SetAlarm("slow", "slow", "life", 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	// .
	done := make(chan struct{})
	go func() {
		defer close(done)
		// .
		// .
		if err := tm.AdvanceLifeClock(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	// .
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = tm.LifeClock()
		time.Sleep(5 * time.Millisecond)
	}
	<-done
}

// .
// .
// .
func TestDeclinedRecurringRearms(t *testing.T) {
	tm, st := newTIME(t)
	tm.RegisterOwner(&recordingOwner{name: "dream", accepted: false})
	repeat := int64(5)
	st.SetAlarm("dream", "dream", "life", 1, &repeat, "")

	// .
	for i := 0; i < 10; i++ {
		if err := tm.AdvanceLifeClock(context.Background()); err != nil {
			t.Fatal(err)
		}
	}

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'dream'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline <= 10 {
		t.Fatalf("declined recurring must be re-armed past the current clock (guard vs starvation), got deadline %d at clock 10", deadline)
	}
	// .
	// .
}

// .
// .
func TestStaleFiringSkipped(t *testing.T) {
	tm, st := newTIME(t)
	next := int64(9_000)
	tm.RegisterOwner(&recordingOwner{name: "daily", accepted: true, next: &next})
	st.SetAlarm("daily", "daily", "wall", 1, nil, "")

	// .
	due, err := st.DueAlarms("wall", WallNow(), 10)
	if err != nil || len(due) != 1 || due[0].Deadline != 1 {
		t.Fatalf("due snapshot: %+v %v", due, err)
	}
	// .
	// .
	// .
	future := WallNow() + 3_600_000
	if ok, err := st.UpdateAlarmDeadlineCAS("daily", 1, future); err != nil || !ok {
		t.Fatalf("setup replace: %v %v", ok, err)
	}
	// .
	// .
	tm.dispatchPass(context.Background(), "wall", due)

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'daily'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != future {
		t.Fatalf("stale firing must not touch the replaced row; want %d, got %d", future, deadline)
	}
}

// .
// .
// .
func TestBootCatchUpFiresMissedAlarm(t *testing.T) {
	tm, st := newTIME(t)
	fired := make(chan int64, 1)
	tm.RegisterOwner(&recordingOwner{name: "brief", accepted: true, fired: fired})
	// .
	st.SetAlarm("brief", "brief", "wall", WallNow()-3_600_000, nil, "")

	done := make(chan struct{})
	go func() { tm.EvaluateAll(context.Background()); close(done) }()
	select {
	case <-fired:
		// .
	case <-time.After(2 * time.Second):
		t.Fatal("missed alarm did not fire on boot recovery")
	}
	<-done
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestWallTimerNeverStacks(t *testing.T) {
	tm, st := newTIME(t)
	next := WallNow() + 3_600_000
	tm.RegisterOwner(&recordingOwner{name: "daily", accepted: true, next: &next})
	st.SetAlarm("daily", "daily", "wall", next, nil, "")

	before := runtime.NumGoroutine()
	tm.Start(context.Background())
	tm.Stop()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("scheduler goroutine leaked after Stop: before=%d after=%d", before, after)
	}

	// .
	tm.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	tm.Stop()
}

// .
func TestSetAlarmRequiresRegisteredOwner(t *testing.T) {
	tm, _ := newTIME(t)
	if err := tm.SetAlarm("ghost", "nobody", "wall", WallNow()+1000, nil, ""); err == nil {
		t.Fatal("arming an alarm for an unregistered owner must be rejected — it would fire into the void")
	}
	tm.RegisterOwner(&recordingOwner{name: "dream", accepted: true})
	if err := tm.SetAlarm("dream", "dream", "life", 5, nil, ""); err != nil {
		t.Fatalf("registered owner must arm: %v", err)
	}
}

// .
// .
// .
func TestHeartbeatLiveGating(t *testing.T) {
	tm, _ := newTIME(t)
	live := false
	tm.mu.Lock()
	tm.pulse = &fakePulse{live: func() bool { return live }}
	tm.mu.Unlock()
	tm.timerCtx = context.Background()

	tm.pulseFire()
	if got := tm.LifeClock(); got != 0 {
		t.Fatalf("no live session: pulse must NOT advance the life clock, got %d", got)
	}

	live = true
	tm.pulseFire()
	if got := tm.LifeClock(); got != 1 {
		t.Fatalf("live session: pulse must advance the life clock, got %d", got)
	}

	live = false
	tm.pulseFire()
	if got := tm.LifeClock(); got != 1 {
		t.Fatalf("session gone: clock must freeze, got %d", got)
	}
}

type fakePulse struct{ live func() bool }

func (f *fakePulse) Interval() time.Duration { return time.Minute }
func (f *fakePulse) Live() bool              { return f.live() }

// .
// .
// .
// .
func TestStopIsTerminalAndIdempotent(t *testing.T) {
	fac, _ := newTIME(t)
	fac.RegisterOwner(&recordingOwner{name: "wallowner"})
	fac.SetAlarm("wa", "wallowner", "wall", WallNow()-1000, nil, "")

	fac.Start(context.Background())
	fac.Stop()
	fac.Stop()

	// .
	fac.After("late", time.Millisecond, func() { t.Fatal("no fires after Stop") })
	fac.EvaluateAll(context.Background())
	time.Sleep(50 * time.Millisecond)
}

func TestStopClearsPlatformWakeAfterSchedulerExits(t *testing.T) {
	fac, st := newTIME(t)
	wake := &blockingPlatformWake{started: make(chan struct{}), release: make(chan struct{})}
	fac.SetPlatformWake(wake)
	fac.RegisterOwner(&recordingOwner{name: "wallowner"})
	if err := st.SetAlarm("future", "wallowner", "wall", WallNow()+60_000, nil, ""); err != nil {
		t.Fatal(err)
	}
	fac.Start(context.Background())
	<-wake.started

	stopped := make(chan struct{})
	go func() {
		fac.Stop()
		close(stopped)
	}()
	close(wake.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not wait for the scheduler")
	}
	if got := wake.lastWake(); got != -1 {
		t.Fatalf("scheduler re-armed the platform after Stop cleared it: %d", got)
	}
}

// .
// .
func TestSetAlarmEarlierWakesTimer(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "waker", accepted: true, fired: make(chan int64, 4)}
	fac.RegisterOwner(owner)

	// .
	fac.SetAlarm("far", "waker", "wall", WallNow()+3600_000, nil, "")
	fac.Start(context.Background())
	deadline := WallNow() + 50
	fac.SetAlarm("soon", "waker", "wall", deadline, nil, "")

	select {
	case got := <-owner.fired:
		if got != deadline {
			t.Fatalf("fired deadline %d, want the EARLY one %d", got, deadline)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("earlier wall alarm never fired — SetAlarm did not wake the timer")
	}
	fac.Stop()
}

// .

// .
// .
// .
func TestWallAlarmFiresBetweenPulses(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "fast", accepted: true, fired: make(chan int64, 2)}
	fac.RegisterOwner(owner)
	fac.Start(context.Background())
	defer fac.Stop()

	deadline := WallNow() + 100
	fac.SetAlarm("quick", "fast", "wall", deadline, nil, "")

	select {
	case <-owner.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("wall alarm did not fire without any pulse — evaluation is pulse-coupled (v1 disease)")
	}
}

// .
func TestEphemeralCadenceAndPanicContainment(t *testing.T) {
	fac, _ := newTIME(t)
	fac.Start(context.Background())
	defer fac.Stop()

	fired := make(chan int, 5)
	fac.Every("tick", 30*time.Millisecond, func() { fired <- 1 })
	n := 0
	select {
	case <-fired:
		n++
	case <-time.After(2 * time.Second):
		t.Fatal("ephemeral never fired")
	}
	// .
	deadline := time.After(500 * time.Millisecond)
	for n < 3 {
		select {
		case <-fired:
			n++
		case <-deadline:
			t.Fatalf("recurrence broken: only %d fires", n)
		}
	}
	fac.Cancel("tick")

	// .
	// .
	var boom atomic.Int32
	fac.Every("boom", 20*time.Millisecond, func() { boom.Add(1); panic("contained") })
	time.Sleep(150 * time.Millisecond)
	if boom.Load() > 2 {
		t.Fatalf("panicking Every must auto-cancel, fired %d times", boom.Load())
	}

	// .
	after := make(chan struct{})
	fac.After("still-alive", 10*time.Millisecond, func() { close(after) })
	select {
	case <-after:
	case <-time.After(2 * time.Second):
		t.Fatal("clockwork died with the panicking callback")
	}
}

// .
// .
func TestPanickingOwnerIsContainedAndRetried(t *testing.T) {
	fac, _ := newTIME(t)
	panicky := &panickingOwner{fired: make(chan struct{}, 1)}
	fac.RegisterOwner(panicky)
	fac.Start(context.Background())
	defer fac.Stop()

	next := WallNow() + 50
	fac.SetAlarm("po", "panic", "wall", next, nil, "")

	select {
	case <-panicky.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("panicking owner never fired")
	}
	// .
	// .
	time.Sleep(100 * time.Millisecond)
	alarms, err := fac.store.DueAlarms("wall", WallNow()+3600000, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range alarms {
		if a.AlarmID == "po" {
			found = true
		}
	}
	if !found {
		t.Fatal("panicking owner's alarm row must be preserved (declined, not deleted)")
	}
}

type panickingOwner struct{ fired chan struct{} }

func (p *panickingOwner) Name() string { return "panic" }
func (p *panickingOwner) OnAlarm(ctx context.Context, id, clock string, deadline int64, payload string) AlarmResult {
	if p.fired == nil {
		p.fired = make(chan struct{}, 1)
	}
	select {
	case p.fired <- struct{}{}:
	default:
	}
	panic("owner panic")
}

// .
// .
// .
func TestMidDispatchKillLeavesRowDue(t *testing.T) {
	fac, _ := newTIME(t)
	fac.RegisterOwner(&blockingOwner{started: make(chan struct{}, 1)})
	fac.SetAlarm("blk", "block", "wall", WallNow()+30, nil, "")
	// .
	// .
	go fac.dispatchPass(context.Background(), "wall", []store.Alarm{{
		AlarmID: "blk", OwnerName: "block", Clock: "wall", Deadline: WallNow() - 1,
	}})
	<-func() chan struct{} {
		fac.mu.Lock()
		o := fac.owners["block"].(*blockingOwner)
		fac.mu.Unlock()
		return o.started
	}()
	// .
	time.Sleep(50 * time.Millisecond)
	alarms, _ := fac.store.DueAlarms("wall", WallNow()+3600000, 10)
	due := false
	for _, a := range alarms {
		if a.AlarmID == "blk" && a.Deadline <= WallNow() {
			due = true
		}
	}
	if !due {
		t.Fatal("mid-dispatch kill must leave the row DUE for catch-up refire (M16)")
	}
}

type blockingOwner struct{ started chan struct{} }

func (b *blockingOwner) Name() string { return "block" }
func (b *blockingOwner) OnAlarm(ctx context.Context, id, clock string, deadline int64, payload string) AlarmResult {
	b.started <- struct{}{}
	<-ctx.Done()
	return AlarmResult{}
}

// .
func TestAlarmPayloadPassesThrough(t *testing.T) {
	fac, st := newTIME(t)
	got := make(chan string, 1)
	fac.RegisterOwner(&payloadOwner{got: got})
	fac.Start(context.Background())
	defer fac.Stop()

	fac.SetAlarm("tmr", "payloads", "wall", WallNow()+40, nil, "operator: your 7 AM wake-up")
	select {
	case p := <-got:
		if p != "operator: your 7 AM wake-up" {
			t.Fatalf("payload mangled: %q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("payload alarm never fired")
	}
	_ = st
}

type payloadOwner struct{ got chan string }

func (p *payloadOwner) Name() string { return "payloads" }
func (p *payloadOwner) OnAlarm(ctx context.Context, id, clock string, deadline int64, payload string) AlarmResult {
	p.got <- payload
	return AlarmResult{Accepted: true}
}

// .
// .
// .
// .
func TestNextWakeUnboundedHorizon(t *testing.T) {
	fac, st := newTIME(t)
	far := WallNow() + 2*3600_000
	st.SetAlarm("farwall", "x", "wall", far, nil, "")
	fac.RegisterOwner(&recordingOwner{name: "x"})

	next, has := fac.nextWake()
	if !has {
		t.Fatal("wall alarm beyond 1h must still produce a wake (unbounded horizon)")
	}
	if got := next.UnixMilli(); got != far {
		t.Fatalf("nextWake = %d, want the far deadline %d", got, far)
	}
}

// .
// .
// .
func TestFarWallAlarmStillFires(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "far", accepted: true, fired: make(chan int64, 2)}
	fac.RegisterOwner(owner)
	fac.Start(context.Background())
	defer fac.Stop()

	// .
	// .
	// .
	deadline := WallNow() + 120
	fac.SetAlarm("fw", "far", "wall", deadline, nil, "")
	select {
	case <-owner.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("wall alarm with no ephemerals never fired — scheduler dead without pulse")
	}
}

// .
// .
// .
func TestNextWakeHidesInFlightDispatch(t *testing.T) {
	tm, st := newTIME(t)
	owner := &recordingOwner{name: "slow", accepted: true}
	tm.RegisterOwner(owner)

	future := WallNow() + 3600_000
	st.SetAlarm("far", "slow", "wall", future, nil, "")

	past := WallNow() - 1000
	st.SetAlarm("due", "slow", "wall", past, nil, "")

	// .
	next, has := tm.nextWake()
	if !has || next.UnixMilli() != past {
		t.Fatalf("pre-dispatch nextWake = %v, want the due row", next)
	}

	// .
	tm.SetAlarmEnqueuer(&captureEnqueuer{})
	tm.dispatchPass(context.Background(), "wall", mustDue(t, st, "wall", past))

	next, has = tm.nextWake()
	if !has {
		t.Fatal("in-flight row must schedule its bounded retry")
	}
	retryDelay := time.Until(next)
	if retryDelay < pendingDispatchRetryAfter-time.Second || retryDelay > pendingDispatchRetryAfter {
		t.Fatalf("in-flight row retry = %v, want about %v", retryDelay, pendingDispatchRetryAfter)
	}

	// .
	// .
	// .
	// .
	tm.ClearPendingDispatch("due")
	next, has = tm.nextWake()
	if !has || next.UnixMilli() != past {
		t.Fatalf("post-release nextWake = %v, want the due row again (retry semantics)", next)
	}
}

type captureEnqueuer struct{ items []store.Alarm }

func (c *captureEnqueuer) EnqueueAlarm(a store.Alarm) error {
	c.items = append(c.items, a)
	return nil
}

// .
// .
// .
func TestUnregisteredOwnerNotEnqueued(t *testing.T) {
	tm, st := newTIME(t)
	tm.RegisterOwner(&recordingOwner{name: "real"})

	past := WallNow() - 1000
	st.SetAlarm("orphan", "ghost", "wall", past, nil, "")
	// .
	// .

	enq := &captureEnqueuer{}
	tm.SetAlarmEnqueuer(enq)
	tm.dispatchPass(context.Background(), "wall", mustDue(t, st, "wall", past))

	if len(enq.items) != 0 {
		t.Fatalf("unregistered owner's alarm was enqueued — handler-failure loop; got %d items", len(enq.items))
	}
	// .
	if _, hidden := tm.pendingDispatch["orphan"]; hidden {
		t.Fatal("unregistered dispatch must not hold nextWake hostage")
	}
}

func mustDue(t *testing.T, st *store.Store, clock string, nowOrLess int64) []store.Alarm {
	t.Helper()
	alarms, err := st.DueAlarms(clock, nowOrLess, 10)
	if err != nil {
		t.Fatal(err)
	}
	return alarms
}
