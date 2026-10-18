package app

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"sync/atomic"
	"testing"
	"time"
)

type reviewRefusingRuntime struct{ calls atomic.Int64 }

func (r *reviewRefusingRuntime) Verify(context.Context, string) (pluginfacility.Evidence, error) {
	r.calls.Add(1)
	return pluginfacility.Evidence{}, errors.New("injected verification refusal")
}
func (r *reviewRefusingRuntime) Prepare(context.Context, pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	panic("not reached")
}
func (r *reviewRefusingRuntime) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	panic("not reached")
}
func (r *reviewRefusingRuntime) Start(context.Context, pluginfacility.Prepared, *pluginfacility.Lease) (pluginfacility.Running, error) {
	panic("not reached")
}
func (r *reviewRefusingRuntime) Health(context.Context, pluginfacility.Running) error {
	panic("not reached")
}
func (r *reviewRefusingRuntime) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	panic("not reached")
}
func (r *reviewRefusingRuntime) Stop(context.Context, pluginfacility.Running) (pluginfacility.Retirement, error) {
	panic("not reached")
}

func TestReviewOperatorRetryReachesFacility(t *testing.T) {
	const id = "id.example.retry"
	rt := &reviewRefusingRuntime{}
	f := pluginfacility.New(pluginfacility.Config{Runtime: rt})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	pol := pluginfacility.Policy{Revision: 1}
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "one.aiiospkg", Hash: "one"}}, pol)
	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait("initial refusal absent", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateRefused
	})
	a := &App{pluginState: pluginState{facility: f}, sweepPoke: make(chan struct{}, 1)}
	before := rt.calls.Load()
	if err := a.RetryPlugin(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.sweepPoke:
	case <-time.After(time.Second):
		t.Fatal("retry did not request sweep")
	}
	// Deliver the rescan the production handler requested, using unchanged input.
	f.Rescan(pol)
	time.Sleep(200 * time.Millisecond)
	inert := rt.calls.Load() == before
	// Control: calling the real facility retry reaches the runtime again.
	f.Retry(id)
	wait("facility retry did not reattempt", func() bool { return rt.calls.Load() > before })
	if inert {
		t.Fatal("RetryPlugin reported success but never reset facility refusal/budget; direct Facility.Retry did")
	}
}

func TestReviewAdmissionPolicyChangesInvalidateIntent(t *testing.T) {
	a := &App{}
	var cfg Config
	cfg.Plugins.Autoload = "T1"
	cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = 1 << 30
	before := a.pluginPolicy(cfg, 1, false)
	cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = 4 << 30
	after := a.pluginPolicy(cfg, 1, false)
	if before.Revision == after.Revision {
		t.Fatal("raising admission budget leaves policy intent unchanged; prior permanent insufficient-budget refusal still stands")
	}
}

func TestReviewPredecessorStopPreservesSuccessorSubscriber(t *testing.T) {
	const id = "id.example.subscriber"
	stopped := false
	successor := &pluginSubscriber{id: id, stop: func() { stopped = true }}
	a := &App{pluginState: pluginState{subscribers: map[string]*pluginSubscriber{id: successor}}}
	// Quiet empty activation closes without broker or process; no production
	// child is needed to exercise the adapter's subscriber ownership.
	old := &pluginhost.Activation{ID: id, Package: "old.aiiospkg", Plugin: &pluginhost.ActivePlugin{ID: id}}
	if _, err := newPluginRuntime(a).Stop(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if a.subscribers[id] != successor || stopped {
		t.Fatal("predecessor retirement stopped/deleted successor event subscriber")
	}
}
