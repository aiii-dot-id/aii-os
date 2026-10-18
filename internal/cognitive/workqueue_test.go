package cognitive

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type heldWake struct {
	entered, release chan struct{}
	calls            atomic.Int32
}

func (*heldWake) WorkKinds() []string { return []string{"wake.timer"} }
func (h *heldWake) RunWork(ctx context.Context, _ *store.WorkItem) error {
	if h.calls.Add(1) != 1 {
		return nil
	}
	close(h.entered)
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLeaseSweepDoesNotReclaimTheExecutorsLiveWake(t *testing.T) {
	_, st := newTIME(t)
	e := NewExecutor(st)
	h := &heldWake{entered: make(chan struct{}), release: make(chan struct{})}
	e.RegisterHandler(h)
	if _, err := e.Enqueue("wake.timer", "{}", "long-wait", "time", 1, 0, 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); e.pass(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-h.entered:
	case <-ctx.Done():
		t.Fatal("wake never claimed")
	}
	// Move the durable lease into the past without a sleep; the handler is
	// demonstrably still owned by this executor, waiting on release.
	if _, err := st.DB().Exec(`UPDATE work_queue SET claimed_at=1 WHERE dedup_key='long-wait'`); err != nil {
		t.Fatal(err)
	}
	e.pass(ctx)
	var state string
	var retry int
	if err := st.DB().QueryRow(`SELECT state,retry_count FROM work_queue WHERE dedup_key='long-wait'`).Scan(&state, &retry); err != nil {
		t.Fatal(err)
	}
	if state != "CLAIMED" || retry != 0 || h.calls.Load() != 1 {
		t.Fatalf("live wake swept: %s retry=%d calls=%d", state, retry, h.calls.Load())
	}
	close(h.release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("wake did not finish")
	}
	// A genuinely absent executor has no live claim to protect. The same
	// database row is now swept by a new executor and waits out its backoff.
	if _, err := st.DB().Exec(`UPDATE work_queue SET state='CLAIMED',claimed_at=1,done_at=0 WHERE dedup_key='long-wait'`); err != nil {
		t.Fatal(err)
	}
	next := NewExecutor(st)
	next.RegisterHandler(h)
	// Time held still: the sweep schedules the retry a backoff from now, and a
	// claim a backoff later — a second or more on a host at load seventy, which
	// is how this test once read the retry as already due (DONE, not PENDING).
	frozen := time.Now().UTC().UnixMilli()
	next.nowMs = func() int64 { return frozen }
	next.pass(ctx)
	if err := st.DB().QueryRow(`SELECT state,retry_count FROM work_queue WHERE dedup_key='long-wait'`).Scan(&state, &retry); err != nil {
		t.Fatal(err)
	}
	if state != "PENDING" || retry != 1 {
		t.Fatalf("absent owner never recovers: %s retry=%d", state, retry)
	}
}

// Executor.Stop is idempotent (the double-close class — TIME and
// Heartbeat taught it this morning; the executor gets it proactively).
func TestExecutorStopIdempotent(t *testing.T) {
	q := &memQueue{}
	e := NewExecutor(q)
	e.Start(context.Background())
	e.Stop()
	e.Stop() // must not panic
}

// memQueue: minimal in-memory QueueWork for executor tests.
type memQueue struct {
	items []*store.WorkItem
}

func (m *memQueue) EnqueueWork(i *store.WorkItem) (string, error) {
	m.items = append(m.items, i)
	return i.ID, nil
}
func (m *memQueue) ClaimWork(kinds []string, now int64) (*store.WorkItem, error) {
	for _, it := range m.items {
		if it.State == "PENDING" {
			it.State = "CLAIMED"
			return it, nil
		}
	}
	return nil, nil
}
func (m *memQueue) CompleteWork(id string) error  { return nil }
func (m *memQueue) FailWork(id, msg string) error { return nil }
func (m *memQueue) SweepExpiredLeases(now int64, running ...string) (int, error) {
	return 0, nil
}
func (m *memQueue) PendingWorkCount() (int, error) { return 0, nil }
