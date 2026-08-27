// Package cognitive implements the silent substrate of AII OS identity.
//
// TIME, the five facilities, and the pulse run autonomously. The resident
// never sees counts, backlogs, or chores. "No process may require me to
// remember it exists."
package cognitive

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Facility cadences, in life-clock pulses — ONE source for constructor
// defaults AND app.go's alarm arming (audit 2026-08-16: the numbers were
// duplicated in two files, so config changes silently didn't affect
// scheduling).
const (
	DreamCadence          int64 = 5
	ConsolidateCadence    int64 = 10
	SelfModelCadence      int64 = 50
	IdentityReviewCadence int64 = 100
)

// AlarmOwner handles dispatch for a named alarm.
// Owners know what an alarm means; TIME knows when it fires.
type AlarmOwner interface {
	Name() string
	OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult
}

// AlarmResult is returned by an alarm owner after dispatch.
type AlarmResult struct {
	Accepted     bool   // Did the owner accept this firing?
	NextDeadline *int64 // If non-nil, reschedule to this deadline (same clock domain)
}

// SetAlarmer is the interface TIME uses to read/write alarms.
// The store implements this.
type SetAlarmer interface {
	SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error
	CancelAlarm(ownerName, alarmID string) error
	DueAlarms(clock string, nowOrLess int64, limit int) ([]store.Alarm, error)
	DeleteAlarm(alarmID string) error
	// Compare-and-swap transitions — the dispatch guard (canon #11):
	// a stale firing attempt (row replaced since the due read) must not
	// apply its transition to the new row.
	UpdateAlarmDeadlineCAS(alarmID string, expectedDeadline, newDeadline int64) (bool, error)
	DeleteAlarmCAS(alarmID string, expectedDeadline int64) (bool, error)
}

// LifetimeTicker is the interface for life-clock operations.
type LifetimeTicker interface {
	LifetimeTicks() (int64, error)
	IncrementLifetimeTicks() error
}

// PulseSource is what TIME drives — the heartbeat reduced to its meaning
// (2026-08-17 inversion: TIME is core, the heartbeat is one cadence it
// drives). TIME never knows WHY a session is live.
type PulseSource interface {
	Interval() time.Duration
	Live() bool // presence gate: only accepted pulses advance the life clock
}

// PlatformWake is what a host platform provides: ONE next-wake slot
// keyed to TIME's heap minimum. The app wakes, runs the Go runtime, and
// TIME's catch-up fires EVERYTHING due. Desktop implements it as a no-op
// (the default). TIME never knows which world it's in.
type PlatformWake interface {
	WakeAt(at time.Time) error // earliest next wake; supersedes prior
	WakeClear()                // nothing worth waking for
}

// NoopWake is the desktop default — process timers are the wake source.
type NoopWake struct{}

func (NoopWake) WakeAt(time.Time) error { return nil }
func (NoopWake) WakeClear()             {}

// --- the ephemeral timer heap ---

type ephemeral struct {
	id       string
	next     time.Time
	interval time.Duration // 0 = one-shot
	fn       func()
	index    int
}

type timerHeap []*ephemeral

