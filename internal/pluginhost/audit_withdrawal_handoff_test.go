// .
// .
// .
// .
// .
// .
// .

package pluginhost

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"sync"
	"testing"
)

// .
// .
// .
type auditWithdrawalHandoffRuntime struct {
	*auditWithdrawalRuntime
	lease       *pluginfacility.Lease
	stopEntered chan struct{}
	allowStop   chan struct{}
	stopOnce    sync.Once
}

func (r *auditWithdrawalHandoffRuntime) Start(ctx context.Context, p pluginfacility.Prepared, lease *pluginfacility.Lease) (pluginfacility.Running, error) {
	r.lease = lease
	return r.auditWithdrawalRuntime.Start(ctx, p, lease)
}
func (r *auditWithdrawalHandoffRuntime) Stop(ctx context.Context, running pluginfacility.Running) (pluginfacility.Retirement, error) {
	r.stopOnce.Do(func() { close(r.stopEntered) })
	<-r.allowStop
	return r.auditWithdrawalRuntime.Stop(ctx, running)
}
func TestAuditWithdrawalSelectionSurvivesActivationCompletion(t *testing.T) {
	for _, change := range []string{"policy", "uninstall"} {
		t.Run(change, func(t *testing.T) {
			reg := newRegistry(t)
			ap := auditCommitActivation(t, reg)
			base := &auditWithdrawalRuntime{ap: ap, entered: make(chan struct{}), published: make(chan error, 1), finish: make(chan struct{}), stopped: make(chan error, 4)}
			rt := &auditWithdrawalHandoffRuntime{auditWithdrawalRuntime: base, stopEntered: make(chan struct{}), allowStop: make(chan struct{})}
			selected := make(chan struct{})
			allowWithdrawal := make(chan struct{})
			var selectedOnce sync.Once
			pluginfacility.AuditAfterWithdrawalSelected = func() { selectedOnce.Do(func() { close(selected) }); <-allowWithdrawal }
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			f := pluginfacility.New(pluginfacility.Config{Runtime: rt, Spawn: func(fn func()) bool {
				if ctx.Err() != nil {
					return false
				}
				wg.Add(1)
				go func() { defer wg.Done(); fn() }()
				return true
			}})
			f.Attach(ctx)
			var finishOnce, withdrawOnce, stopOnce sync.Once
			finish := func() { finishOnce.Do(func() { close(base.finish) }) }
			withdraw := func() { withdrawOnce.Do(func() { close(allowWithdrawal) }) }
			stop := func() { stopOnce.Do(func() { close(rt.allowStop) }) }
			observed := make(chan struct{})
			defer func() {
				finish()
				withdraw()
				stop()
				cancel()
				f.Close()
				wg.Wait()
				pluginfacility.AuditAfterWithdrawalSelected = nil
			}()
			set := []pluginfacility.Observed{{ID: ap.ID, Package: "one", Hash: "hash"}}
			f.Observe(set, pluginfacility.Policy{Revision: 1})
			auditAuthorizationWait(t, base.entered, "initial redirect not entered")
			if err := <-base.published; err != nil {
				t.Fatal(err)
			}
			go func() {
				defer close(observed)
				if change == "policy" {
					f.Observe(set, pluginfacility.Policy{Revision: 2, Safe: true})
				} else {
					f.Observe(nil, pluginfacility.Policy{Revision: 2})
				}
			}()
			auditAuthorizationWait(t, selected, "Observe did not select candidate withdrawal")
			finish()
			// .
			// .
			auditAuthorizationWait(t, rt.stopEntered, "completed activation was not sent for retirement")
			withdraw()
			auditAuthorizationWait(t, observed, "Observe did not return")
			before := ap.inv.(*auditCommitInvoker).calls.Load()
			result, err := reg.Execute(context.Background(), "audit.declared", nil)
			authorized := rt.lease.Authorized()
			calls := ap.inv.(*auditCommitInvoker).calls.Load() - before
			t.Logf("selected withdrawal after %s: authorized=%v calls=%d result_error=%q error=%v", change, authorized, calls, result.Error, err)
			if authorized || calls != 0 {
				t.Errorf("selected candidate escaped withdrawal after activation completed: authorized=%v guest_calls=%d", authorized, calls)
			}
		})
	}
}
