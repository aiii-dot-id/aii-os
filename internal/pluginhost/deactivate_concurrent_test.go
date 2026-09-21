package pluginhost

import (
	"context"
	"sync"
	"testing"
)

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
			<-start
			_ = ap.Deactivate(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	// .
	// .
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Errorf("deactivating an already-deactivated activation: %v", err)
	}
}