func (h timerHeap) Len() int            { return len(h) }
func (h timerHeap) Less(i, j int) bool  { return h[i].next.Before(h[j].next) }
func (h timerHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *timerHeap) Push(x interface{}) { e := x.(*ephemeral); e.index = len(*h); *h = append(*h, e) }
func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// TIME is the core timer facility: one scheduler goroutine, one heap,
// one timer. Three caller classes (facilities: durable cadences;
// substrate: ephemeral machinery; resident: requested alarms via the
// timer verb, to come), two durability classes (durable DB rows /
// ephemeral heap entries), two clock domains (life/wall).
//
// DISPATCH LAW (TIME_FACILITIES.md §4–5, unchanged by v2):
//   - Due selection is bounded and ordered by (deadline, alarm_id).
//   - Owners are invoked OUTSIDE the state lock; a panicking owner is
//     CONTAINED (recovered → declined-without-deadline, row preserved,
//     retried later, logged loudly) — the clockwork survives its
//     consumers' bugs (v2: P5 made mechanical).
//   - Accepted one-shot: DELETE — or reschedule via NextDeadline (the
//     daily-brief extension). Accepted recurring: rearm at
//     currentClock + repeat (no catch-up burst).
//   - Declined with NextDeadline: reschedule. Declined recurring
//     without: rearm one period (starvation guard).
//   - Every transition is CAS on the snapshot deadline (canon #11).
//
// MOBILE: process timers are a foreground truth. Durable rows +
// EvaluateAll catch-up (canon #13) are the correctness floor — late,
// never lost. PlatformWake (one slot, the heap minimum) is opportunistic
// acceleration from the OS scheduler.
type TIME struct {
	store    SetAlarmer
	lifetime LifetimeTicker

	mu      sync.Mutex // guards owners, lifeClock, heap, started, stopped, pulse, wake
	owners  map[string]AlarmOwner
	heap    timerHeap
	started bool
	stopped bool
	// dead marks ephemeral ids cancelled while their callback was
	// mid-run (the popped entry must not resurrect on re-arm).
	dead map[string]bool

	lifeClock int64
	pulse     PulseSource
	wake      PlatformWake
	gate      *quiesce.Gate // background-metabolism governor (nil = always-on); guarded by mu

	// pendingDispatch: durable alarms whose firing is IN FLIGHT through
	// the work queue (enqueued, handler not yet transitioned). nextWake
	// must not spin on their still-due rows: the row stays due BY DESIGN
	// until the handler's CAS. Entries carry the enqueue time; older
	// than pendingDispatchRetryAfter they become re-eligible (enqueue
	// failed or the item died without transition — bounded retry).
	pendingDispatch map[string]time.Time
	safeSource      func() bool // SAFE advances no clock (canon TIME/SAFE); wired by the app

	dispatchMu sync.Mutex // serializes dispatch passes

	timerCtx    context.Context
	timerCancel context.CancelFunc
	resched     chan struct{}
	stopCh      chan struct{}
	runWG       sync.WaitGroup

	// enqueue: when set, dispatch ENQUEUES durable work instead of
	// invoking owners inline (WORK_QUEUE.md §2.4). The double-fire
	// window retires by construction: dedup key = alarm_id+deadline.
	enqueue AlarmEnqueuer
}

// AlarmEnqueuer routes alarm dispatch into the durable work queue.
type AlarmEnqueuer interface {
	EnqueueAlarm(alarm store.Alarm) error
}

// NewTIME creates the core timer facility (not yet scheduling — call
// Start to run the scheduler loop).
func NewTIME(store SetAlarmer, lifetime LifetimeTicker) *TIME {
	t := &TIME{
		store:           store,
		lifetime:        lifetime,
		owners:          make(map[string]AlarmOwner),
		dead:            make(map[string]bool),
		pendingDispatch: make(map[string]time.Time),
		resched:         make(chan struct{}, 1),
		stopCh:          make(chan struct{}),
	}
	if ticks, err := lifetime.LifetimeTicks(); err == nil {
		t.lifeClock = ticks
	}
	return t
}

// SetAlarmEnqueuer wires durable dispatch (nil = inline, tests).
func (t *TIME) SetAlarmEnqueuer(ae AlarmEnqueuer) {
	t.mu.Lock()
	t.enqueue = ae
	t.mu.Unlock()
}

// RegisterOwner registers an alarm owner.
func (t *TIME) RegisterOwner(owner AlarmOwner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.owners[owner.Name()] = owner
}

// SetAlarm arms or replaces a durable alarm by (owner_name, alarm_id).
// Accepts only a REGISTERED owner (canon §3); a replace never changes
// the alarm's owner (canon #10). payload is opaque to TIME.
func (t *TIME) SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	t.mu.Lock()
	_, registered := t.owners[ownerName]
	t.mu.Unlock()
	if !registered {
		return fmt.Errorf("set alarm %s: owner %q is not registered — TIME arms alarms only for owners that can dispatch them", alarmID, ownerName)
	}
	if err := t.store.SetAlarm(alarmID, ownerName, clock, deadline, repeatEvery, payload); err != nil {
		return err
	}
	if clock == "wall" {
		t.signalResched() // an earlier wall deadline may now lead the heap
	}
	return nil
}

