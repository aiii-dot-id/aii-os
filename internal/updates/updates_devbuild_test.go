package updates

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestNoReleaseVersionStopsTheCheckerBeforeTheNetwork(t *testing.T) {
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "" },
		func() bool { return true },
		nil,
		nil,
		t.TempDir(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		c.Run(ctx, func() bool { return false }, func() bool { return false })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run parked on the hourly ticker with an uncomparable version — it must decide once and return")
	}
}
