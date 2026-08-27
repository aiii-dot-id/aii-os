package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/foreground"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The Executor: the queue's single worker (v1). Loop: sweep expired
// leases (crash recovery as a query) → claim → run with panic
// containment → complete/fail. Handlers own MEANING; the queue owns
// delivery and durability. Agent frameworks (future plugins) register
// handlers for their own kinds and enqueue their own next steps — the
// queue never learns what a plan is (WORK_QUEUE.md §2.5).

// WorkHandler runs one durable work item. Payload is JSON, opaque to
// the queue; the handler interprets. Returning an error fails the item
// (retry per policy); panics are contained as failures.
type WorkHandler interface {
	WorkKinds() []string
	RunWork(ctx context.Context, w *store.WorkItem) error
}

// QueueWork is the store surface the executor needs.
type QueueWork interface {
	EnqueueWork(item *store.WorkItem) (string, error)
	ClaimWork(kinds []string, nowMs int64) (*store.WorkItem, error)
	CompleteWork(id string) error
	FailWork(id string, errMsg string) error
	SweepExpiredLeases(nowMs int64) (int, error)
	PendingWorkCount() (int, error)
}

// Executor runs durable work across agency.queue_workers concurrent
// workers (v2, 2026-08-26 — external review P2-5: one worker
// serialized every sub-agent, so the advertised parallelism was a
// ceiling on a queue that never ran two at once). Claims stay atomic
// at the store; a successful claim CASCADES a poke so a sibling wakes
// for the next item instead of the first worker draining serially.
// HISTORY (WORK_QUEUE.md): v1 was one goroutine by design, and a long
// facility pass delayed other queued work by up
// to one handler run; alarm kinds enqueue at priority 1 (high) so they
// are claimed first the moment the executor frees.
type Executor struct {
	q        QueueWork
	handlers map[string]WorkHandler
	mu       sync.RWMutex
	wg       sync.WaitGroup
	stop     chan struct{}
	stopOnce sync.Once
	// poke wakes the loop the moment work is enqueued — the outbox's
	// pokeOutbox pattern, the third sibling (outboxPoke, sweepPoke).
	// The ticker below is demoted to drift insurance; enqueues carry
	// the signal (wakeup unification, 2026-08-26).
	poke chan struct{}
	// holds is the foreground-need registry (nil = inert): a claimed
	// work item is a promise mid-keeping, and the process must not be
	// suspended out from under it.
	holds *foreground.Holds
	// workers is how many claim-and-run loops Start launches (min 1).
	workers int
	ticker  *quiesce.Ticker
	poll    time.Duration
	gate    *quiesce.Gate // background-metabolism governor (nil = always-on)
}

// NewExecutor wires the executor over the queue store.
func NewExecutor(q QueueWork) *Executor {
	return &Executor{
		q:        q,
		handlers: make(map[string]WorkHandler),
		stop:     make(chan struct{}),
		poke:     make(chan struct{}, 1),
		poll:     60 * time.Second, // heartbeat; enqueue pokes carry the signal
	}
}

// SetQuiesceGate wires the background-metabolism governor (quiesce,
// 2026-08-19 — a phone died to teach it). Wire before Start, like
// RegisterHandler. At 500ms this poll was ~173k wakeups/day of lease
// sweeps against an empty queue; backgrounded it parks. Due work is
// DEFERRED, not lost: the foreground catch-up tick runs a full pass,
// and an OS-scheduled TimeWake enqueue still lands in the durable
// queue for that pass to claim.
func (e *Executor) SetQuiesceGate(g *quiesce.Gate) { e.gate = g }

// RegisterHandler mounts a handler for its kinds.
func (e *Executor) RegisterHandler(h WorkHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, k := range h.WorkKinds() {
		e.handlers[k] = h
	}
}

