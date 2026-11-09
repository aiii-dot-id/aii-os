package cognitive

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type failingDueStore struct {
	SetAlarmer
	err error
}

func (s failingDueStore) DueAlarms(string, int64, int) ([]store.Alarm, error) {
	return nil, s.err
}

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

func newTIME(t *testing.T) (*TIME, *store.Store) {
	t.Helper()
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "time.db"))
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
	var repeat any
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
	if err := tm.EnsureAlarm("ghost:ensure", "nobody", "life", 5, nil, ""); err == nil {
		t.Fatal("restart arming must also refuse an unregistered owner")
	}
	tm.RegisterOwner(&recordingOwner{name: "dream", accepted: true})
	if err := tm.SetAlarm("dream", "dream", "life", 5, nil, ""); err != nil {
		t.Fatalf("registered owner must arm: %v", err)
	}
	if err := tm.EnsureAlarm("dream:ensure", "dream", "life", 5, nil, ""); err != nil {
		t.Fatalf("registered owner must ensure a missing alarm: %v", err)
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

// An owner that declines WITH a fault is heard: TIME says the reason
// beside the alarm — once per failed invocation, so a delivery that keeps
// failing is said on every pass — and the transition is a plain decline's:
// the row survives, and an overdue one is deferred by the decline floor
// rather than left due. Driven through the synchronous dispatch path so
// the row is read after settlement, not racing it.
func TestFaultedDeclineIsLoggedWithItsReasonAndTheRowIsPreserved(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	fac, st := newTIME(t)
	faulty := &faultingOwner{reason: errors.New("outbox unavailable: disk full")}
	fac.RegisterOwner(faulty)
	due := WallNow() - 1000 // already due: the decline must defer it, not leave it hot
	if err := fac.SetAlarm("fo", "faulty", "wall", due, nil, ""); err != nil {
		t.Fatal(err)
	}
	rowBefore := alarmByID(t, st, "fo", due)

	before := WallNow()
	fac.dispatchPass(context.Background(), "wall", []store.Alarm{rowBefore})
	after := WallNow()

	if n := strings.Count(capture.String(), "outbox unavailable: disk full"); n != 1 {
		t.Fatalf("the reason is said once per failed invocation; said %d times:\n%s", n, capture.String())
	}
	if !capture.Contains(`owner "faulty" could not deliver alarm fo`) {
		t.Fatalf("the log names neither the owner nor the alarm:\n%s", capture.String())
	}
	floor := declinedRetryAfterMS("wall")
	row := alarmByID(t, st, "fo", after+floor)
	if row.Deadline < before+floor || row.Deadline > after+floor {
		t.Fatalf("a declined overdue alarm is deferred by the floor: deadline %d, want within [%d, %d]", row.Deadline, before+floor, after+floor)
	}

	// The next pass fails again and is said again: once PER invocation.
	fac.dispatchPass(context.Background(), "wall", []store.Alarm{row})
	if n := strings.Count(capture.String(), "outbox unavailable: disk full"); n != 2 {
		t.Fatalf("a second failed invocation is said a second time; said %d times", n)
	}
	if again := alarmByID(t, st, "fo", WallNow()+2*floor); again.Deadline < row.Deadline {
		t.Fatalf("the row survived the second decline but its deadline went backwards: %d < %d", again.Deadline, row.Deadline)
	}
}

// alarmByID reads one alarm row due by horizon, or fails: a missing row
// is the retention failure this test exists to catch.
func alarmByID(t *testing.T, st *store.Store, id string, horizon int64) store.Alarm {
	t.Helper()
	alarms, err := st.DueAlarms("wall", horizon, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range alarms {
		if a.AlarmID == id {
			return a
		}
	}
	t.Fatalf("alarm %s is not due by %d — the row was not preserved", id, horizon)
	return store.Alarm{}
}

type faultingOwner struct{ reason error }

func (f *faultingOwner) Name() string { return "faulty" }
func (f *faultingOwner) OnAlarm(ctx context.Context, id, clock string, deadline int64, payload string) AlarmResult {
	return AlarmResult{Err: f.reason}
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
		t.Fatal("mid-dispatch kill must leave the row DUE for catch-up refire")
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
	tm.mu.Lock()
	dueToken := tm.pendingDispatch["due"].token
	tm.mu.Unlock()
	tm.ClearPendingDispatch("due", dueToken)
	next, has = tm.nextWake()
	if !has || next.UnixMilli() != past {
		t.Fatalf("post-release nextWake = %v, want the due row again (retry semantics)", next)
	}
}

type captureEnqueuer struct{ items []store.Alarm }

func (c *captureEnqueuer) EnqueueAlarm(a store.Alarm, token uint64) error {
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
}

// A DUE ALARM WITH NO OWNER DOES NOT SPIN THE SCHEDULER. The row was left
// with no hold, so its past deadline was the next wake: a zero timer, the
// same row found due, one WARN line, and round again without pause. It is
// held as a row in flight is held, the operator is told of it once, and a
// row behind it still has its turn.
func TestADueAlarmWithNoOwnerDoesNotSpinTheScheduler(t *testing.T) {
	tm, st := newTIME(t)
	tm.RegisterOwner(&recordingOwner{name: "real"})
	past := WallNow() - 1000
	st.SetAlarm("orphan", "ghost", "wall", past, nil, "")
	tm.SetAlarmEnqueuer(&captureEnqueuer{})

	buf := logsink.CaptureForTest(t)
	for i := 0; i < 3; i++ {
		tm.dispatchPass(context.Background(), "wall", mustDue(t, st, "wall", past))
	}
	at, has := tm.nextWakeIncludingEphemerals(false)
	if !has || !at.After(time.Now().Add(pendingDispatchRetryAfter/2)) {
		t.Fatalf("THE SCHEDULER WAKES AT ONCE FOR A ROW NOBODY OWNS: next wake %v (has %v), now %v", at, has, time.Now())
	}
	if n := strings.Count(buf.String(), "has unregistered owner ghost"); n != 1 {
		t.Errorf("the operator is told %d times of the row with no owner; want once:\n%s", n, buf.String())
	}

	// A registered owner's alarm, due sooner than the hold ends, is the
	// next wake: the held row hides only itself.
	soon := WallNow() + 2000
	if err := tm.SetAlarm("mine", "real", "wall", soon, nil, ""); err != nil {
		t.Fatal(err)
	}
	if at, has := tm.nextWakeIncludingEphemerals(false); !has || at.UnixMilli() != soon {
		t.Fatalf("a row behind the held one lost its turn: next wake %v, want %v", at.UnixMilli(), soon)
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

// heldOwner declines because the identity is in a turn. It is deliberately
// not recordingOwner: Deferred is the whole point of these tests, and a
// shared double that could return it would make the flag look exercised by
// tests that never set it.
// heldOwner declines because a turn holds, and keeps declining until it is
// released. ONE OWNER CARRIES BOTH HALVES of the lifecycle test (host
// 0630, ruling 3): the row is not reset and the owner is not swapped, so
// the decline and the later run are the same alarm's behaviour and not
// two different fixtures agreeing with each other.
type heldOwner struct {
	name string
	held bool
}

func (h *heldOwner) Name() string { return h.name }
func (h *heldOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	if h.held {
		return AlarmResult{Deferred: true}
	}
	return AlarmResult{Accepted: true}
}

// A RECURRING TICK THE RHYTHM DECLINES BECAUSE A TURN HOLDS KEEPS ITS
// DEADLINE. Before this, applyTransitions caught a declined recurring tick
// in the same branch as a run and rearmed it a whole period on, so an
// operator's "run their next pass now" that met a turn was answered a
// period later, and the page's "Asked at {time}" stood until then with no
// pass in the record.
// TestHeldRecurringTickIsRetriedAtTheFloorNotThePeriod asserts the bound
// the host named: a held tick is retried at the anti-spin FLOOR, never
// rearmed at now + period. "Under an hour" would accept a nearly full
// period, so the bound is stated explicitly.
func TestHeldRecurringTickIsRetriedAtTheFloorNotThePeriod(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000) // one hour
	tm.RegisterOwner(&heldOwner{name: "held", held: true})
	if err := st.SetAlarm("held", "held", "wall", 1, &period, ""); err != nil {
		t.Fatal(err)
	}

	before := WallNow()
	tm.EvaluateAll(context.Background())

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatalf("a held recurring alarm must SURVIVE the firing: %v", err)
	}
	if deadline >= before+period {
		t.Fatalf("a held tick was rearmed a whole period on: deadline %d >= %d+%d. "+
			"The cadence continues from the pass that RAN, not from an attempt that was held.",
			deadline, before, period)
	}
	if deadline < before {
		t.Fatalf("a held tick was moved backwards: deadline %d < %d", deadline, before)
	}
	// THE BOUND ITSELF, not "less than an hour": the retry floor.
	floor := declinedRetryAfterMS("wall")
	if deadline > before+floor+1000 {
		t.Fatalf("a held tick was deferred past the retry floor: deadline %d > %d+%d (+1s slack). "+
			"A held tick is retried at the floor, never rearmed at now+period.",
			deadline, before, floor)
	}
}

// TestAHeldTickIsRetriedAtTheFloorThenRearmsFromTheRun is the one
// lifecycle the host asked for (0630, ruling 3): ONE ROW carried through,
// never reset and with no owner swap between halves, so neither fact can
// pass by being a different alarm's behaviour:
//
//	press -> the owner declines (a turn holds) -> retried at the floor
//	-> the owner runs -> rearmed at run + period.
//
// The press is real: it moves the deadline to now, which is what makes
// the decline a DECLINE OF AN ASKED-FOR PASS rather than an ordinary tick.
func TestAHeldTickIsRetriedAtTheFloorThenRearmsFromTheRun(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	// Due an hour out: not due, so the only thing that can make it fire is
	// the press.
	if err := st.SetAlarm("held", "held", "wall", WallNow()+period, &period, ""); err != nil {
		t.Fatal(err)
	}
	tm.RegisterOwner(&heldOwner{name: "held", held: true})

	// 1. The press brings it forward.
	asked := WallNow()
	press, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward: %v", err)
	}
	if !press.Moved || press.Reason != "" {
		t.Fatalf("the press must move a future recurring alarm: %+v", press)
	}

	// 2. It meets a turn and declines: retried at the FLOOR, not a period.
	before := WallNow()
	tm.EvaluateAll(context.Background())
	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatalf("a held recurring alarm must SURVIVE the firing: %v", err)
	}
	floor := declinedRetryAfterMS("wall")
	if deadline > before+floor+1000 {
		t.Fatalf("held step deferred past the floor: deadline %d > %d+%d. A held tick keeps its deadline; "+
			"it is not rearmed a whole period on.", deadline, before, floor)
	}
	if deadline < asked {
		t.Fatalf("held step moved the deadline before the press: %d < %d", deadline, asked)
	}

	// 3. The turn ends and the SAME row runs. No SetAlarm, no owner swap:
	//    the owner's decline is a field it flips, so the row is carried.
	held := tm.owners["held"].(*heldOwner)
	held.held = false
	// THE ROW IS NOT RESET (host 0630, ruling 3). Its deadline is still the
	// floor the held tick left it at, so the due-walk has nothing to hand
	// us yet; rewriting the deadline to 1 would make the test pass by
	// changing the thing under test. Dispatching the row directly runs it
	// through the same inline path and the same transition law, and the
	// assertion below proves the row carried over untouched.
	row, ok, err := st.AlarmByID("held")
	if err != nil || !ok {
		t.Fatalf("read the held row: ok=%v err=%v", ok, err)
	}
	if row.Deadline != deadline {
		t.Fatalf("the row was reset between the held tick and the run: %d != %d", row.Deadline, deadline)
	}
	// The same row, never rewritten: the wall clock is moved past the floor
	// and the due walk itself finds the row eligible and runs it.
	later := time.UnixMilli(deadline + 1_000)
	wallNowFn = func() time.Time { return later }
	t.Cleanup(func() { wallNowFn = time.Now })
	ran := WallNow()
	tm.EvaluateAll(context.Background())

	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatalf("an accepted recurring alarm must SURVIVE the firing: %v", err)
	}
	if deadline < ran+period {
		t.Fatalf("after a run the alarm must be rearmed a period from the RUN: %d < %d+%d. "+
			"The cadence continues from the pass that ran.", deadline, ran, period)
	}
}

