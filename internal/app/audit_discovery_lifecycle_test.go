// .
// .
// .
// .

package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type auditDiscoveryRuntime struct {
	a          *App
	realVerify bool
	starts     atomic.Int32
	pending    atomic.Bool
}

func (r *auditDiscoveryRuntime) Verify(ctx context.Context, pkg string) (pluginfacility.Evidence, error) {
	if r.realVerify {
		return (pluginRuntime{a: r.a}).Verify(ctx, pkg)
	}
	return pluginfacility.Evidence{ID: "org.example.audit", Version: pkg, Package: pkg, PackageHash: "hash:" + pkg, Tier: "T0"}, nil
}
func (r *auditDiscoveryRuntime) Prepare(_ context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: ev, Present: true}, nil
}
func (r *auditDiscoveryRuntime) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	return nil
}
func (r *auditDiscoveryRuntime) Start(_ context.Context, p pluginfacility.Prepared, _ *pluginfacility.Lease) (pluginfacility.Running, error) {
	r.starts.Add(1)
	ev := p.Evidence
	return &running{id: ev.ID, version: ev.Version, pkg: ev.Package, hash: ev.PackageHash, kind: "plugin", ap: &pluginhost.ActivePlugin{ID: ev.ID, Version: ev.Version}}, nil
}
func (r *auditDiscoveryRuntime) Health(_ context.Context, run pluginfacility.Running) error {
	if run.(*running).pkg == "bad" {
		return errors.New("audit replacement health failed")
	}
	return nil
}
func (r *auditDiscoveryRuntime) Redirect(prev, next pluginfacility.Running) error {
	var old *pluginhost.ActivePlugin
	if prev != nil {
		old = prev.(*running).ap
	}
	r.a.adoptPlugin(next.(*running), old)
	return nil
}
func (r *auditDiscoveryRuntime) Stop(_ context.Context, run pluginfacility.Running) (pluginfacility.Retirement, error) {
	r.a.releaseActivation(run.(*running))
	return pluginfacility.Retirement{Established: !r.pending.Load(), Residue: []string{"audit child remains unreaped"}}, nil
}

func auditDiscoveryApp(t *testing.T, autoload string, discover bool) (*App, *pluginfacility.Facility, *auditDiscoveryRuntime) {
	t.Helper()
	a := &App{cfg: &Config{Plugins: PluginsConfig{Autoload: autoload}}}
	rt := &auditDiscoveryRuntime{a: a, realVerify: discover}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	cfg := pluginfacility.Config{Runtime: rt, Spawn: func(fn func()) bool {
		if ctx.Err() != nil {
			return false
		}
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
		return true
	}}
	if discover {
		cfg.Discover = a.discoverPlugins
	}
	f := pluginfacility.New(cfg)
	a.facilityOnce.Do(func() { a.facility = f })
	f.Attach(ctx)
	t.Cleanup(func() { rt.pending.Store(false); cancel(); f.Close(); wg.Wait() })
	return a, f, rt
}
func auditDiscoveryWait(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
func auditDiscoveryExport(t *testing.T, a *App, name string) {
	t.Helper()
	if dir := os.Getenv("AIII_AUDIT_LIFECYCLE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		v := map[string]any{"autoload": "T0", "skips": []any{}, "catalog": []any{}, "pending": a.pluginPendingViews(), "installed": a.pluginViews(a.cfg)}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAuditDiscoveryNoneRefusesEveryTier(t *testing.T) {
	a := &App{}
	cfg := Config{Plugins: PluginsConfig{Autoload: "none"}}
	tier, _, _ := autoloadTier(cfg.Plugins.Autoload)
	pol := a.pluginPolicy(cfg, tier, false)
	for _, verified := range []string{"T0", "T1", "T2", "T3"} {
		if ok, why := pol.Allows(pluginfacility.Evidence{ID: "org.example.audit", Tier: verified}); ok {
			t.Errorf("autoload none admitted verified %s: %s", verified, why)
		}
	}
	// .
	cfg.Plugins.Autoload = "T2"
	pol = a.pluginPolicy(cfg, packagefmt.TierT2, false)
	for _, tc := range []struct {
		tier string
		want bool
	}{{"T0", false}, {"T1", false}, {"T2", true}, {"T3", true}} {
		if ok, _ := pol.Allows(pluginfacility.Evidence{Tier: tc.tier}); ok != tc.want {
			t.Errorf("T2 threshold for %s = %v", tc.tier, ok)
		}
	}
}
func TestAuditDiscoveryAppScanHonorsNone(t *testing.T) {
	dir := t.TempDir()
	pkg := buildResponderPkg(t, dir, "org.example.audit")
	installPluginDir(t, dir, "org.example.audit", pkg)
	t.Chdir(dir)
	a, f, rt := auditDiscoveryApp(t, "none", true)
	a.rescanPlugins(context.Background())
	auditDiscoveryWait(t, "skip or synthetic activation", func() bool { return len(f.Skips()) > 0 || rt.starts.Load() > 0 })
	if rt.starts.Load() != 0 {
		t.Error("actual App rescan verified a fresh package and called Runtime.Start under autoload none")
	}
	if len(f.Skips()) != 1 {
		t.Errorf("disabled package not surfaced as skipped: %+v", f.Snapshot())
	}
}
func TestAuditLifecyclePendingRetirementRemainsVisible(t *testing.T) {
	a, f, rt := auditDiscoveryApp(t, "T0", false)
	set := []pluginfacility.Observed{{ID: "org.example.audit", Package: "good", Hash: "hash:good"}}
	f.Observe(set, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "active", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateActive
	})
	rt.pending.Store(true)
	f.Observe(nil, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "pending retirement", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateDraining && len(s.Instances[0].Residue) > 0
	})
	a.applyFacilitySnapshot()
	auditDiscoveryExport(t, a, "draining")
	if len(a.pluginViews(a.cfg)) == 0 && len(a.pluginPendingViews()) == 0 {
		t.Error("unreaped retirement exists in facility but vanished from both installed and pending cards")
	}
	rt.pending.Store(false)
	f.Poke("synthetic child reaped")
	auditDiscoveryWait(t, "retirement cleanup", func() bool { return len(f.Snapshot().Instances) == 0 })
}
func TestAuditLifecycleFailedUpdateCarriesBackendEvidence(t *testing.T) {
	a, f, _ := auditDiscoveryApp(t, "T0", false)
	f.Observe([]pluginfacility.Observed{{ID: "org.example.audit", Package: "good", Hash: "hash:good"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "active predecessor", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateActive
	})
	f.Observe([]pluginfacility.Observed{{ID: "org.example.audit", Package: "bad", Hash: "hash:bad"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "candidate refusal", func() bool { s := f.Snapshot(); return len(s.Instances) == 1 && s.Instances[0].Refusal != nil })
	a.applyFacilitySnapshot()
	views := a.pluginViews(a.cfg)
	if len(views) != 1 || views[0].Lifecycle == nil || views[0].Lifecycle.Refusal == nil || !strings.Contains(views[0].Lifecycle.Refusal.Cause, "audit replacement health failed") {
		t.Fatalf("backend did not preserve candidate refusal: %+v", views)
	}
	auditDiscoveryExport(t, a, "failed-update")
}