// Start runs the executor's workers. One governed ticker is shared:
// a tick wakes ONE worker (capacity-one channel), that worker's first
// claim cascades a poke, and the cascade wakes the rest exactly as
// deep as the due work goes — no thundering herd, no serial drain.
func (e *Executor) Start(ctx context.Context) {
	n := e.workers
	if n < 1 {
		n = 1
	}
	e.ticker = quiesce.NewTicker(e.gate, e.poll) // governed: parked = zero polls (quiesce, 2026-08-19)
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

// SetHolds wires the foreground-need registry (nil stays inert).
func (e *Executor) SetHolds(h *foreground.Holds) { e.holds = h }

// SetWorkers sets the concurrent worker count before Start (min 1).
func (e *Executor) SetWorkers(n int) {
	if n < 1 {
		n = 1
	}
	e.workers = n
}

// Wake signals that durable work is ready; the ticker is drift insurance.
func (e *Executor) Wake() {
	select {
	case e.poke <- struct{}{}:
	default:
	}
}

// Stop terminates the loop (in-flight work's lease expires and
// recovers). Idempotent — the double-close panic class, fixed here
// proactively after TIME/Heartbeat taught it this morning. Stop waits
// for the in-flight handler (bounded by its LLM timeout).
func (e *Executor) Stop() {
	e.stopOnce.Do(func() { close(e.stop) })
	e.wg.Wait()
	if e.ticker != nil {
		e.ticker.Stop()
	}
}

// pass: sweep, then claim-and-run until no due work remains.
func (e *Executor) pass(ctx context.Context) {
	if n, err := e.q.SweepExpiredLeases(time.Now().UTC().UnixMilli()); err != nil {
		log.Printf("WORKQ: sweep failed: %v", err)
	} else if n > 0 {
		log.Printf("WORKQ: swept %d expired leases (crash recovery)", n)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stop:
			return
		default:
		}
		w, err := e.q.ClaimWork(nil, time.Now().UTC().UnixMilli())
		if err != nil {
			log.Printf("WORKQ: claim failed: %v", err)
			return
		}
		if w == nil {
			return // queue drained for now
		}
		// CASCADE: more work may be due behind this claim. Wake a
		// sibling BEFORE running, so parallel items actually run in
		// parallel instead of serially in whoever woke first.
		select {
		case e.poke <- struct{}{}:
		default:
		}
		e.runOne(ctx, w)
	}
}

func (e *Executor) runOne(ctx context.Context, w *store.WorkItem) {
	h := e.handlerFor(w.Kind)
	if h == nil {
		// No handler: fail fast with the honest reason — never silently
		// park work nobody can run.
		if err := e.q.FailWork(w.ID, "no handler registered for kind "+w.Kind); err != nil {
			log.Printf("WORKQ: fail-fast %s (%s): %v", w.ID, w.Kind, err)
		} else {
			log.Printf("WORKQ: no handler for kind %q — item %s failed honestly (retries will exhaust)", w.Kind, w.ID)
		}
		return
	}
	err := e.invokeHandler(ctx, h, w)
	if err == nil {
		if cerr := e.q.CompleteWork(w.ID); cerr != nil {
			log.Printf("WORKQ: complete %s failed: %v", w.ID, cerr)
		} else {
			log.Printf("WORKQ: %s %s done", w.Kind, w.ID)
		}
		return
	}
	if ferr := e.q.FailWork(w.ID, err.Error()); ferr != nil {
		log.Printf("WORKQ: fail %s: %v (original: %v)", w.ID, ferr, err)
	} else {
		log.Printf("WORKQ: %s %s failed: %v", w.Kind, w.ID, err)
	}
}

// handlerFor resolves exact kind, then namespace wildcard ("alarm.*"
// matches "alarm.timers"). Registration is by namespace; lookup walks it.
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

// invokeHandler with panic containment — a panicking handler is a
// failed item (retry per policy), never a dead executor.
func (e *Executor) invokeHandler(ctx context.Context, h WorkHandler, w *store.WorkItem) (err error) {
	rel := e.holds.Acquire("work: " + w.Kind)
	defer rel()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("WORKQ: handler %q PANICKED on %s (contained, failed item): %v\n%s", w.Kind, w.ID, r, debug.Stack())
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return h.RunWork(ctx, w)
}

// Enqueue is the public entry for schedulers (TIME, identity, plugins).
//
// leaseMs is CRASH RECOVERY, not a scheduler: it must OUTLIVE the slowest
// honest run of the kind being enqueued, or a sibling worker's sweep
// returns the still-running item to PENDING and starts a SECOND
// CONCURRENT copy of work that never stopped (identity/work.go records
// the same lesson for sub-agents). 0 takes the store's default, which is
// only ever right for work shorter than it.
func (e *Executor) Enqueue(kind, payload, dedupKey, source string, priority int, scheduledMs, leaseMs int64) (string, error) {
	id, err := e.q.EnqueueWork(&store.WorkItem{
		Kind: kind, Payload: payload, DedupKey: dedupKey, Source: source,
		Priority: priority, Scheduled: scheduledMs, LeaseMs: leaseMs,
	})
	if err != nil {
		return "", err
	}
	// Wake the loop now; the ticker is only drift insurance (wakeup
	// unification, 2026-08-26 — enqueues carry the signal).
	e.Wake()
	return id, nil
}

// EncodePayload is a convenience for handlers that build JSON payloads.
func EncodePayload(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
