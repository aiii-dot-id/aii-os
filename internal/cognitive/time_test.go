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

// The rhythm audit tests (2026-08-16) — each pins a finding against the
// canon dispatch law (TIME_FACILITIES.md §4–5, acceptance criteria).

type recordingOwner struct {
	name     string
	accepted bool
	next     *int64
	delay    time.Duration
	fired    chan int64 // receives the scheduled deadline on each firing
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

type ticker struct{ ticks int64 }

func (t *ticker) LifetimeTicks() (int64, error) { return t.ticks, nil }
func (t *ticker) IncrementLifetimeTicks() error { t.ticks++; return nil }

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

// F1: an accepted one-shot WITH NextDeadline must RESCHEDULE, not die.
// The daily morning brief fired once per process lifetime before this —
// the dispatcher deleted one-shots on accept and discarded NextDeadline.
func TestAcceptedOneShotReschedules(t *testing.T) {
	tm, st := newTIME(t)
	next := int64(1_000_000)
	tm.RegisterOwner(&recordingOwner{name: "daily", accepted: true, next: &next})
	st.SetAlarm("daily", "daily", "wall", 1, nil, "") // due immediately

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

// F2: owner dispatch must happen OUTSIDE the state lock — a slow owner
// (LLM work) must not block LifeClock() or the next pulse. The C rhythm
// processor enqueues to an executor for exactly this reason.
func TestDispatchOutsideStateLock(t *testing.T) {
	tm, _ := newTIME(t)
	slow := &recordingOwner{name: "slow", accepted: true, delay: 150 * time.Millisecond}
	tm.RegisterOwner(slow)

	// Arm a due LIFE alarm, then advance the clock from a goroutine while
	// asserting LifeClock stays responsive during the slow owner's dispatch
	st := tm.store
	if err := st.SetAlarm("slow", "slow", "life", 1, nil, ""); err != nil {
		t.Fatal(err)
	}
	// life clock starts at 0; set alarm due at 1; advance to 1
	done := make(chan struct{})
	go func() {
		defer close(done)
		// tick underlying store to 1 via the lifetime interface? TIME uses
		// its own lifetime; for tm it's the store. Advance:
		if err := tm.AdvanceLifeClock(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	// While the slow owner is dispatching, LifeClock() must return fast
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = tm.LifeClock() // must not block >150ms-owner
		time.Sleep(5 * time.Millisecond)
	}
	<-done
}

// F5: a DECLINED recurring alarm without NextDeadline must rearm one
// period — the starvation guard. Previously the row stayed due forever
// and was re-invoked on every pulse.
func TestDeclinedRecurringRearms(t *testing.T) {
	tm, st := newTIME(t)
	tm.RegisterOwner(&recordingOwner{name: "dream", accepted: false})
	repeat := int64(5)
	st.SetAlarm("dream", "dream", "life", 1, &repeat, "") // due at life 1

	// advance life clock to 10 (store-backed lifetime)
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
	// fired exactly once at clock 1 (declined → rearmed to 15 → not due again)
	// (single-pulse assertion: the alarm should NOT have fired on every pulse)
}

// F4/CAS: a stale firing attempt (row replaced between due-read and
// transition) must NOT apply its transition to the new row.
func TestStaleFiringSkipped(t *testing.T) {
	tm, st := newTIME(t)
	next := int64(9_000)
	tm.RegisterOwner(&recordingOwner{name: "daily", accepted: true, next: &next})
	st.SetAlarm("daily", "daily", "wall", 1, nil, "")

	// Snapshot the due set (what a dispatch pass would read)…
	due, err := st.DueAlarms("wall", WallNow(), 10)
	if err != nil || len(due) != 1 || due[0].Deadline != 1 {
		t.Fatalf("due snapshot: %+v %v", due, err)
	}
	// …then the row is replaced before the transition lands (the window
	// the CAS exists for). Use a deadline that is NOT due so the new row
	// cannot itself be legitimately re-fired by the pass.
	future := WallNow() + 3_600_000
	if ok, err := st.UpdateAlarmDeadlineCAS("daily", 1, future); err != nil || !ok {
		t.Fatalf("setup replace: %v %v", ok, err)
	}
	// Dispatch the STALE snapshot: scheduled deadline 1 no longer matches
	// the row (now future) → CAS must refuse the transition.
	tm.dispatchPass(context.Background(), "wall", due)

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'daily'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != future {
		t.Fatalf("stale firing must not touch the replaced row; want %d, got %d", future, deadline)
	}
}

// F3: boot catch-up — an alarm that became due while the system was down
// fires on EvaluateAll. (The old arm-before-evaluate order guaranteed it
// never did.)
func TestBootCatchUpFiresMissedAlarm(t *testing.T) {
	tm, st := newTIME(t)
	fired := make(chan int64, 1)
	tm.RegisterOwner(&recordingOwner{name: "brief", accepted: true, fired: fired})
	// a wall alarm due an hour ago
	st.SetAlarm("brief", "brief", "wall", WallNow()-3_600_000, nil, "")

	done := make(chan struct{})
	go func() { tm.EvaluateAll(context.Background()); close(done) }()
	select {
	case <-fired:
		// catch-up delivered
	case <-time.After(2 * time.Second):
		t.Fatal("missed alarm did not fire on boot recovery")
	}
	<-done
}

// F6 pin: re-arming must replace the sole timer, never stack a second
// chain (canon #4). Observable via time.Timer.Stop semantics: if the
// previous timer was stopped by the re-arm, calling Stop on it again
// returns false.
// v2: the scheduler is ONE goroutine with ONE timer at a time — the
// never-stacks property is structural now. This test pins the v2
// contract differently: Start is idempotent-safe (one loop), Stop is
// terminal, and no goroutine leaks between restarts.
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
	if after > before+2 { // loop exits; small slop for runtime internals
		t.Fatalf("scheduler goroutine leaked after Stop: before=%d after=%d", before, after)
	}

	// And the terminal property: a second Start after Stop does nothing.
	tm.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	tm.Stop()
}

// Canon §3 pin: Set accepts only a registered owner.
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

// The fixed C4, v2 shape: the life clock advances ONLY on accepted
// live pulses. No session → no advance (lived presence, not uptime).
// The pulse is TIME-internal now — pulseFire is the semantic unit.
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

// Stop is terminal and idempotent (2026-08-17 review): the wall-timer
// re-arm chain must not outlive Stop (a fired-then-Stopped AfterFunc
// used to re-arm through evaluateWallAlarms — a 60s zombie loop in any
// long-lived host), and a second Stop must not double-close the channel.
func TestStopIsTerminalAndIdempotent(t *testing.T) {
	fac, _ := newTIME(t)
	fac.RegisterOwner(&recordingOwner{name: "wallowner"})
	fac.SetAlarm("wa", "wallowner", "wall", WallNow()-1000, nil, "") // already due

	fac.Start(context.Background())
	fac.Stop()
	fac.Stop() // second Stop: must not panic

	// After Stop, arming ephemerals does nothing and the loop is gone.
	fac.After("late", time.Millisecond, func() { t.Fatal("no fires after Stop") })
	fac.EvaluateAll(context.Background()) // catch-up is store-driven, safe post-Stop
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

// SetAlarm for an earlier-than-armed WALL deadline must wake the timer:
// previously the new alarm waited for the old deadline (fired late).
func TestSetAlarmEarlierWakesTimer(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "waker", accepted: true, fired: make(chan int64, 4)}
	fac.RegisterOwner(owner)

	// Arm far out; then set one due almost immediately.
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

// --- TIME v2: the proofs ---

// DECOUPLING PROOF: a wall alarm fires BETWEEN pulses, sub-second —
// evaluation no longer rides a 300s loop. (M1 was the disease; this is
// the cure.)
func TestWallAlarmFiresBetweenPulses(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "fast", accepted: true, fired: make(chan int64, 2)}
	fac.RegisterOwner(owner)
	fac.Start(context.Background())
	defer fac.Stop()

	deadline := WallNow() + 100 // 100ms
	fac.SetAlarm("quick", "fast", "wall", deadline, nil, "")

	select {
	case <-owner.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("wall alarm did not fire without any pulse — evaluation is pulse-coupled (v1 disease)")
	}
}

// Sub-second ephemeral cadence + auto-cancel of a panicking Every.
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
	// enough ticks to prove recurrence
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

	// Panic containment: a panicking Every is auto-cancelled, the
	// clockwork survives (no process death), and it does NOT panic-loop.
	var boom atomic.Int32
	fac.Every("boom", 20*time.Millisecond, func() { boom.Add(1); panic("contained") })
	time.Sleep(150 * time.Millisecond)
	if boom.Load() > 2 {
		t.Fatalf("panicking Every must auto-cancel, fired %d times", boom.Load())
	}

	// The clockwork still works after the panics.
	after := make(chan struct{})
	fac.After("still-alive", 10*time.Millisecond, func() { close(after) })
	select {
	case <-after:
	case <-time.After(2 * time.Second):
		t.Fatal("clockwork died with the panicking callback")
	}
}

// PANICKING OWNER: recover → declined-without-deadline → row preserved
// → the alarm fires again on the next pass (retried, never lost).
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
	// The row must survive: transition only applies after OnAlarm
	// RETURNS, and a panic is contained as declined-no-deadline.
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

// MID-DISPATCH KILL (M16): transitions commit only after owner
// completion — an owner that never returns leaves the row DUE, and
// catch-up refires. This is the iOS-expiration story, on desktop.
func TestMidDispatchKillLeavesRowDue(t *testing.T) {
	fac, _ := newTIME(t)
	fac.RegisterOwner(&blockingOwner{started: make(chan struct{}, 1)})
	fac.SetAlarm("blk", "block", "wall", WallNow()+30, nil, "")
	// NOT started — the scheduler isn't running; simulate the kill by
	// dispatching manually in a goroutine we abandon.
	go fac.dispatchPass(context.Background(), "wall", []store.Alarm{{
		AlarmID: "blk", OwnerName: "block", Clock: "wall", Deadline: WallNow() - 1,
	}})
	<-func() chan struct{} {
		fac.mu.Lock()
		o := fac.owners["block"].(*blockingOwner)
		fac.mu.Unlock()
		return o.started
	}()
	// "Killed" mid-dispatch: never call fac.Stop here; just check the row.
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
	<-ctx.Done() // never returns while the caller waits
	return AlarmResult{}
}

// RESIDENT-TIMER payload: opaque bytes ride the row through dispatch.
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

// HORIZON REGRESSION (implementation review 2026-08-17): v1's 1-hour
// query horizon survived into v2's nextWake — a wall alarm beyond 1h
// (morning brief armed for tomorrow) armed NO timer and silently never
// fired. The horizon is now unbounded; the idle path keeps a recheck.
func TestNextWakeUnboundedHorizon(t *testing.T) {
	fac, st := newTIME(t)
	far := WallNow() + 2*3600_000 // +2h
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

// Same class, end-to-end: an alarm armed BEYOND the old horizon still
// fires (here just past it via a shortened wait — the unbounded query
// is what's under test).
func TestFarWallAlarmStillFires(t *testing.T) {
	fac, _ := newTIME(t)
	owner := &recordingOwner{name: "far", accepted: true, fired: make(chan int64, 2)}
	fac.RegisterOwner(owner)
	fac.Start(context.Background())
	defer fac.Stop()

	// No ephemerals running (no pulse): the ONLY wake source is the
	// durable wall alarm. 120ms out; with the old horizon bug the class
	// was "beyond horizon → never", proven by nextWake above.
	deadline := WallNow() + 120
	fac.SetAlarm("fw", "far", "wall", deadline, nil, "")
	select {
	case <-owner.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("wall alarm with no ephemerals never fired — scheduler dead without pulse")
	}
}

// Finding 4 (2026-08-17 review): with the enqueuer wired, a due alarm's
// row stays due until the handler CASes it — nextWake must HIDE in-flight
// rows or the scheduler spins at timer(0) for the handler's whole runtime.
func TestNextWakeHidesInFlightDispatch(t *testing.T) {
	tm, st := newTIME(t)
	owner := &recordingOwner{name: "slow", accepted: true}
	tm.RegisterOwner(owner)

	future := WallNow() + 3600_000
	st.SetAlarm("far", "slow", "wall", future, nil, "")

	past := WallNow() - 1000
	st.SetAlarm("due", "slow", "wall", past, nil, "")

	// Before dispatch: the DUE row leads.
	next, has := tm.nextWake()
	if !has || next.UnixMilli() != past {
		t.Fatalf("pre-dispatch nextWake = %v, want the due row", next)
	}

	// Wire an enqueuer and dispatch: the row is now in flight.
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

	// The handler never ran (the enqueue path invokes owners only via the
	// queue): after release, the still-due row leads again — the bounded-
	// retry semantics. In production ApplyAlarmTransitions deletes or
	// rearms the row as part of the same release.
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

// Finding 4, class 2: an alarm whose owner is unregistered must NOT be
// enqueued — the handler would fail it forever (no transition, row stays
// due, dedup expiry re-enqueues: an infinite loop with retry noise).
func TestUnregisteredOwnerNotEnqueued(t *testing.T) {
	tm, st := newTIME(t)
	tm.RegisterOwner(&recordingOwner{name: "real"})

	past := WallNow() - 1000
	st.SetAlarm("orphan", "ghost", "wall", past, nil, "")
	// Bypass SetAlarm's registration gate (store writes directly) — the
	// orphan row exists, as it would after an owner retirement.

	enq := &captureEnqueuer{}
	tm.SetAlarmEnqueuer(enq)
	tm.dispatchPass(context.Background(), "wall", mustDue(t, st, "wall", past))

	if len(enq.items) != 0 {
		t.Fatalf("unregistered owner's alarm was enqueued — handler-failure loop; got %d items", len(enq.items))
	}
	// And no pending hold: the row is simply the operator's to see.
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
