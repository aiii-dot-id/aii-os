// .
// .
// .
// .

package pluginhost

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
type reviewActiveRetirementRuntime struct {
	*auditWithdrawalRuntime
	stopEntered chan struct{}
	stopOnce    sync.Once
	pending     atomic.Bool
}

func (r *reviewActiveRetirementRuntime) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	return r.ap.Redirect(nil)
}

func (r *reviewActiveRetirementRuntime) Stop(ctx context.Context, _ pluginfacility.Running) (pluginfacility.Retirement, error) {
	r.stopOnce.Do(func() { close(r.stopEntered) })
	if r.pending.Load() {
		return pluginfacility.Retirement{Established: false, Residue: []string{"resident session remains open"}}, nil
	}
	err := r.ap.Deactivate(ctx)
	return pluginfacility.Retirement{Established: err == nil}, err
}

func TestReviewRetiringActivationRejectsNewCalls(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	rt := &reviewActiveRetirementRuntime{auditWithdrawalRuntime: &auditWithdrawalRuntime{ap: ap}, stopEntered: make(chan struct{})}
	rt.pending.Store(true)
	f := pluginfacility.New(pluginfacility.Config{Runtime: rt})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	defer func() { rt.pending.Store(false); cancel(); f.Close(); _ = ap.Deactivate(context.Background()) }()
	f.Observe([]pluginfacility.Observed{{ID: ap.ID, Package: "one", Hash: "hash"}}, pluginfacility.Policy{Revision: 1})
	deadline := time.Now().Add(3 * time.Second)
	for {
		views := f.Snapshot().Instances
		if len(views) == 1 && views[0].State == pluginfacility.StateActive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("activation never became active")
		}
		time.Sleep(time.Millisecond)
	}
	f.Observe(nil, pluginfacility.Policy{Revision: 2})
	auditAuthorizationWait(t, rt.stopEntered, "retirement never started")
	deadline = time.Now().Add(3 * time.Second)
	for {
		views := f.Snapshot().Instances
		if len(views) == 1 && views[0].State == pluginfacility.StateDraining {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unresolved retirement was not published as draining")
		}
		time.Sleep(time.Millisecond)
	}
	before := ap.inv.(*auditCommitInvoker).calls.Load()
	result, err := reg.Execute(context.Background(), "audit.declared", nil)
	if got := ap.inv.(*auditCommitInvoker).calls.Load(); got != before {
		t.Fatalf("retiring activation accepted a NEW guest call: calls=%d authorized=%v result=%+v err=%v", got-before, ap.isAuthorized(), result, err)
	}
}