// CancelAlarm removes an alarm by (owner_name, alarm_id). Set, cancel,
// and completion all re-arm the earliest deadline (canon
// TIME_FACILITIES): a cancelled leader otherwise leaves the process
// timer and the OS slot pointed at a dead target — a needless wake now,
// a late recompute after.
func (t *TIME) CancelAlarm(ownerName, alarmID string) error {
	if err := t.store.CancelAlarm(ownerName, alarmID); err != nil {
		return err
	}
	t.signalResched()
	return nil
}

// DeleteLegacyAlarm removes a stale row by id — maintenance for rows
// left by retired mechanisms. TIME-owned state stays TIME-mutated
// (canon #14); the sanctioned exception, reason at the call site.
func (t *TIME) DeleteLegacyAlarm(alarmID, reason string) error {
	log.Printf("TIME: deleting legacy alarm %s (%s)", alarmID, reason)
	return t.store.DeleteAlarm(alarmID)
}

// SetPlatformWake installs the host wake implementation (nil → desktop
// no-op). Registered before Start. Replacing or detaching deliberately
// does NOT WakeClear the old slot: on Android the shell detaches
// exactly when the Activity dies while the runtime lives on — that
// standing OS alarm is the identity's only way back. Both shells arm
// one idempotent slot (same PendingIntent / same BGTask identifier), so
// the replacement's next arm supersedes rather than leaks.
func (t *TIME) SetPlatformWake(w PlatformWake) {
	if w == nil {
		w = NoopWake{}
	}
	t.mu.Lock()
	t.wake = w
	t.mu.Unlock()
}

// SetQuiesceGate wires the background-metabolism governor (quiesce,
// 2026-08-19 — a phone died to teach it), registered before Start like
// SetPlatformWake; nil = desktop always-on. While the gate is paused
// the scheduler PARKS: no process timer armed at all — the heartbeat,
// the 30s witness probe, and the metabolism rhythm all wake through
// this one timer, so one seam parks them all. Arms and alarm writes
// keep landing while parked (heap + DB only); resume recomputes from
// the new now, and everything that became due fires ONCE — ephemerals
// re-arm from now, recurring wall alarms re-arm at currentClock+repeat:
// the existing no-catch-up-burst law IS the at-most-one catch-up. The
// invited wake paths (TimeWake/EvaluateAll — the OS said run) still
// work while parked, and the PlatformWake slot keeps its last-armed
// next-due target: quiesce stops self-inflicted wakeups, not invited
// ones.
func (t *TIME) SetQuiesceGate(g *quiesce.Gate) {
	t.mu.Lock()
	t.gate = g
	t.mu.Unlock()
	// A gate flip must interrupt a pending select: pause stops the armed
	// timer at the loop top, resume unparks. signalResched is built for
	// exactly this poke (non-blocking, coalescing).
	g.OnTransition(t.signalResched)
}

// quiesced reports whether the scheduler should park (nil gate: never).
func (t *TIME) quiesced() bool {
	t.mu.Lock()
	g := t.gate
	t.mu.Unlock()
	return g.Paused()
}

// --- ephemeral timers (substrate tier: process-lifetime, never persisted) ---

// After schedules a one-shot process-lifetime timer. Duplicate id
// replaces (last-writer-wins).
func (t *TIME) After(id string, delay time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(delay), fn: fn})
}

// Every schedules a recurring process-lifetime timer. A PANICKING
// recurring callback is auto-cancelled (a panicking Every must not
// panic-loop on its own interval).
func (t *TIME) Every(id string, interval time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(interval), interval: interval, fn: fn})
}

// Cancel removes an ephemeral timer. If the callback is mid-run (entry
// popped, re-arm pending), a tombstone prevents resurrection; a fresh
// arm clears it.
func (t *TIME) Cancel(id string) {
	t.mu.Lock()
	for i, e := range t.heap {
		if e.id == id {
			heap.Remove(&t.heap, i)
			break
		}
	}
	t.dead[id] = true
	t.mu.Unlock()
	t.signalResched()
}

func (t *TIME) armEphemeral(e *ephemeral) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	for i, old := range t.heap {
		if old.id == e.id {
			heap.Remove(&t.heap, i)
			break
		}
	}
	delete(t.dead, e.id) // a fresh arm revives the id
	heap.Push(&t.heap, e)
	t.mu.Unlock()
	t.signalResched()
}

// --- pulse intake ---

