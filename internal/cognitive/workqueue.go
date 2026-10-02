package cognitive

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/foreground"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type WorkHandler interface {
	WorkKinds() []string
	RunWork(ctx context.Context, w *rows.WorkItem) error
}

type QueueWork interface {
	EnqueueWork(item *rows.WorkItem) (string, error)
	ClaimWork(kinds []string, nowMs int64) (*rows.WorkItem, error)
	CompleteWork(id string) error
	FailWork(id string, errMsg string) error
	SweepExpiredLeases(nowMs int64, running ...string) (int, error)
	PendingWorkCount() (int, error)
}

type Executor struct {
	q        QueueWork
	handlers map[string]WorkHandler
	mu       sync.RWMutex
	wg       sync.WaitGroup
	stop     chan struct{}
	stopOnce sync.Once

	poke chan struct{}

	holds *foreground.Holds

	workers int
	ticker  *quiesce.Ticker
	poll    time.Duration

	nowMs func() int64
	gate  *quiesce.Gate

	claimMu sync.Mutex
	running map[string]bool
}

func NewExecutor(q QueueWork) *Executor {
	return &Executor{
		q:        q,
		handlers: make(map[string]WorkHandler),
		stop:     make(chan struct{}),
		poke:     make(chan struct{}, 1),
		poll:     60 * time.Second,
		running:  make(map[string]bool),
		nowMs:    func() int64 { return time.Now().UTC().UnixMilli() },
	}
}

func (e *Executor) SetQuiesceGate(g *quiesce.Gate) { e.gate = g }

func (e *Executor) RegisterHandler(h WorkHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range h.WorkKinds() {
		e.handlers[k] = h
	}
}

func (e *Executor) Start(ctx context.Context) {
	n := e.workers
	if n < 1 {
		n = 1
	}
	e.ticker = quiesce.NewTicker(e.gate, e.poll)
	for i := 0; i < n; i++ {
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-e.stop:
					return
				case <-e.ticker.C:
					e.pass(ctx)
				case <-e.poke:
					e.pass(ctx)
				}
			}
		}()
	}
}

func (e *Executor) SetHolds(h *foreground.Holds) { e.holds = h }

func (e *Executor) SetWorkers(n int) {
	if n < 1 {
		n = 1
	}
	e.workers = n
}

func (e *Executor) Wake() {
	select {
	case e.poke <- struct{}{}:
	default:
	}
}

func (e *Executor) Stop() {
	e.stopOnce.Do(func() { close(e.stop) })
	e.wg.Wait()
	if e.ticker != nil {
		e.ticker.Stop()
	}
}

func (e *Executor) pass(ctx context.Context) {
	e.claimMu.Lock()
	ids := make([]string, 0, len(e.running))
	for id := range e.running {
		ids = append(ids, id)
	}
	n, err := e.q.SweepExpiredLeases(e.nowMs(), ids...)
	e.claimMu.Unlock()
	if err != nil {
		logsink.Warn("workq.error", "sweep failed: %v", err)
	} else if n > 0 {
		logsink.Info("workq.decision", "swept %d expired leases (crash recovery)", n)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stop:
			return
		default:
		}
		e.claimMu.Lock()
		w, err := e.q.ClaimWork(nil, e.nowMs())
		if err == nil && w != nil {
			e.running[w.ID] = true
		}
		e.claimMu.Unlock()
		if err != nil {
			logsink.Warn("workq.error", "claim failed: %v", err)
			return
		}
		if w == nil {
			return
		}

		select {
		case e.poke <- struct{}{}:
		default:
		}
		e.runOne(ctx, w)
		e.claimMu.Lock()
		delete(e.running, w.ID)
		e.claimMu.Unlock()
	}
}

const alarmKindPrefix = "alarm."

func (e *Executor) runOne(ctx context.Context, w *rows.WorkItem) {
	h := e.handlerFor(w.Kind)
	if h == nil {

		if err := e.q.FailWork(w.ID, "no handler registered for kind "+w.Kind); err != nil {
			logsink.Warn("workq.refusal", "fail-fast %s (%s): %v", w.ID, w.Kind, err)
		} else {
			logsink.Warn("workq.refusal", "no handler for kind %q — item %s failed honestly (retries will exhaust)", w.Kind, w.ID)
		}
		return
	}
	err := e.invokeHandler(ctx, h, w)
	if err == nil {
		if cerr := e.q.CompleteWork(w.ID); cerr != nil {
			logsink.Warn("workq.error", "complete %s failed: %v", w.ID, cerr)
		} else if strings.HasPrefix(w.Kind, alarmKindPrefix) {

			logsink.Tick("workq.end", "alarms", 0)
		} else {
			logsink.Info("workq.end", "%s %s done", w.Kind, w.ID)
		}
		return
	}
	if ferr := e.q.FailWork(w.ID, err.Error()); ferr != nil {
		logsink.Warn("workq.error", "fail %s: %v (original: %v)", w.ID, ferr, err)
	} else {
		logsink.Warn("workq.error", "%s %s failed: %v", w.Kind, w.ID, err)
	}
}

func (e *Executor) handlerFor(kind string) WorkHandler {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if h, ok := e.handlers[kind]; ok {
		return h
	}
	if i := strings.IndexByte(kind, '.'); i > 0 {
		if h, ok := e.handlers[kind[:i+1]+"*"]; ok {
			return h
		}
	}
	return nil
}

func (e *Executor) invokeHandler(ctx context.Context, h WorkHandler, w *rows.WorkItem) (err error) {
	rel := e.holds.Acquire("work: " + w.Kind)
	defer rel()
	defer func() {
		if r := recover(); r != nil {
			logsink.Error("workq.error", "handler %q PANICKED on %s (contained, failed item): %v\n%s", w.Kind, w.ID, r, debug.Stack())
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h.RunWork(ctx, w)
}

func (e *Executor) Enqueue(kind, payload, dedupKey, source string, priority int, scheduledMs, leaseMs int64) (string, error) {
	id, err := e.q.EnqueueWork(&rows.WorkItem{
		Kind: kind, Payload: payload, DedupKey: dedupKey, Source: source,
		Priority: priority, Scheduled: scheduledMs, LeaseMs: leaseMs,
	})
	if err != nil {
		return "", err
	}

	e.Wake()
	return id, nil
}
