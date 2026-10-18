package mobile

import (
	"context"
	"testing"
	"time"
)

// stopRuntime ends a test's runtime and fails the test when its teardown
// fails: Runtime.Stop logs a failed store, ledger or plugin close and
// returns nothing, so a teardown regression passed every test.
func stopRuntime(t testing.TB, r *Runtime) {
	t.Helper()
	// Only a teardown that never ends is caught here: how long a correct
	// one takes is the host's (internal/app's stopApp says why, and the
	// bound is the same).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := r.a.Shutdown(ctx); err != nil {
		t.Errorf("teardown: %v", err)
	}
}