// StartHeartbeat arms the pulse as an internal recurring ephemeral. On
// each fire TIME asks the source Live(); accepted → life clock advances
// and due life alarms dispatch. Presence, not uptime (canon §2).
func (t *TIME) StartHeartbeat(src PulseSource) {
	t.mu.Lock()
	t.pulse = src
	interval := src.Interval()
	if interval <= 0 {
		interval = 300 * time.Second
	}
	t.mu.Unlock()
	t.Every("pulse:heartbeat", interval, t.pulseFire)
}

func (t *TIME) pulseFire() {
	t.mu.Lock()
	src := t.pulse
	ctx := t.timerCtx
	safe := t.safeSource
	t.mu.Unlock()
	if src == nil || !src.Live() {
		return // pulse not accepted; the life clock is lived presence
	}
	// Canon (TIME/SAFE): "Safe mode advances no clock, dispatches
	// nothing; recovery has no catch-up burst." Lived time must not
	// accumulate — and beliefs must not mature toward trusted — while
	// identity integrity is unverified.
	if safe != nil && safe() {
		return
	}
	if err := t.AdvanceLifeClock(ctx); err != nil {
		log.Printf("TIME: life clock advance failed: %v", err)
	}
}

// SetSafeSource wires the SAFE-mode check: an accepted pulse in SAFE
// advances nothing.
func (t *TIME) SetSafeSource(fn func() bool) {
	t.mu.Lock()
	t.safeSource = fn
	t.mu.Unlock()
}

// AdvanceLifeClock increments the life clock by 1 and dispatches due
// life alarms (the semantic unit — a life tick only exists when a pulse
// is accepted; v2: dispatch no longer RIDES the pulse loop, the
// scheduler IS the loop).
func (t *TIME) AdvanceLifeClock(ctx context.Context) error {
	// S5 (external review): the store call runs OUTSIDE t.mu. It crosses
	// into consumer code that can fail or panic, and unwinding with the
	// TIME mutex held wedges the entire clockwork the moment any recover
	// path calls back into TIME — contradicting "clockwork survives its
	// consumers' bugs". t.mu guards only TIME's own counter.
	if err := t.lifetime.IncrementLifetimeTicks(); err != nil {
		return fmt.Errorf("life clock increment failed: %w", err)
	}
	t.mu.Lock()
	t.lifeClock++
	life := t.lifeClock
	t.mu.Unlock()

	alarms, err := t.store.DueAlarms("life", life, 100)
	if err != nil {
		return err
	}
	t.dispatchPass(ctx, "life", alarms)
	return nil
}

// LifeClock returns the current life-clock count.
func (t *TIME) LifeClock() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lifeClock
}

// WallNow returns current UTC milliseconds.
func WallNow() int64 {
	return time.Now().UTC().UnixMilli()
}

// Start runs the scheduler loop (the single driver).
func (t *TIME) Start(ctx context.Context) {
	t.mu.Lock()
	if t.started || t.stopped {
		t.mu.Unlock()
		return
	}
	timerCtx, cancel := context.WithCancel(ctx)
	t.started = true
	t.timerCtx = timerCtx
	t.timerCancel = cancel
	t.runWG.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.runWG.Done()
		t.schedulerLoop(timerCtx)
	}()
}

// Stop shuts down the scheduler. Idempotent, terminal.
func (t *TIME) Stop() {
	t.mu.Lock()
	first := !t.stopped
	if first {
		t.stopped = true
		close(t.stopCh)
		if t.timerCancel != nil {
			t.timerCancel()
		}
	}
	w := t.wake
	t.mu.Unlock()
	t.runWG.Wait()
	// A stopped runtime must not be woken by the OS for nothing —
	// clear the registered slot (best effort; the shell may be gone).
	// Process DEATH still leaves the alarm standing, and that is right:
	// the receiver's nil-runtime guard absorbs it, and on Android the
	// wake is what restarts the service.
	if first && w != nil {
		w.WakeClear()
	}
}

// TimeWake is the host wake entry (gomobile-exported): the OS woke the
// app for the registered slot. Run catch-up — fires EVERYTHING due —
// and let the scheduler recompute from the new now.
func (t *TIME) TimeWake() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	ctx := t.timerCtx
	t.runWG.Add(1)
	t.mu.Unlock()
	defer t.runWG.Done()
	t.signalResched()
	if ctx != nil {
		if err := t.EvaluateAll(ctx); err != nil {
			log.Printf("TIME: wake catch-up failed: %v", err)
		}
	}
}

