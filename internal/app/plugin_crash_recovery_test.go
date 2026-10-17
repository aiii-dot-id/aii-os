//go:build !android && !ios

package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestRealWorkerCeilingReachesTheCardAndTryAgainRestoresService(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	if runtime.GOOS == "windows" {
		worker += ".exe"
	}
	build := exec.Command("go", "build", "-o", worker, "./cmd/aii-plugin-worker")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v %s", err, out)
	}
	const id = "org.example.crashproof"
	pkg := buildResponderPkg(t, dir, id)
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	a := &App{pluginToolReg: reg, pluginOpts: &pluginhost.Options{WorkerBinary: worker}}
	f := pluginfacility.New(pluginfacility.Config{Runtime: newPluginRuntime(a)})
	a.facilityOnce.Do(func() { a.facility = f })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(func() { cancel(); f.Close() })
	f.Attach(ctx)
	ev, err := newPluginRuntime(a).Verify(ctx, pkg)
	if err != nil {
		t.Fatal(err)
	}
	f.Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: dir, Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}}, pluginfacility.Policy{Revision: 1})
	wait := func(what string, ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatalf("%s: %v; snapshot=%+v", what, ctx.Err(), f.Snapshot())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	current := func() *pluginhost.ActivePlugin {
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		for _, p := range a.plugins {
			if p.ID == id {
				return p
			}
		}
		return nil
	}
	wait("first activation", func() bool { return current() != nil })
	first := current()
	names := first.Tools()
	if len(names) != 1 {
		t.Fatalf("fixture tools: %v", names)
	}
	invoke := func() {
		t.Helper()
		result, err := reg.Execute(ctx, names[0], nil)
		if err != nil || result.Error != "" || !strings.Contains(result.Output, `"echoed":true`) {
			t.Fatalf("plugin route unavailable: %v %+v", err, result)
		}
	}
	invoke()
	for i := 0; i < 6; i++ {
		pid := first.SupervisedPid()
		if killed, err := first.KillSupervisedChild(); err != nil || !killed {
			t.Fatalf("no live child was killed: %v", err)
		}
		if i < 5 {
			wait("replacement child serves", func() bool {
				if first.SupervisedPid() == 0 || first.SupervisedPid() == pid {
					return false
				}
				result, err := reg.Execute(ctx, names[0], nil)
				if pluginhosttest.UnavailableNotStopped(err) {
					return false
				} // provably not dispatched
				if err != nil || result.Error != "" || !strings.Contains(result.Output, `"echoed":true`) {
					t.Fatalf("replacement route: %v %+v", err, result)
				}
				return true
			})
		}
	}
	select {
	case <-first.Terminated():
	case <-ctx.Done():
		t.Fatal("restart ceiling never terminated the supervisor")
	}
	wait("crashed refusal on the plugin card", func() bool {
		a.applyFacilitySnapshot()
		for _, v := range a.pluginPendingViews() {
			if v.ID == id {
				return v.Phase == lifeRefused && v.Refusal != nil && v.Refusal.Stage == "crashed" && v.RetryAt != "" && strings.Contains(v.Refusal.Cause, "5 restarts")
			}
		}
		return false
	})
	if current() != nil {
		t.Fatal("dead activation still appears active")
	}
	if err := a.RetryPlugin(id); err != nil {
		t.Fatal(err)
	}
	wait("Try again starts a fresh activation", func() bool { p := current(); return p != nil && p != first })
	invoke()
	if current().Restarts() != 0 {
		t.Fatal("new activation inherited the old process's restart count")
	}
	f.Close()
	if current() != nil {
		t.Fatal("shutdown retained the serving activation")
	}
}
