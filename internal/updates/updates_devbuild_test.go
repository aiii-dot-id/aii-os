package updates

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// A build whose version is not a release version can never answer "is
// that release newer than me?". The version is compiled in, so that
// verdict is knowable before the first tick — yet the checker used to
// reach GitHub hourly and fail. A live
// identity logged 35 such failures in a day, every one impossible to
// succeed, and each reporting a 404 rather than the real cause.
//
// The checker must decide once and stop, not park on the ticker.
func TestNoReleaseVersionStopsTheCheckerBeforeTheNetwork(t *testing.T) {
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "" }, // no release version
		func() bool { return true },
		nil, // repo unset — resolveRepo falls back to DefaultRepo
		nil,
		t.TempDir(),
		func() bool { return false }, func() bool { return false },
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	select {
	case <-done: // returned on its own, without a ticker and without the network
	case <-time.After(2 * time.Second):
		t.Fatal("Run parked on the hourly ticker with an uncomparable version — it must decide once and return")
	}
}