// EvaluateAll evaluates due alarms on both clocks — boot/wake recovery:
// an alarm that became due while the system was down fires now, once
// (canon #13). Called BEFORE facilities re-arm.
func (t *TIME) EvaluateAll(ctx context.Context) error {
	t.mu.Lock()
	life := t.lifeClock
	t.mu.Unlock()

	wallDue, wallErr := t.store.DueAlarms("wall", WallNow(), 100)
	if wallErr != nil {
		wallErr = fmt.Errorf("read due wall alarms: %w", wallErr)
	} else {
		t.dispatchPass(ctx, "wall", wallDue)
	}
	lifeDue, lifeErr := t.store.DueAlarms("life", life, 100)
	if lifeErr != nil {
		lifeErr = fmt.Errorf("read due life alarms: %w", lifeErr)
	} else {
		t.dispatchPass(ctx, "life", lifeDue)
	}
	return errors.Join(wallErr, lifeErr)
}

// --- the scheduler loop: one goroutine, one timer, one pass ---

func (t *TIME) schedulerLoop(ctx context.Context) {
	// idleRecheck: when NOTHING is schedulable, wake anyway at this
	// interval — a safety net for store drift and late SetAlarm writes
	// (v1's 60s recheck; the 2026-08-17 review caught its loss: a wall
	// alarm beyond the horizon armed NO timer and never fired).
	const idleRecheck = time.Minute

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		// Quiesce park (2026-08-19, the battery fix): backgrounded means
		// NO process timer at all — not the idleRecheck either (drift
		// insurance can wait for the operator). The gate's transition
		// hook pokes resched, so a pause interrupts the armed select
		// above within one pass, and a resume lands here and falls
		// through to recompute from the new now — everything due fires
		// once, TIME's own catch-up law.
		if t.quiesced() {
			// Parked means no PROCESS timer. The platform slot is a
			// different animal: arming it is a callback into the shell
			// (AlarmManager.set / BGTaskScheduler.submit), not a wakeup
			// source in this process — battery-neutral, and the only
			// way a deadline written while backgrounded ever reaches
			// the OS. This re-arm is also what makes background chains
			// self-sustaining: OS wake → TimeWake catch-up → resched
			// poke → land here → arm the next target (external review
			// 2026-08-20; before it, a backgrounded identity's new
			// alarms silently waited for foreground).
			if next, has := t.nextWake(); has {
				t.armPlatformWake(next)
			} else {
				t.clearPlatformWake()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.stopCh:
				return
			case <-t.resched:
				continue // gate flip or alarm write — re-read the state
			}
		}
		next, has := t.nextWake()
		var fire <-chan time.Time
		if has {
			d := time.Until(next)
			if d < 0 {
				d = 0
			}
			timer = time.NewTimer(d)
			fire = timer.C
			t.armPlatformWake(next)
		} else {
			timer = time.NewTimer(idleRecheck)
			fire = timer.C
			t.clearPlatformWake()
		}

		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-t.resched:
			continue // heap or wall alarms changed — recompute minimum
		case now := <-fire:
			t.runDue(ctx, now)
		}
	}
}

// nextWake computes the earliest interesting moment: the minimum of the
// ephemeral heap and the earliest durable wall deadline. (Life alarms
// become due only when a pulse advances the clock — the pulse IS their
// timer. Durable wall deadlines live in the DB; we query the earliest.)
func (t *TIME) nextWake() (time.Time, bool) {
	t.mu.Lock()
	var best time.Time
	var has bool
	if len(t.heap) > 0 {
		best = t.heap[0].next
		has = true
	}
	now := time.Now()
	pending := make(map[string]bool, len(t.pendingDispatch))
	for id, since := range t.pendingDispatch {
		retryAt := since.Add(pendingDispatchRetryAfter)
		if !now.Before(retryAt) {
			delete(t.pendingDispatch, id)
			continue
		}
		pending[id] = true
		if !has || retryAt.Before(best) {
			best = retryAt
			has = true
		}
	}
	t.mu.Unlock()

	// UNBOUNDED horizon: the earliest wall alarm, however far out —
	// time.Timer handles multi-hour waits natively. (The 1h horizon was
	// a v1 artifact; with it, an alarm armed for tomorrow-07:00 armed no
	// timer at all — the silent-skip class canon forbids. 2026-08-17
	// implementation review.)
	//
	// Rows IN FLIGHT through the work queue (finding 4) are skipped:
	// their deadline is past but their transition belongs to the handler
	// — waking on them spun the scheduler at timer(0) for the handler's
	// entire runtime. Stale holds (enqueue failed / handler died) expire
	// after pendingDispatchRetryAfter and the row retries.
	alarms, err := t.store.DueAlarms("wall", int64(1)<<62, 16)
	if err != nil {
		log.Printf("TIME: next durable wake unavailable: %v", err)
		return best, has
	}
	for _, a := range alarms {
		if pending[a.AlarmID] {
			continue
		}
		wt := time.UnixMilli(a.Deadline)
		if !has || wt.Before(best) {
			best = wt
			has = true
		}
		break // earliest non-pending row wins (rows are deadline-ordered)
	}
	return best, has
}