// TestAPressSurvivesARestartBeforeTheTick is the restart case host 0630
// ruling 3 asks for as its own: the press moves a durable deadline, the
// daemon is replaced before any tick runs, and the new process finds the
// row due and runs the pass. Nothing about the ask lived in the old
// process's memory, which is why the door moves the row rather than
// holding a flag.
func TestAPressSurvivesARestartBeforeTheTick(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	if err := st.SetAlarm("held", "held", "wall", WallNow()+period, &period, ""); err != nil {
		t.Fatal(err)
	}
	tm.RegisterOwner(&heldOwner{name: "held", held: false})

	press, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward: %v", err)
	}
	if !press.Moved || press.Reason != "" {
		t.Fatalf("the press must move a future recurring alarm: %+v", press)
	}

	// The daemon is replaced: a new TIME over the same store, with no
	// memory of the press. Its owner is a fresh one that reports firing.
	fired := make(chan int64, 1)
	tm2 := NewTIME(st, st)
	tm2.RegisterOwner(&recordingOwner{name: "held", accepted: true, fired: fired})

	row, ok, err := st.AlarmByID("held")
	if err != nil || !ok {
		t.Fatalf("read the row after the restart: ok=%v err=%v", ok, err)
	}
	if row.Deadline != press.To {
		t.Fatalf("the ask did not survive the restart: the durable deadline %d is not the one the press wrote, %d", row.Deadline, press.To)
	}
	if row.Deadline > WallNow() {
		t.Fatalf("the moved row must be due at the later clock: deadline %d > now %d", row.Deadline, WallNow())
	}

	tm2.EvaluateAll(context.Background())

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("the first tick after the restart did not run the pass the press asked for")
	}
}

