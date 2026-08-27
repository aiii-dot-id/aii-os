package cognitive

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

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
func (m *memQueue) SweepExpiredLeases(now int64) (int, error) {
	return 0, nil
}
func (m *memQueue) PendingWorkCount() (int, error) { return 0, nil }