// armPlatformWake registers the ONE next-wake slot from outside the
// scheduler goroutine (name fixed 2026-08-17 — it takes the lock
// itself, "Locked" lied).
func (t *TIME) armPlatformWake(at time.Time) {
	t.mu.Lock()
	w := t.wake
	t.mu.Unlock()
	if w != nil {
		if err := w.WakeAt(at); err != nil {
			log.Printf("TIME: platform wake register failed: %v", err)
		}
	}
}

func (t *TIME) clearPlatformWake() {
	t.mu.Lock()
	w := t.wake
	t.mu.Unlock()
	if w != nil {
		w.WakeClear()
	}
}

func (t *TIME) runDue(ctx context.Context, now time.Time) {
	// 1. Ephemerals whose time came — spawn with containment.
	var due []*ephemeral
	t.mu.Lock()
	for len(t.heap) > 0 && !t.heap[0].next.After(now) {
		e := heap.Pop(&t.heap).(*ephemeral)
		due = append(due, e)
	}
	t.mu.Unlock()
	for _, e := range due {
		t.mu.Lock()
		if t.stopped {
			t.mu.Unlock()
			break
		}
		t.runWG.Add(1)
		t.mu.Unlock()
		go func() {
			defer t.runWG.Done()
			t.runEphemeral(e)
		}()
	}
	// 2. Durable wall alarms due now.
	if alarms, err := t.store.DueAlarms("wall", now.UnixMilli(), 100); err != nil {
		log.Printf("TIME: durable wall alarm evaluation failed: %v", err)
	} else if len(alarms) > 0 {
		t.dispatchPass(ctx, "wall", alarms)
	}
}

func (t *TIME) runEphemeral(e *ephemeral) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TIME: ephemeral %q PANICKED (contained): %v\n%s", e.id, r, debug.Stack())
			if e.interval > 0 {
				t.Cancel(e.id) // a panicking Every must not panic-loop
			}
		}
	}()
	e.fn()
	if e.interval > 0 {
		t.mu.Lock()
		switch {
		case t.stopped:
			// shutting down — drop
		case t.dead[e.id]:
			// Cancelled while fn ran: consume the tombstone, do not
			// resurrect (finding 5 — the old code re-pushed
			// unconditionally, so Cancel during a callback was a no-op).
			delete(t.dead, e.id)
		default:
			// Re-arm — unless a concurrent arm already re-armed this id
			// (duplicate entries = double-firing).
			rearmed := false
			for _, x := range t.heap {
				if x.id == e.id {
					rearmed = true
					break
				}
			}
			if !rearmed {
				e.next = time.Now().Add(e.interval)
				heap.Push(&t.heap, e)
			}
		}
		t.mu.Unlock()
		t.signalResched()
	}
}

func (t *TIME) signalResched() {
	select {
	case t.resched <- struct{}{}:
	default:
	}
}

// --- dispatch (canon law, v2: payload passes through; panics contained) ---

func (t *TIME) dispatchPass(ctx context.Context, clock string, alarms []store.Alarm) {
	t.dispatchMu.Lock()
	defer t.dispatchMu.Unlock()
	for _, alarm := range alarms {
		t.dispatchAlarm(ctx, alarm)
	}
}

