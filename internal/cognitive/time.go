// .
// .
// .
// .
// .
package cognitive

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"runtime/debug"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
const (
	DreamCadence          int64 = 5
	ConsolidateCadence    int64 = 10
	SelfModelCadence      int64 = 50
	IdentityReviewCadence int64 = 100
)

// .
// .
type AlarmOwner interface {
	Name() string
	OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult
}

// .
type AlarmResult struct {
	Accepted     bool
	NextDeadline *int64
}

// .
// .
type SetAlarmer interface {
	SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error
	CancelAlarm(ownerName, alarmID string) error
	DueAlarms(clock string, nowOrLess int64, limit int) ([]store.Alarm, error)
	DeleteAlarm(alarmID string) error
	// .
	// .
	// .
	UpdateAlarmDeadlineCAS(alarmID string, expectedDeadline, newDeadline int64) (bool, error)
	DeleteAlarmCAS(alarmID string, expectedDeadline int64) (bool, error)
}

// .
type LifetimeTicker interface {
	LifetimeTicks() (int64, error)
	IncrementLifetimeTicks() error
}

// .
// .
// .
type PulseSource interface {
	Interval() time.Duration
	Live() bool
}

// .
// .
// .
// .
type PlatformWake interface {
	WakeAt(at time.Time) error
	WakeClear()
}

// .
type NoopWake struct{}

func (NoopWake) WakeAt(time.Time) error { return nil }
func (NoopWake) WakeClear()             {}

// .

type ephemeral struct {
	id       string
	next     time.Time
	interval time.Duration
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type TIME struct {
	store    SetAlarmer
	lifetime LifetimeTicker

	mu     sync.Mutex
	owners map[string]AlarmOwner
	// .
	// .
	onFire  func(owner, alarmID string, accepted bool)
	heap    timerHeap
	started bool
	stopped bool
	// .
	// .
	dead map[string]bool

	lifeClock int64
	pulse     PulseSource
	wake      PlatformWake
	gate      *quiesce.Gate

	// .
	// .
	// .
	// .
	// .
	// .
	pendingDispatch map[string]time.Time
	safeSource      func() bool

	dispatchMu sync.Mutex

	timerCtx    context.Context
	timerCancel context.CancelFunc
	resched     chan struct{}
	stopCh      chan struct{}
	runWG       sync.WaitGroup

	// .
	// .
	// .
	enqueue AlarmEnqueuer
}

// .
type AlarmEnqueuer interface {
	EnqueueAlarm(alarm store.Alarm) error
}

// .
// .
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

// .
func (t *TIME) SetAlarmEnqueuer(ae AlarmEnqueuer) {
	t.mu.Lock()
	t.enqueue = ae
	t.mu.Unlock()
}

// .
func (t *TIME) RegisterOwner(owner AlarmOwner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.owners[owner.Name()] = owner
}

// .
// .
// .
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
		t.signalResched()
	}
	return nil
}

// .
// .
// .
// .
// .
func (t *TIME) CancelAlarm(ownerName, alarmID string) error {
	if err := t.store.CancelAlarm(ownerName, alarmID); err != nil {
		return err
	}
	t.signalResched()
	return nil
}

// .
// .
// .
func (t *TIME) DeleteLegacyAlarm(alarmID, reason string) error {
	logsink.Info("time.decision", "deleting legacy alarm %s (%s)", alarmID, reason)
	return t.store.DeleteAlarm(alarmID)
}

// .
// .
// .
// .
// .
// .
// .
func (t *TIME) SetPlatformWake(w PlatformWake) {
	if w == nil {
		w = NoopWake{}
	}
	t.mu.Lock()
	t.wake = w
	t.mu.Unlock()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (t *TIME) SetQuiesceGate(g *quiesce.Gate) {
	t.mu.Lock()
	t.gate = g
	t.mu.Unlock()
	// .
	// .
	// .
	g.OnTransition(t.signalResched)
}

// .
func (t *TIME) quiesced() bool {
	t.mu.Lock()
	g := t.gate
	t.mu.Unlock()
	return g.Paused()
}

// .

// .
// .
func (t *TIME) After(id string, delay time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(delay), fn: fn})
}

