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
// .
// .
// .
// .

package pluginhost

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
type auditWithdrawalRuntime struct {
	ap        *ActivePlugin
	entered   chan struct{}
	published chan error
	finish    chan struct{}
	stopped   chan error
}

func (r *auditWithdrawalRuntime) Verify(context.Context, string) (pluginfacility.Evidence, error) {
	return pluginfacility.Evidence{ID: r.ap.ID, Package: "one", PackageHash: "hash", Version: "1", Tier: "T0"}, nil
}
func (r *auditWithdrawalRuntime) Prepare(_ context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	return pluginfacility.Prepared{Evidence: ev, Present: true}, nil
}
func (r *auditWithdrawalRuntime) Acquire(context.Context, pluginfacility.Prepared, func(pluginfacility.MaterialStatus)) error {
	return nil
}

type auditWithdrawalRunning struct{ id string }

func (r *auditWithdrawalRunning) PluginID() string { return r.id }
func (r *auditWithdrawalRuntime) Start(_ context.Context, _ pluginfacility.Prepared, lease *pluginfacility.Lease) (pluginfacility.Running, error) {
	// .
	r.ap.Authorize(lease.Authorized)
	return &auditWithdrawalRunning{id: r.ap.ID}, nil
}
func (r *auditWithdrawalRuntime) Health(context.Context, pluginfacility.Running) error { return nil }
func (r *auditWithdrawalRuntime) Redirect(pluginfacility.Running, pluginfacility.Running) error {
	close(r.entered)
	err := r.ap.Redirect(nil)
	r.published <- err
	<-r.finish
	return err
}
func (r *auditWithdrawalRuntime) Stop(ctx context.Context, _ pluginfacility.Running) (pluginfacility.Retirement, error) {
	err := r.ap.Deactivate(ctx)
	r.stopped <- err
	return pluginfacility.Retirement{Established: err == nil}, err
}

func TestAuditWithdrawalCannotPublishAnInvokableHostRoute(t *testing.T) {
	for _, change := range []string{"policy", "uninstall"} {
		t.Run(change, func(t *testing.T) {
			reg := newRegistry(t)
			ap := auditCommitActivation(t, reg)
			rt := &auditWithdrawalRuntime{ap: ap, entered: make(chan struct{}), published: make(chan error, 1), finish: make(chan struct{}), stopped: make(chan error, 2)}
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
			var unlockOnce, finishOnce sync.Once
			ap.pubMu.Lock()
			unlock := func() { unlockOnce.Do(ap.pubMu.Unlock) }
			finish := func() { finishOnce.Do(func() { close(rt.finish) }) }
			defer func() { unlock(); finish(); cancel(); f.Close(); wg.Wait() }()
			set := []pluginfacility.Observed{{ID: ap.ID, Package: "one", Hash: "hash"}}
			f.Observe(set, pluginfacility.Policy{Revision: 1})
			select {
			case <-rt.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("redirect not entered")
			}
			// .
			// .
			if change == "policy" {
				f.Observe(set, pluginfacility.Policy{Revision: 2, Safe: true})
			} else {
				f.Observe(nil, pluginfacility.Policy{Revision: 2})
			}
			unlock()
			select {
			case err := <-rt.published:
				// .
				// .
				if err != nil && !errors.Is(err, ErrWithdrawn) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("redirect did not finish")
			}
			result, err := reg.Execute(context.Background(), "audit.declared", nil)
			calls := ap.inv.(*auditCommitInvoker).calls.Load()
			if err == nil && result.Error == "" && calls > 0 {
				t.Errorf("actual host registry dispatched %d operation after %s Observe returned; result=%+v", calls, change, result)
			}
			finish()
			select {
			case err := <-rt.stopped:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cleanup did not finish")
			}
			if _, ok := reg.Get("audit.declared"); ok {
				t.Error("test cleanup left its route")
			}
		})
	}
}
