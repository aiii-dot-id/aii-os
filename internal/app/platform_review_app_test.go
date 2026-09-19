// .
// .
// .
// .
// .

package app

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewPredecessorStopPreservesSuccessorSection(t *testing.T) {
	const id = "id.example.section"
	a := &App{sections: sections.NewRegistry()}
	old := &running{id: id, pkg: "old.aiiospkg", kind: "section", sec: &sections.Section{Decl: sections.Decl{ID: id}}}
	next := &running{id: id, pkg: "new.aiiospkg", kind: "section", sec: &sections.Section{Decl: sections.Decl{ID: id}}}
	if err := a.sections.Register(old.sec); err != nil {
		t.Fatal(err)
	}
	h := pluginRuntime{a: a}
	if err := h.Redirect(old, next); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.sections.Get(id); !ok || got != next.sec {
		t.Fatal("successor not published")
	}
	if _, err := h.Stop(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if got, ok := a.sections.Get(id); !ok || got != next.sec {
		t.Fatal("predecessor retirement removed successor section route")
	}
}

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
	a := &App{facility: f, sweepPoke: make(chan struct{}, 1)}
	before := rt.calls.Load()
	if err := a.RetryPlugin(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.sweepPoke:
	case <-time.After(time.Second):
		t.Fatal("retry did not request sweep")
	}
	// .
	f.Rescan(pol)
	time.Sleep(200 * time.Millisecond)
	inert := rt.calls.Load() == before
	// .
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
	a := &App{subscribers: map[string]*pluginSubscriber{id: successor}}
	// .
	// .
	old := &running{id: id, pkg: "old.aiiospkg", kind: "plugin", ap: &pluginhost.ActivePlugin{ID: id}}
	if _, err := (pluginRuntime{a: a}).Stop(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if a.subscribers[id] != successor || stopped {
		t.Fatal("predecessor retirement stopped/deleted successor event subscriber")
	}
}
