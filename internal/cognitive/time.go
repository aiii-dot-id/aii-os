package cognitive

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

const (
	SelfModelCadence      int64 = 50
	IdentityReviewCadence int64 = 100
)

type AlarmOwner interface {
	Name() string
	OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult
}

type SafeFirer interface {
	OnSafeAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string)
}

type SafePassEnder interface {
	SafePassEnd(ctx context.Context)
}

type DuePager interface {
	DueAlarmsAfter(clock string, nowOrLess, afterDeadline int64, afterID string, limit int) ([]rows.Alarm, error)
}

const safePageSize = 100

type safeMark struct {
	durable int64
	next    int64
	clock   string
}

const safeNever = int64(1)<<62 - 1

func nextPeriodBoundary(deadline, period, now int64) int64 {
	if period <= 0 {
		return safeNever
	}
	if now < deadline {
		return deadline + period
	}
	return deadline + ((now-deadline)/period+1)*period
}

type AlarmResult struct {
	Accepted     bool
	NextDeadline *int64

	Err error
}

type SetAlarmer interface {
	SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error
	EnsureAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error
	CancelAlarm(ownerName, alarmID string) error
	DueAlarms(clock string, nowOrLess int64, limit int) ([]rows.Alarm, error)
	DeleteAlarm(alarmID string) error

	UpdateAlarmDeadlineCAS(alarmID string, expectedDeadline, newDeadline int64) (bool, error)
	DeleteAlarmCAS(alarmID string, expectedDeadline int64) (bool, error)
}

type LifetimeTicker interface {
	LifetimeTicks() (int64, error)
	IncrementLifetimeTicks() error
}

type PulseSource interface {
	Interval() time.Duration
	Live() bool
}

type PlatformWake interface {
	WakeAt(at time.Time) error
	WakeClear()
}

type NoopWake struct{}

func (NoopWake) WakeAt(time.Time) error { return nil }
func (NoopWake) WakeClear()             {}

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

type TIME struct {
	store    SetAlarmer
	lifetime LifetimeTicker

	mu     sync.Mutex
	owners map[string]AlarmOwner

	onFire  func(owner, alarmID string, accepted bool)
	heap    timerHeap
	started bool
	stopped bool

	dead map[string]bool

	lifeClock int64
	pulse     PulseSource
	wake      PlatformWake
	gate      *quiesce.Gate

	pendingDispatch map[string]time.Time
	safeSource      func() bool

	safeMarks map[string]safeMark

	dispatchMu sync.Mutex

	timerCtx    context.Context
	timerCancel context.CancelFunc
	resched     chan struct{}
	stopCh      chan struct{}
	runWG       sync.WaitGroup

	enqueue AlarmEnqueuer
}

type AlarmEnqueuer interface {
	EnqueueAlarm(alarm rows.Alarm) error
}

func NewTIME(store SetAlarmer, lifetime LifetimeTicker) *TIME {
	t := &TIME{
		store:           store,
		lifetime:        lifetime,
		owners:          make(map[string]AlarmOwner),
		dead:            make(map[string]bool),
		pendingDispatch: make(map[string]time.Time),
		safeMarks:       make(map[string]safeMark),
		resched:         make(chan struct{}, 1),
		stopCh:          make(chan struct{}),
	}
	if ticks, err := lifetime.LifetimeTicks(); err == nil {
		t.lifeClock = ticks
	}
	return t
}

func (t *TIME) SetAlarmEnqueuer(ae AlarmEnqueuer) {
	t.mu.Lock()
	t.enqueue = ae
	t.mu.Unlock()
}

func (t *TIME) RegisterOwner(owner AlarmOwner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.owners[owner.Name()] = owner
}

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

func (t *TIME) EnsureAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	t.mu.Lock()
	_, registered := t.owners[ownerName]
	t.mu.Unlock()
	if !registered {
		return fmt.Errorf("ensure alarm %s: owner %q is not registered", alarmID, ownerName)
	}
	if err := t.store.EnsureAlarm(alarmID, ownerName, clock, deadline, repeatEvery, payload); err != nil {
		return err
	}
	if clock == "wall" {
		t.signalResched()
	}
	return nil
}

func (t *TIME) CancelAlarm(ownerName, alarmID string) error {
	if err := t.store.CancelAlarm(ownerName, alarmID); err != nil {
		return err
	}
	t.signalResched()
	return nil
}