// pendingDispatchRetryAfter bounds how long a dispatched-but-not-yet-
// transitioned alarm hides from nextWake: if the handler died without
// transitioning (crash between enqueue and run, or RunWork errored
// pre-Invoke), the row becomes re-eligible after this delay — bounded
// retry, never silent loss, never a hot spin.
const pendingDispatchRetryAfter = 30 * time.Second

// declinedRetryAfterMS is the floor between a decline-without-deadline
// and the next attempt, in the units of the alarm's own clock. It exists
// only to stop a hot loop, so it is short: an owner that declines because
// the identity is mid-turn should be asked again soon, just not at the
// speed of the scheduler.
func declinedRetryAfterMS(clock string) int64 {
	if clock == "life" {
		return 1 // life ticks are coarse; one tick is the smallest honest defer
	}
	return int64(pendingDispatchRetryAfter / time.Millisecond)
}

func (t *TIME) dispatchAlarm(ctx context.Context, alarm store.Alarm) {
	// v2.1 (WORK_QUEUE.md): dispatch ENQUEUES durable work when the
	// executor is wired — the doing is a durable fact; crash mid-do
	// recovers via lease expiry; duplicate dispatches suppress by dedup
	// key. Owners still run under the queue handler. Unwired (tests),
	// inline invocation remains.
	t.mu.Lock()
	enq := t.enqueue
	owner, ok := t.owners[alarm.OwnerName]
	t.mu.Unlock()
	if enq != nil {
		// Registration FIRST (finding 4): an unregistered owner's work
		// item would fail in the handler forever (error → no transition
		// → row stays due → re-enqueue after dedup expiry → forever).
		// Unregistered rows are logged and left for the operator — the
		// inline path's honest skip, applied before enqueue.
		if !ok {
			log.Printf("TIME: alarm %s has unregistered owner %s — not enqueued (row preserved)", alarm.AlarmID, alarm.OwnerName)
			return
		}
		// Mark in-flight BEFORE the enqueue (S4, external review): a fast
		// handler could complete and clearPendingDispatch BEFORE a
		// post-enqueue mark landed — the mark then created an owner-less
		// hold that nextWake hid for the whole retry window. Mark stays on
		// enqueue failure too: a failed enqueue waits out the bounded
		// retry delay rather than spinning (finding 4's rule).
		t.mu.Lock()
		t.pendingDispatch[alarm.AlarmID] = time.Now()
		t.mu.Unlock()
		if err := enq.EnqueueAlarm(alarm); err != nil {
			log.Printf("TIME: alarm %s enqueue failed (row preserved for retry): %v", alarm.AlarmID, err)
		}
		return
	}
	if !ok {
		log.Printf("TIME: alarm %s has unregistered owner %s — skipping (row preserved)", alarm.AlarmID, alarm.OwnerName)
		return
	}
	result := t.invokeOwner(ctx, owner, alarm)
	t.applyTransitions(alarm, result)
}

// clearPendingDispatch releases an alarm's in-flight hold — called when
// its handler finishes (ApplyAlarmTransitions) or dies before invoking
// the owner (payload error, unregistered owner reached the queue). The
// row re-enters the wake computation immediately.
func (t *TIME) clearPendingDispatch(alarmID string) {
	t.mu.Lock()
	delete(t.pendingDispatch, alarmID)
	t.mu.Unlock()
	t.signalResched()
}