// .
// .
// .
func (t *TIME) Every(id string, interval time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(interval), interval: interval, fn: fn})
}

// .
// .
// .
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
	delete(t.dead, e.id)
	heap.Push(&t.heap, e)
	t.mu.Unlock()
	t.signalResched()
}

// .

// .
// .
// .
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
		return
	}
	// .
	// .
	// .
	// .
	if safe != nil && safe() {
		return
	}
	if err := t.AdvanceLifeClock(ctx); err != nil {
		logsink.Warn("time.error", "life clock advance failed: %v", err)
	}
}

// .
// .
func (t *TIME) SetSafeSource(fn func() bool) {
	t.mu.Lock()
	t.safeSource = fn
	t.mu.Unlock()
}

// .
// .
// .
// .
func (t *TIME) AdvanceLifeClock(ctx context.Context) error {
	// .
	// .
	// .
	// .
	// .
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

// .
func (t *TIME) LifeClock() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lifeClock
}

// .
func WallNow() int64 {
	return time.Now().UTC().UnixMilli()
}

// .
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

// .
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
	// .
	// .
	// .
	// .
	// .
	if first && w != nil {
		w.WakeClear()
	}
}

// .
// .
// .
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
			logsink.Warn("time.error", "wake catch-up failed: %v", err)
		}
	}
}

// .
// .
// .
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

// .

func (t *TIME) schedulerLoop(ctx context.Context) {
	// .
	// .
	// .
	// .
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
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if t.quiesced() {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
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
				continue
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
			continue
		case now := <-fire:
			t.runDue(ctx, now)
		}
	}
}

// .
// .
// .
// .
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

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	alarms, err := t.store.DueAlarms("wall", int64(1)<<62, 16)
	if err != nil {
		logsink.Warn("time.error", "next durable wake unavailable: %v", err)
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
		break
	}
	return best, has
}

