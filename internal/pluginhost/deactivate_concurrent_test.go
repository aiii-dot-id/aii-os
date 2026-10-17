package pluginhost

import (
	"context"
	"sync"
	"testing"
)

func TestDeactivateIsSafeWhenTwoCallersArriveAtOnce(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)

	const callers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all of them past the barrier together
			_ = ap.Deactivate(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	// And it stays idempotent afterwards: a retirement asked again once
	// everything is already released must not fail or release twice.
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Errorf("deactivating an already-deactivated activation: %v", err)
	}
}
