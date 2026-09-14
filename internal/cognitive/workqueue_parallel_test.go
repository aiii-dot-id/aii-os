package cognitive

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .

// .
type lockedQueue struct {
	mu    sync.Mutex
	items []*store.WorkItem
}

func (m *lockedQueue) EnqueueWork(i *store.WorkItem) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i.State = "PENDING"
	m.items = append(m.items, i)
	return i.ID, nil
}
func (m *lockedQueue) ClaimWork(kinds []string, now int64) (*store.WorkItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.State == "PENDING" {
			it.State = "CLAIMED"
			return it, nil
		}
	}
	return nil, nil
}
func (m *lockedQueue) CompleteWork(id string) error          { return nil }
func (m *lockedQueue) FailWork(id, msg string) error         { return nil }
func (m *lockedQueue) SweepExpiredLeases(int64) (int, error) { return 0, nil }
func (m *lockedQueue) PendingWorkCount() (int, error)        { return 0, nil }

// .
// .
// .
type rendezvousHandler struct {
	both chan struct{}
	mu   sync.Mutex
	in   int
}

func (h *rendezvousHandler) WorkKinds() []string { return []string{"slow"} }
func (h *rendezvousHandler) RunWork(ctx context.Context, w *store.WorkItem) error {
	h.mu.Lock()
	h.in++
	if h.in == 2 {
		close(h.both)
	}
	h.mu.Unlock()
	select {
	case <-h.both:
		return nil
	case <-time.After(3 * time.Second):
		return nil
	}
}

func TestWorkersRunItemsConcurrently(t *testing.T) {
	q := &lockedQueue{}
	e := NewExecutor(q)
	e.SetWorkers(2)
	h := &rendezvousHandler{both: make(chan struct{})}
	e.RegisterHandler(h)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Start(ctx)
	defer e.Stop()

	if _, err := e.Enqueue("slow", "{}", "a", "test", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Enqueue("slow", "{}", "b", "test", 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	select {
	case <-h.both:
		// .
	case <-time.After(3 * time.Second):
		t.Fatal("two due items never ran concurrently — the queue is still serial")
	}
}