func (t *TIME) DeleteLegacyAlarm(alarmID, reason string) error {
	logsink.Info("time.decision", "deleting legacy alarm %s (%s)", alarmID, reason)
	return t.store.DeleteAlarm(alarmID)
}

func (t *TIME) SetPlatformWake(w PlatformWake) {
	if w == nil {
		w = NoopWake{}
	}
	t.mu.Lock()
	t.wake = w
	t.mu.Unlock()
}

func (t *TIME) SetQuiesceGate(g *quiesce.Gate) {
	t.mu.Lock()
	t.gate = g
	t.mu.Unlock()

	g.OnTransition(t.signalResched)
}

func (t *TIME) quiesced() bool {
	t.mu.Lock()
	g := t.gate
	t.mu.Unlock()
	return g.Paused()
}

func (t *TIME) After(id string, delay time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(delay), fn: fn})
}

func (t *TIME) Every(id string, interval time.Duration, fn func()) {
	t.armEphemeral(&ephemeral{id: id, next: time.Now().Add(interval), interval: interval, fn: fn})
}

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

	if safe != nil && safe() {
		return
	}
	if err := t.AdvanceLifeClock(ctx); err != nil {
		logsink.Warn("time.error", "life clock advance failed: %v", err)
	}
}

func (t *TIME) SetSafeSource(fn func() bool) {
	t.mu.Lock()
	t.safeSource = fn
	t.mu.Unlock()
}

func (t *TIME) AdvanceLifeClock(ctx context.Context) error {

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

func (t *TIME) LifeClock() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lifeClock
}

func WallNow() int64 {
	return time.Now().UTC().UnixMilli()
}

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

	if first && w != nil {
		w.WakeClear()
	}
}

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