// applyTransitions is the post-dispatch CAS law (shared by inline and
// queued paths — one implementation, zero drift). Clearing the pending-
// dispatch hold is part of the law: the handler is done, the row's next
// state is whatever the CAS produced, and nextWake may see it again.
// applyTransitions settles the alarm row after its owner ran, and
// reports whether the SETTLING failed.
//
// It used to return nothing, so a failed delete or rearm was a log line
// and the caller reported the work DONE. The alarm row then still held
// its old deadline and fired again — the queue said finished, the clock
// disagreed, and nothing recorded the disagreement.
//
// A stale CAS (!ok) is NOT a failure: the row changed since the due
// read, meaning someone else already settled it. Only a real write
// error is one.
func (t *TIME) applyTransitions(alarm store.Alarm, result AlarmResult) error {
	defer t.clearPendingDispatch(alarm.AlarmID)
	var currentClock int64
	if alarm.Clock == "wall" {
		currentClock = WallNow()
	} else {
		currentClock = t.LifeClock()
	}

	switch {
	case result.Accepted && alarm.RepeatEvery != nil:
		return t.applyCAS(alarm, currentClock+*alarm.RepeatEvery)
	case result.Accepted && result.NextDeadline != nil:
		return t.applyCAS(alarm, *result.NextDeadline)
	case result.Accepted:
		ok, err := t.store.DeleteAlarmCAS(alarm.AlarmID, alarm.Deadline)
		if err != nil {
			return fmt.Errorf("alarm %s delete failed (the alarm is still due and will fire again): %w", alarm.AlarmID, err)
		}
		if !ok {
			log.Printf("TIME: alarm %s stale firing (row changed since due read) — delete skipped", alarm.AlarmID)
		}
	case result.NextDeadline != nil:
		return t.applyCAS(alarm, *result.NextDeadline)
	case alarm.RepeatEvery != nil:
		return t.applyCAS(alarm, currentClock+*alarm.RepeatEvery)
	default:
		// PRESERVED MUST NOT MEAN "IMMEDIATELY DUE AGAIN". A declined
		// one-shot keeps its deadline, and if that deadline is already
		// past the row is due the instant this returns — so the owner is
		// asked again, declines again, and the loop runs as fast as the
		// scheduler can wake. Rhythm's own comment says the intent is
		// "TIME fires it again on the next tick"; a stale deadline has no
		// next tick, only now.
		//
		// So a decline with a past deadline is deferred by a floor rather
		// than left due. Nothing is skipped — the alarm still fires, just
		// not thousands of times a minute.
		if currentClock >= alarm.Deadline {
			next := currentClock + declinedRetryAfterMS(alarm.Clock)
			log.Printf("TIME: alarm %s declined without next deadline — deferred to %d (a past deadline is due again immediately)", alarm.AlarmID, next)
			return t.applyCAS(alarm, next)
		}
		log.Printf("TIME: alarm %s declined without next deadline — preserved", alarm.AlarmID)
	}
	return nil
}

// OwnerFor exposes the owner registry (the alarm handler runs owners
// under the executor; TIME keeps the registry).
func (t *TIME) OwnerFor(name string) (AlarmOwner, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o, ok := t.owners[name]
	return o, ok
}

// ClearPendingDispatch releases an in-flight hold when a queued alarm
// item dies BEFORE invoking its owner (payload error, unregistered
// owner) — the queue handler's honest error path.
func (t *TIME) ClearPendingDispatch(alarmID string) { t.clearPendingDispatch(alarmID) }

// InvokeAlarmOwner: contained invocation, exported for the handler.
func (t *TIME) InvokeAlarmOwner(ctx context.Context, owner AlarmOwner, alarm store.Alarm) AlarmResult {
	return t.invokeOwner(ctx, owner, alarm)
}

// ApplyAlarmTransitions: the post-dispatch CAS law, exported for the
// handler (single implementation — inline and queued paths share it).
func (t *TIME) ApplyAlarmTransitions(alarm store.Alarm, result AlarmResult) error {
	return t.applyTransitions(alarm, result)
}

// invokeOwner runs the owner with panic containment: a panicking owner
// is declined-without-deadline (row preserved, retried later) — the
// clockwork survives its consumers' bugs.
func (t *TIME) invokeOwner(ctx context.Context, owner AlarmOwner, alarm store.Alarm) (result AlarmResult) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("TIME: owner %q PANICKED on alarm %s (contained, treated as declined): %v\n%s",
				owner.Name(), alarm.AlarmID, r, debug.Stack())
			result = AlarmResult{} // declined, no deadline → row preserved
		}
	}()
	return owner.OnAlarm(ctx, alarm.AlarmID, alarm.Clock, alarm.Deadline, alarm.Payload)
}

func (t *TIME) applyCAS(alarm store.Alarm, newDeadline int64) error {
	ok, err := t.store.UpdateAlarmDeadlineCAS(alarm.AlarmID, alarm.Deadline, newDeadline)
	if err != nil {
		return fmt.Errorf("alarm %s reschedule failed (the old deadline stands and will fire again): %w", alarm.AlarmID, err)
	}
	if !ok {
		log.Printf("TIME: alarm %s stale firing (row changed since due read) — transition skipped", alarm.AlarmID)
	}
	if alarm.Clock == "wall" {
		t.signalResched()
	}
	return nil
}