// TestAPressClaimsTheRowSoATickCannotDispatchIt is a review's 0439 finding 1
// in its exact interval. The press's claim and the tick's mark are one map
// entry under t.mu, so a claimed row is not dispatched and no owner runs
// against a row whose deadline is being moved.
func TestAPressClaimsTheRowSoATickCannotDispatchIt(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	if err := st.SetAlarm("held", "held", "wall", 1, &period, ""); err != nil {
		t.Fatal(err)
	}
	owner := &recordingOwner{name: "held", accepted: true, fired: make(chan int64, 1)}
	tm.RegisterOwner(owner)

	alarm, ok, err := st.AlarmByID("held")
	if err != nil || !ok {
		t.Fatalf("read alarm: ok=%v err=%v", ok, err)
	}

	// The press's claim, taken exactly as BringAlarmForward takes it.
	// Take the claim exactly as BringAlarmForward takes it: one locked
	// act, so the check and the write cannot be separated.
	tm.mu.Lock()
	tm.acquireHoldLocked("held")
	tm.mu.Unlock()

	tm.dispatchAlarm(context.Background(), alarm)

	select {
	case <-owner.fired:
		t.Fatal("a tick dispatched a row the press had claimed: an owner ran while the press was moving that row's deadline")
	default:
	}
}

