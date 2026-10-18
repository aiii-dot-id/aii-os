package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// alwaysBusy is a plugin runtime whose one activation is in the middle of
// a call for as long as anyone asks it to stop.
type alwaysBusy struct{}

type busyActivation struct{ id string }

func (b *busyActivation) PluginID() string { return b.id }

func (alwaysBusy) Verify(_ context.Context, pkg string) (pluginfacility.Evidence, error) {
	return pluginfacility.Evidence{ID: "org.example.busy", Version: "1.0.0", Package: pkg, PackageHash: "sha256:" + pkg, ManifestHash: "sha256:m", Tier: "T0"}, nil
}
func (alwaysBusy) Prepare(_ context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: ev, Present: true}, nil
}
func (alwaysBusy) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	return nil
}
func (alwaysBusy) Start(_ context.Context, p pluginfacility.Prepared, _ *pluginfacility.Lease) (pluginfacility.Running, error) {
	return &busyActivation{id: p.Evidence.ID}, nil
}
func (alwaysBusy) Health(context.Context, pluginfacility.Running) error { return nil }
func (alwaysBusy) Redirect(_, _ pluginfacility.Running) error           { return nil }
func (alwaysBusy) Stop(context.Context, pluginfacility.Running) (pluginfacility.Retirement, error) {
	return pluginfacility.Retirement{Established: false, Residue: []string{"retirement waits for an admitted invocation to finish"}, Busy: true}, nil
}

// A PLUGIN THAT WOULD NOT STOP IS A STOP THAT FAILED. A teardown that
// failed leaves with 1 and not with 0: a store that did not close, a
// ledger that did not, a plugin that would not stop. The facility's close
// answered nothing, so a plugin left running with a call in flight was a
// clean exit to the service manager and to every drill. The application
// now asks the facility what it could not stop, and says it.
func TestAPluginTheFacilityCouldNotStopFailsTheTeardown(t *testing.T) {
	a := New(defaultConfig())
	f := pluginfacility.New(pluginfacility.Config{Runtime: alwaysBusy{}, RetireTimeout: 200 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Attach(ctx)
	f.Observe([]pluginfacility.Observed{{ID: "org.example.busy", Dir: "plugins/org.example.busy", Package: "busy.aiiospkg", Hash: "sha256:busy.aiiospkg", ManifestHash: "sha256:m"}}, pluginfacility.Policy{Revision: 1})
	deadline := time.Now().Add(10 * time.Second)
	for {
		snap := f.Snapshot()
		serving := false
		for _, inst := range snap.Instances {
			if inst.ID == "org.example.busy" && inst.State == pluginfacility.StateActive {
				serving = true
			}
		}
		if serving {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin never served: %+v", snap.Instances)
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.pluginMu.Lock()
	a.facility = f
	a.pluginMu.Unlock()

	err := a.closeLiveResources()
	if err == nil || !strings.Contains(err.Error(), "plugins that would not stop: org.example.busy") || !strings.Contains(err.Error(), "waits for an admitted invocation") {
		t.Fatalf("a plugin the facility could not stop must fail the teardown by name, with what it held: %v", err)
	}
}
