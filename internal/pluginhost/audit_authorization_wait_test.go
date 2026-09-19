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
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func auditAuthorizationWait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(what)
	}
}

// .
// .
func TestAuditAuthorizationRecheckedAfterOperationWait(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	if err := ap.inFlight.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(ap.inFlight.release) }
	checked := make(chan struct{})
	var checkedOnce sync.Once
	var valid atomic.Bool
	valid.Store(true)
	ap.Authorize(func() bool { v := valid.Load(); checkedOnce.Do(func() { close(checked) }); return v })
	done := make(chan tools.Result, 1)
	go func() {
		r, err := reg.Execute(context.Background(), "audit.declared", nil)
		if err != nil {
			r.Error = err.Error()
		}
		done <- r
	}()
	defer func() { release(); _ = ap.Deactivate(context.Background()) }()
	auditAuthorizationWait(t, checked, "call never reached authorization")
	valid.Store(false)
	before := ap.inv.(*auditCommitInvoker).calls.Load()
	release()
	select {
	case result := <-done:
		if got := ap.inv.(*auditCommitInvoker).calls.Load(); got != before {
			t.Errorf("queued call invoked guest after withdrawal: calls=%d result=%+v", got-before, result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued call did not settle")
	}
}

// .
// .
func TestAuditAuthorizationRecheckedAfterPredecessorPublicationWait(t *testing.T) {
	reg := newRegistry(t)
	prev := auditCommitActivation(t, reg)
	next := auditCommitActivation(t, reg)
	if err := prev.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	prev.pubMu.Lock()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(prev.pubMu.Unlock) }
	checked := make(chan struct{})
	var checkedOnce sync.Once
	var valid atomic.Bool
	valid.Store(true)
	next.Authorize(func() bool { v := valid.Load(); checkedOnce.Do(func() { close(checked) }); return v })
	done := make(chan error, 1)
	go func() { done <- next.Redirect(prev) }()
	defer func() {
		release()
		_ = next.Deactivate(context.Background())
		_ = prev.Deactivate(context.Background())
	}()
	auditAuthorizationWait(t, checked, "candidate never reached authorization")
	valid.Store(false)
	release()
	select {
	case err := <-done:
		if !errors.Is(err, ErrWithdrawn) {
			t.Errorf("withdrawn candidate displaced predecessor instead of refusing: err=%v superseded=%v", err, prev.superseded.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("redirect did not settle")
	}
	before := prev.inv.(*auditCommitInvoker).calls.Load()
	result, err := reg.Execute(context.Background(), "audit.declared", nil)
	if err != nil || result.Error != "" || prev.inv.(*auditCommitInvoker).calls.Load() != before+1 {
		t.Errorf("serving predecessor lost after refused intent: result=%+v err=%v predecessor_calls=%d", result, err, prev.inv.(*auditCommitInvoker).calls.Load()-before)
	}
}