// TestAPressReleaseDoesNotDropARetakenHold is the release half of the
// host's 0630 ruling 1. A release must drop only the reservation it took.
// A hold that expired and was retaken by someone else carries a different
// token, and the press's release must leave it standing — otherwise a
// press that finished late would clear a RUNNING dispatch's hold and let
// the same row be dispatched a second time against a moved deadline.
func TestAPressReleaseDoesNotDropARetakenHold(t *testing.T) {
	tm, _ := newTIME(t)

	tm.mu.Lock()
	mine, took := tm.acquireHoldLocked("held")
	tm.mu.Unlock()
	if !took {
		t.Fatal("the first claim must succeed on a free row")
	}

	// That hold expires and the row is claimed again, so the entry now
	// belongs to someone else.
	tm.mu.Lock()
	delete(tm.pendingDispatch, "held")
	theirs, retook := tm.acquireHoldLocked("held")
	tm.mu.Unlock()
	if !retook {
		t.Fatal("the row was free; the second claim must succeed")
	}
	if theirs == mine {
		t.Fatalf("the retaken hold reused the first token %d; the tokens must differ", mine)
	}

	// The late press releases ITS token and nothing else.
	tm.releaseHold("held", mine)

	tm.mu.Lock()
	held, ok := tm.pendingDispatch["held"]
	tm.mu.Unlock()
	if !ok {
		t.Fatal("a press release dropped a hold it did not own: a running dispatch's reservation " +
			"was cleared, so the row can be dispatched a second time against a moved deadline")
	}
	if held.token != theirs {
		t.Fatalf("the surviving hold is token %d, want the retaken %d", held.token, theirs)
	}
}

// TestAPressIsRefusedWhileAPassIsInFlight is the other direction of the
// same hold, and it is typed so the page never has to match prose.
func TestAPressIsRefusedWhileAPassIsInFlight(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	if err := st.SetAlarm("held", "held", "wall", WallNow()+period, &period, ""); err != nil {
		t.Fatal(err)
	}
	// Take the claim exactly as BringAlarmForward takes it: one locked
	// act, so the check and the write cannot be separated.
	tm.mu.Lock()
	tm.acquireHoldLocked("held")
	tm.mu.Unlock()

	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward: %v", err)
	}
	if res.Moved || res.Reason != PressInFlight {
		t.Fatalf("a press under a running pass must be refused %q, not moved: %+v", PressInFlight, res)
	}
}

// TestAPressOnAnAlreadyDueAlarmIsRefusedNotSucceeded pins the ruling that
// already_due is a refusal: nothing moved, and a caller that read it as a
// success would show the operator an ask that was never made.
func TestAPressOnAnAlreadyDueAlarmIsRefusedNotSucceeded(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	if err := st.SetAlarm("held", "held", "wall", 1, &period, ""); err != nil {
		t.Fatal(err)
	}
	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward: %v", err)
	}
	if res.Moved || res.Reason != PressAlreadyDue {
		t.Fatalf("an already-due alarm must be refused %q, not reported as a move: %+v", PressAlreadyDue, res)
	}
}

