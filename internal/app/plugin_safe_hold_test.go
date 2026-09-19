package app

import (
	"context"
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
// .
// .
// .
// .
// .

type safeHoldRuntime struct {
	mu        sync.Mutex
	gates     map[string]chan struct{}
	starts    map[string]int
	cancelled map[string]int
	stops     map[string]int
}

func (r *safeHoldRuntime) Verify(_ context.Context, pkg string) (pluginfacility.Evidence, error) {
	return pluginfacility.Evidence{ID: "org.example." + pkg, Version: "1.0.0", Package: pkg, PackageHash: "hash:" + pkg, Tier: "T3"}, nil
}
func (r *safeHoldRuntime) Prepare(_ context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: ev, Present: true}, nil
}
func (r *safeHoldRuntime) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	return nil
}
func (r *safeHoldRuntime) Start(ctx context.Context, p pluginfacility.Prepared, _ *pluginfacility.Lease) (pluginfacility.Running, error) {
	pkg := p.Evidence.Package
	r.mu.Lock()
	r.starts[pkg]++
	gate := r.gates[pkg]
	r.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			r.mu.Lock()
			r.cancelled[pkg]++
			r.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	return &running{id: p.Evidence.ID, version: p.Evidence.Version, pkg: pkg, kind: "asset"}, nil
}
func (r *safeHoldRuntime) Health(context.Context, pluginfacility.Running) error { return nil }
func (r *safeHoldRuntime) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	return nil
}
func (r *safeHoldRuntime) Stop(_ context.Context, run pluginfacility.Running) (pluginfacility.Retirement, error) {
	r.mu.Lock()
	r.stops[run.(*running).pkg]++
	r.mu.Unlock()
	return pluginfacility.Retirement{Established: true}, nil
}
func (r *safeHoldRuntime) count(m map[string]int, pkg string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return m[pkg]
}

func TestSAFEHoldsThePluginLifecycleStill(t *testing.T) {
	rt := &safeHoldRuntime{gates: map[string]chan struct{}{"rising": make(chan struct{})},
		starts: map[string]int{}, cancelled: map[string]int{}, stops: map[string]int{}}
	a := &App{cfg: &Config{Plugins: PluginsConfig{Autoload: "T0"}}, sweepPoke: make(chan struct{}, 4)}
	f := pluginfacility.New(pluginfacility.Config{Runtime: rt})
	a.facilityOnce.Do(func() { a.facility = f })
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close(); a.resetModeForTest() })
	tier, _, _ := autoloadTier("T0")
	observe := func(pkgs ...string) {
		set := make([]pluginfacility.Observed, 0, len(pkgs))
		for _, p := range pkgs {
			set = append(set, pluginfacility.Observed{ID: "org.example." + p, Package: p, Hash: "hash:" + p})
		}
		f.Observe(set, a.pluginPolicy(*a.cfg, tier, false))
	}
	state := func(id string) pluginfacility.State {
		for _, v := range f.Snapshot().Instances {
			if v.ID == id {
				return v.State
			}
		}
		return ""
	}
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
	const serving, rising = "org.example.serving", "org.example.rising"

	observe("serving")
	wait("one plugin to serve", func() bool { return state(serving) == pluginfacility.StateActive })
	observe("serving", "rising")
	wait("another to be on its way up", func() bool { return rt.count(rt.starts, "rising") == 1 })
	if pol := a.pluginPolicy(*a.cfg, tier, false); pol.Hold {
		t.Fatal("the policy holds before SAFE")
	}

	a.enterSafe("test: the chain does not verify")

	// .
	// .
	wait("the start in flight to be abandoned", func() bool { return rt.count(rt.cancelled, "rising") == 1 })
	pol := a.pluginPolicy(*a.cfg, tier, false)
	if !pol.Hold || !strings.Contains(pol.HoldWhy, "SAFE") || !strings.Contains(pol.HoldWhy, "the chain does not verify") {
		t.Fatalf("the policy does not carry the hold and its reason: %+v", pol)
	}
	select {
	case <-a.sweepPoke:
	default:
		t.Error("SAFE's entry asked for no pass over the plugins")
	}

	// .
	a.rescanPlugins(ctx)
	a.applyFacilitySnapshot()
	var row *dashboard.PluginPendingView
	for _, v := range a.pluginPendingViews() {
		if v.ID == rising {
			v := v
			row = &v
		}
	}
	if row == nil || row.Phase != lifeHeld || !strings.Contains(row.Summary, "SAFE") {
		t.Fatalf("what SAFE keeps from starting is not on the page as held: %+v", row)
	}

	// .
	// .
	if err := a.RetryPlugin(rising); err != nil {
		t.Fatal(err)
	}
	observe("serving", "rising", "arrived-during-safe")
	a.rescanPlugins(ctx)
	time.Sleep(150 * time.Millisecond)
	if n := rt.count(rt.starts, "rising"); n != 1 {
		t.Errorf("a child was started %d more time(s) while SAFE held", n-1)
	}
	if n := rt.count(rt.starts, "arrived-during-safe"); n != 0 {
		t.Error("a package that arrived during SAFE was started")
	}
	// .
	if state(serving) != pluginfacility.StateActive || rt.count(rt.stops, "serving") != 0 {
		t.Errorf("SAFE stopped what serves: state=%s stops=%d", state(serving), rt.count(rt.stops, "serving"))
	}
	// .
	observe("rising")
	wait("an uninstall during SAFE to stop the plugin", func() bool { return rt.count(rt.stops, "serving") == 1 })
}