func (t *TIME) EvaluateAll(ctx context.Context) error {
	t.mu.Lock()
	life := t.lifeClock
	t.mu.Unlock()

	if t.inSafe() {
		return errors.Join(t.safePass(ctx, "wall", WallNow()), t.safePass(ctx, "life", life))
	}
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

func (t *TIME) schedulerLoop(ctx context.Context) {

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

		if t.quiesced() {

			if next, has := t.nextParkedWake(); has {
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

func (t *TIME) nextWake() (time.Time, bool) {
	return t.nextWakeIncludingEphemerals(true)
}

func (t *TIME) nextParkedWake() (time.Time, bool) {
	return t.nextWakeIncludingEphemerals(false)
}

func (t *TIME) nextWakeIncludingEphemerals(includeEphemerals bool) (time.Time, bool) {
	t.mu.Lock()
	var best time.Time
	var has bool
	if includeEphemerals && len(t.heap) > 0 {
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

	if t.inSafe() {

		if at, ok := t.safeNextWake("wall", now.UnixMilli()); ok {
			wt := time.UnixMilli(at)
			if !has || wt.Before(best) {
				best, has = wt, true
			}
		}
		return best, has
	}
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

	if t.inSafe() {
		if err := t.safePass(ctx, "wall", now.UnixMilli()); err != nil {
			logsink.Warn("time.error", "durable wall alarm evaluation failed (SAFE): %v", err)
		}
		return
	}
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

		case t.dead[e.id]:

			delete(t.dead, e.id)
		default:

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

func (t *TIME) dispatchPass(ctx context.Context, clock string, alarms []rows.Alarm) {
	t.dispatchMu.Lock()
	defer t.dispatchMu.Unlock()

	t.mu.Lock()
	if len(t.safeMarks) > 0 {
		t.safeMarks = make(map[string]safeMark)
	}
	t.mu.Unlock()
	for _, alarm := range alarms {
		t.dispatchAlarm(ctx, alarm)
	}
}

func (t *TIME) inSafe() bool {
	t.mu.Lock()
	safe := t.safeSource
	t.mu.Unlock()
	return safe != nil && safe()
}

func (t *TIME) safeDue(a rows.Alarm) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m, ok := t.safeMarks[a.AlarmID]; ok && m.durable == a.Deadline {
		return m.next
	}
	return a.Deadline
}

func (t *TIME) safeWalk(clock string, nowOrLess int64, visit func(rows.Alarm)) (last rows.Alarm, n int, complete bool, err error) {
	pager, ok := t.store.(DuePager)
	if !ok {
		rows, err := t.store.DueAlarms(clock, nowOrLess, safePageSize)
		if err != nil {
			return last, 0, false, err
		}
		for _, a := range rows {
			visit(a)
			last, n = a, n+1
		}
		return last, n, len(rows) < safePageSize, nil
	}
	afterDeadline, afterID := int64(-1)<<62, ""
	for {
		rows, err := pager.DueAlarmsAfter(clock, nowOrLess, afterDeadline, afterID, safePageSize)
		if err != nil {
			return last, n, false, err
		}
		for _, a := range rows {
			visit(a)
			last, n = a, n+1
		}
		if len(rows) < safePageSize {
			return last, n, true, nil
		}
		afterDeadline, afterID = last.Deadline, last.AlarmID
	}
}

func (t *TIME) safePass(ctx context.Context, clock string, now int64) error {
	t.dispatchMu.Lock()
	defer t.dispatchMu.Unlock()
	if !t.inSafe() {
		return nil
	}
	seen := map[string]struct{}{}
	var due []rows.Alarm
	_, _, complete, err := t.safeWalk(clock, now, func(a rows.Alarm) {
		seen[a.AlarmID] = struct{}{}
		if t.safeDue(a) <= now {
			due = append(due, a)
		}
	})
	if err != nil {
		return fmt.Errorf("read due %s alarms (SAFE): %w", clock, err)
	}
	if complete {

		t.mu.Lock()
		for id, m := range t.safeMarks {
			if _, present := seen[id]; !present && m.clock == clock {
				delete(t.safeMarks, id)
			}
		}
		t.mu.Unlock()
	}

	var fired, held safeTally
	for _, a := range due {
		if t.safeFire(ctx, a, now) {
			fired.add(a)
		} else {
			held.add(a)
		}
	}
	for name := range fired.by {
		t.mu.Lock()
		ender, ok := t.owners[name].(SafePassEnder)
		t.mu.Unlock()
		if ok {
			t.safePassEnd(ctx, name, ender)
		}
	}
	if fired.total > 0 {
		logsink.Info("time.decision", "%d %s alarm(s) fired transiently under SAFE (%s; e.g. %s) — nothing written; the rows fire again, durably, on recovery",
			fired.total, clock, fired.owners(), fired.example)
	}
	if held.total > 0 {
		logsink.Info("time.refusal", "%d %s alarm(s) held under SAFE (%s; e.g. %s) — their owners keep no promise in SAFE; the rows are untouched and fire when the identity is NORMAL",
			held.total, clock, held.owners(), held.example)
	}
	return nil
}

func (t *TIME) safePassEnd(ctx context.Context, name string, ender SafePassEnder) {
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("time.error", "owner %q PANICKED ending a SAFE pass (contained): %v\n%s", name, r, debug.Stack())
		}
	}()
	ender.SafePassEnd(ctx)
}

type safeTally struct {
	total   int
	by      map[string]int
	example string
}

func (s *safeTally) add(a rows.Alarm) {
	if s.by == nil {
		s.by = map[string]int{}
		s.example = fmt.Sprintf("%s@%d", a.AlarmID, a.Deadline)
	}
	s.by[a.OwnerName]++
	s.total++
}

func (s *safeTally) owners() string {
	out := make([]string, 0, len(s.by))
	for name, n := range s.by {
		out = append(out, fmt.Sprintf("%s×%d", name, n))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func (t *TIME) safeFire(ctx context.Context, a rows.Alarm, now int64) (fired bool) {
	deadline := t.safeDue(a)
	t.mu.Lock()
	owner, registered := t.owners[a.OwnerName]
	firer, fires := owner.(SafeFirer)
	next := safeNever
	if fires && a.RepeatEvery != nil {
		next = nextPeriodBoundary(deadline, *a.RepeatEvery, now)
	}
	t.safeMarks[a.AlarmID] = safeMark{durable: a.Deadline, next: next, clock: a.Clock}
	t.mu.Unlock()
	if !fires {
		if !registered {
			logsink.Warn("time.refusal", "alarm %s has unregistered owner %s — held under SAFE (row preserved)", a.AlarmID, a.OwnerName)
		}
		return false
	}
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("time.error", "owner %q PANICKED on SAFE firing of %s@%d (contained; SAFE does not retry): %v\n%s",
				owner.Name(), a.AlarmID, deadline, r, debug.Stack())
		}
	}()
	firer.OnSafeAlarm(ctx, a.AlarmID, a.Clock, deadline, a.Payload)
	if next != safeNever && a.Clock == "wall" {
		t.signalResched()
	}
	return true
}

func (t *TIME) safeNextWake(clock string, now int64) (int64, bool) {
	best, has := int64(0), false
	note := func(at int64) {
		if at < safeNever && (!has || at < best) {
			best, has = at, true
		}
	}
	last, n, complete, err := t.safeWalk(clock, now, func(a rows.Alarm) { note(t.safeDue(a)) })
	if err != nil {
		logsink.Warn("time.error", "next durable wake unavailable (SAFE): %v", err)
		return best, has
	}
	if !complete {
		return best, has
	}

	if pager, ok := t.store.(DuePager); ok {
		afterDeadline, afterID := int64(-1)<<62, ""
		if n > 0 {
			afterDeadline, afterID = last.Deadline, last.AlarmID
		}
		rows, err := pager.DueAlarmsAfter(clock, int64(1)<<62, afterDeadline, afterID, 1)
		if err == nil && len(rows) > 0 && rows[0].Deadline > now {
			note(rows[0].Deadline)
		}
	} else if rows, err := t.store.DueAlarms(clock, int64(1)<<62, safePageSize); err == nil {
		for _, a := range rows {
			if a.Deadline > now {
				note(a.Deadline)
				break
			}
		}
	}
	return best, has
}

const pendingDispatchRetryAfter = 30 * time.Second

func declinedRetryAfterMS(clock string) int64 {
	if clock == "life" {
		return 1
	}
	return int64(pendingDispatchRetryAfter / time.Millisecond)
}

func (t *TIME) dispatchAlarm(ctx context.Context, alarm rows.Alarm) {

	t.mu.Lock()
	enq := t.enqueue
	owner, ok := t.owners[alarm.OwnerName]
	t.mu.Unlock()
	if enq != nil {

		if !ok {
			logsink.Warn("time.refusal", "alarm %s has unregistered owner %s — not enqueued (row preserved)", alarm.AlarmID, alarm.OwnerName)
			return
		}

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

func (t *TIME) clearPendingDispatch(alarmID string) {
	t.mu.Lock()
	delete(t.pendingDispatch, alarmID)
	t.mu.Unlock()
	t.signalResched()
}

func (t *TIME) applyTransitions(alarm rows.Alarm, result AlarmResult) error {
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

		if currentClock >= alarm.Deadline {
			next := currentClock + declinedRetryAfterMS(alarm.Clock)
			logsink.Info("time.decision", "alarm %s declined without next deadline — deferred to %d (a past deadline is due again immediately)", alarm.AlarmID, next)
			return t.applyCAS(alarm, next)
		}
		logsink.Info("time.decision", "alarm %s declined without next deadline — preserved", alarm.AlarmID)
	}
	return nil
}

func (t *TIME) OwnerFor(name string) (AlarmOwner, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o, ok := t.owners[name]
	return o, ok
}

func (t *TIME) ClearPendingDispatch(alarmID string) { t.clearPendingDispatch(alarmID) }

func (t *TIME) InvokeAlarmOwner(ctx context.Context, owner AlarmOwner, alarm rows.Alarm) AlarmResult {
	return t.invokeOwner(ctx, owner, alarm)
}

func (t *TIME) ApplyAlarmTransitions(alarm rows.Alarm, result AlarmResult) error {
	return t.applyTransitions(alarm, result)
}

func (t *TIME) invokeOwner(ctx context.Context, owner AlarmOwner, alarm rows.Alarm) (result AlarmResult) {
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("time.error", "owner %q PANICKED on alarm %s (contained, treated as declined): %v\n%s",
				owner.Name(), alarm.AlarmID, r, debug.Stack())
			result = AlarmResult{}
		}
		if !result.Accepted && result.Err != nil {

			logsink.Error("time.error", "owner %q could not deliver alarm %s (row preserved, retried next pass): %v",
				owner.Name(), alarm.AlarmID, result.Err)
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

func (t *TIME) SetFireObserver(fn func(owner, alarmID string, accepted bool)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onFire = fn
}

func (t *TIME) applyCAS(alarm rows.Alarm, newDeadline int64) error {
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