// The other side of the same law: a run still rearms at run + period.
// Without this, the test above would pass on an implementation that never
// rearms at all.
func TestAcceptedRecurringTickRearmsAtRunPlusPeriod(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	tm.RegisterOwner(&recordingOwner{name: "runs", accepted: true})
	if err := st.SetAlarm("runs", "runs", "wall", 1, &period, ""); err != nil {
		t.Fatal(err)
	}

	before := WallNow()
	tm.EvaluateAll(context.Background())

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'runs'`).Scan(&deadline); err != nil {
		t.Fatalf("an accepted recurring alarm must SURVIVE the firing: %v", err)
	}
	if deadline < before+period {
		t.Fatalf("a run did not rearm at run+period: deadline %d < %d+%d", deadline, before, period)
	}
}

// BringAlarmForward is the whole of what the page's press does.
func TestBringAlarmForwardMovesTheDeadlineToNow(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	future := WallNow() + period
	if err := st.SetAlarm("held", "held", "wall", future, &period, ""); err != nil {
		t.Fatal(err)
	}

	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward: %v", err)
	}
	if !res.Moved {
		t.Fatalf("the press did not move the deadline: %+v", res)
	}
	if res.From != future {
		t.Fatalf("From = %d, want the deadline it read, %d", res.From, future)
	}

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != res.To {
		t.Fatalf("row deadline %d does not match what the press reported, %d", deadline, res.To)
	}
	if deadline > WallNow() {
		t.Fatalf("the deadline was not brought to now: %d > %d", deadline, WallNow())
	}
}

// The refusals the spec names, each read before the deadline moves.
func TestBringAlarmForwardRefusesUnderSafe(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	future := WallNow() + period
	if err := st.SetAlarm("held", "held", "wall", future, &period, ""); err != nil {
		t.Fatal(err)
	}
	tm.safeSource = func() bool { return true }

	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward under SAFE: %v", err)
	}
	if res.Moved {
		t.Fatalf("the press moved a deadline under SAFE: %+v", res)
	}
	if res.Reason != PressSafe {
		t.Fatalf("Reason = %q, want SAFE", res.Reason)
	}

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != future {
		t.Fatalf("the deadline moved under SAFE: %d, want %d", deadline, future)
	}
}

func TestBringAlarmForwardRefusesWhileAPassIsInFlight(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	future := WallNow() + period
	if err := st.SetAlarm("held", "held", "wall", future, &period, ""); err != nil {
		t.Fatal(err)
	}
	// Take the claim exactly as BringAlarmForward takes it: one locked
	// act, so the check and the write cannot be separated.
	tm.mu.Lock()
	tm.acquireHoldLocked("held")
	tm.mu.Unlock()

	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward while in flight: %v", err)
	}
	if res.Moved {
		t.Fatalf("the press moved a deadline underneath a running pass: %+v", res)
	}
	if res.Reason != PressInFlight {
		t.Fatalf("Reason = %q, want the in-flight refusal", res.Reason)
	}

	var deadline int64
	if err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = 'held'`).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	if deadline != future {
		t.Fatalf("the deadline moved while a pass was in flight: %d, want %d", deadline, future)
	}
}

// A paused daemon dispatches no pass, so the press is refused "paused"
// rather than moving a deadline nothing will take (host ruling
// 2026-10-31 07:48Z). The row is untouched.
func TestBringAlarmForwardRefusesWhilePaused(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	future := WallNow() + period
	if err := st.SetAlarm("held", "held", "wall", future, &period, ""); err != nil {
		t.Fatal(err)
	}
	g := quiesce.NewGate()
	g.Pause()
	tm.SetQuiesceGate(g)

	res, err := tm.BringAlarmForward("held")
	if err != nil {
		t.Fatalf("BringAlarmForward while paused: %v", err)
	}
	if res.Moved || res.Reason != PressPaused {
		t.Fatalf("the press must be refused %q while the daemon is paused, not %+v", PressPaused, res)
	}
	row, ok, err := st.AlarmByID("held")
	if err != nil || !ok {
		t.Fatalf("alarm row after the refusal: ok=%v err=%v", ok, err)
	}
	if row.Deadline != future {
		t.Fatalf("a refused press must leave the deadline where it was: %d, want %d", row.Deadline, future)
	}
}