// .
// .
// .
func (t *TIME) armPlatformWake(at time.Time) {
	t.mu.Lock()
	w := t.wake
	t.mu.Unlock()
	if w != nil {
		if err := w.WakeAt(at); err != nil {
			logsink.Warn("time.error", "platform wake register failed: %v", err)
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
	// .
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
	// .
	if alarms, err := t.store.DueAlarms("wall", now.UnixMilli(), 100); err != nil {
		logsink.Warn("time.error", "durable wall alarm evaluation failed: %v", err)
	} else if len(alarms) > 0 {
		t.dispatchPass(ctx, "wall", alarms)
	}
}

func (t *TIME) runEphemeral(e *ephemeral) {
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("time.error", "ephemeral %q PANICKED (contained): %v\n%s", e.id, r, debug.Stack())
			if e.interval > 0 {
				t.Cancel(e.id)
			}
		}
	}()
	e.fn()
	if e.interval > 0 {
		t.mu.Lock()
		switch {
		case t.stopped:
			// .
		case t.dead[e.id]:
			// .
			// .
			// .
			delete(t.dead, e.id)
		default:
			// .
			// .
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

// .

func (t *TIME) dispatchPass(ctx context.Context, clock string, alarms []store.Alarm) {
	t.dispatchMu.Lock()
	defer t.dispatchMu.Unlock()
	for _, alarm := range alarms {
		t.dispatchAlarm(ctx, alarm)
	}
}

// .
// .
// .
// .
// .
const pendingDispatchRetryAfter = 30 * time.Second

// .
// .
// .
// .
// .
func declinedRetryAfterMS(clock string) int64 {
	if clock == "life" {
		return 1
	}
	return int64(pendingDispatchRetryAfter / time.Millisecond)
}

func (t *TIME) dispatchAlarm(ctx context.Context, alarm store.Alarm) {
	// .
	// .
	// .
	// .
	// .
	t.mu.Lock()
	enq := t.enqueue
	owner, ok := t.owners[alarm.OwnerName]
	t.mu.Unlock()
	if enq != nil {
		// .
		// .
		// .
		// .
		// .
		if !ok {
			logsink.Warn("time.refusal", "alarm %s has unregistered owner %s — not enqueued (row preserved)", alarm.AlarmID, alarm.OwnerName)
			return
		}
		// .
		// .
		// .
		// .
		// .
		// .
		t.mu.Lock()
		t.pendingDispatch[alarm.AlarmID] = time.Now()
		t.mu.Unlock()
		if err := enq.EnqueueAlarm(alarm); err != nil {
			logsink.Warn("time.error", "alarm %s enqueue failed (row preserved for retry): %v", alarm.AlarmID, err)
		}
		return
	}
	if !ok {
		logsink.Warn("time.refusal", "alarm %s has unregistered owner %s — skipping (row preserved)", alarm.AlarmID, alarm.OwnerName)
		return
	}
	result := t.invokeOwner(ctx, owner, alarm)
	t.applyTransitions(alarm, result)
}

// .
// .
// .
// .
func (t *TIME) clearPendingDispatch(alarmID string) {
	t.mu.Lock()
	delete(t.pendingDispatch, alarmID)
	t.mu.Unlock()
	t.signalResched()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
			logsink.Debug("time.refusal", "alarm %s stale firing (row changed since due read) — delete skipped", alarm.AlarmID)
		}
	case result.NextDeadline != nil:
		return t.applyCAS(alarm, *result.NextDeadline)
	case alarm.RepeatEvery != nil:
		return t.applyCAS(alarm, currentClock+*alarm.RepeatEvery)
	default:
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if currentClock >= alarm.Deadline {
			next := currentClock + declinedRetryAfterMS(alarm.Clock)
			logsink.Info("time.decision", "alarm %s declined without next deadline — deferred to %d (a past deadline is due again immediately)", alarm.AlarmID, next)
			return t.applyCAS(alarm, next)
		}
		logsink.Info("time.decision", "alarm %s declined without next deadline — preserved", alarm.AlarmID)
	}
	return nil
}

// .
// .
func (t *TIME) OwnerFor(name string) (AlarmOwner, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o, ok := t.owners[name]
	return o, ok
}

// .
// .
// .
func (t *TIME) ClearPendingDispatch(alarmID string) { t.clearPendingDispatch(alarmID) }

// .
func (t *TIME) InvokeAlarmOwner(ctx context.Context, owner AlarmOwner, alarm store.Alarm) AlarmResult {
	return t.invokeOwner(ctx, owner, alarm)
}

// .
// .
func (t *TIME) ApplyAlarmTransitions(alarm store.Alarm, result AlarmResult) error {
	return t.applyTransitions(alarm, result)
}

// .
// .
// .
func (t *TIME) invokeOwner(ctx context.Context, owner AlarmOwner, alarm store.Alarm) (result AlarmResult) {
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("time.error", "owner %q PANICKED on alarm %s (contained, treated as declined): %v\n%s",
				owner.Name(), alarm.AlarmID, r, debug.Stack())
			result = AlarmResult{}
		}
		t.mu.Lock()
		obs := t.onFire
		t.mu.Unlock()
		if obs != nil {
			obs(owner.Name(), alarm.AlarmID, result.Accepted)
		}
	}()
	return owner.OnAlarm(ctx, alarm.AlarmID, alarm.Clock, alarm.Deadline, alarm.Payload)
}

// .
// .
// .
func (t *TIME) SetFireObserver(fn func(owner, alarmID string, accepted bool)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFire = fn
}

func (t *TIME) applyCAS(alarm store.Alarm, newDeadline int64) error {
	ok, err := t.store.UpdateAlarmDeadlineCAS(alarm.AlarmID, alarm.Deadline, newDeadline)
	if err != nil {
		return fmt.Errorf("alarm %s reschedule failed (the old deadline stands and will fire again): %w", alarm.AlarmID, err)
	}
	if !ok {
		logsink.Debug("time.refusal", "alarm %s stale firing (row changed since due read) — transition skipped", alarm.AlarmID)
	}
	if alarm.Clock == "wall" {
		t.signalResched()
	}
	return nil
}
