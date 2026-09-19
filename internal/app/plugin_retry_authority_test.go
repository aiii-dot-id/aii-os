package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
// .

type retryRuntime struct {
	mu       sync.Mutex
	verifyOK bool
	verifies int
	gate     chan struct{}
}

func (r *retryRuntime) Verify(_ context.Context, pkg string) (pluginfacility.Evidence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verifies++
	if !r.verifyOK {
		return pluginfacility.Evidence{}, errors.New("the signature does not verify")
	}
	return pluginfacility.Evidence{ID: "org.example.retry", Version: "1.0.0", Package: pkg, PackageHash: "hash:" + pkg, Tier: "T1"}, nil
}
func (r *retryRuntime) Prepare(_ context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: ev, Present: true}, nil
}
func (r *retryRuntime) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	return nil
}
func (r *retryRuntime) Start(ctx context.Context, p pluginfacility.Prepared, _ *pluginfacility.Lease) (pluginfacility.Running, error) {
	r.mu.Lock()
	gate := r.gate
	r.mu.Unlock()
	select {
	case <-gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &running{id: p.Evidence.ID, version: p.Evidence.Version, pkg: p.Evidence.Package, kind: "asset"}, nil
}
func (r *retryRuntime) Health(context.Context, pluginfacility.Running) error { return nil }
func (r *retryRuntime) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	return nil
}
func (r *retryRuntime) Stop(context.Context, pluginfacility.Running) (pluginfacility.Retirement, error) {
	return pluginfacility.Retirement{Established: true}, nil
}

func TestTryAgainReachesTheFacilityAndKeepsTheRecord(t *testing.T) {
	const id = "org.example.retry"
	rt := &retryRuntime{gate: make(chan struct{})}
	a := &App{sweepPoke: make(chan struct{}, 1)}
	f := pluginfacility.New(pluginfacility.Config{Runtime: rt})
	a.facilityOnce.Do(func() { a.facility = f })
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	row := func() *dashboard.PluginPendingView {
		a.applyFacilitySnapshot()
		for _, v := range a.pluginPendingViews() {
			if v.ID == id {
				v := v
				return &v
			}
		}
		return nil
	}
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "one.aiiospkg", Hash: "hash:one.aiiospkg"}}, pluginfacility.Policy{Revision: 1})
	wait("the permanent refusal on the page", func() bool { r := row(); return r != nil && r.Phase == lifeRefused && r.Refusal != nil })

	if err := a.RetryPlugin("org.example.nobody"); err == nil {
		t.Error("an id this host does not have was reported as retried")
	}
	for _, bad := range []string{"", "../x", "a/b"} {
		if err := a.RetryPlugin(bad); err == nil {
			t.Errorf("%q was accepted as a plugin id", bad)
		}
	}

	// .
	// .
	rt.mu.Lock()
	before := rt.verifies
	rt.verifyOK = true
	rt.mu.Unlock()
	if err := a.RetryPlugin(id); err != nil {
		t.Fatal(err)
	}
	wait("a new attempt at the runtime", func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.verifies > before })
	// .
	// .
	wait("the new attempt to be starting on the page", func() bool { r := row(); return r != nil && r.Phase == lifeStarting })
	if r := row(); r.Refusal == nil || !strings.Contains(r.Refusal.Cause, "does not verify") {
		t.Errorf("the last refusal left the card before there was a new outcome: %+v", r)
	}
	close(rt.gate)
	wait("it to serve, and the row to go", func() bool { return row() == nil })
}
