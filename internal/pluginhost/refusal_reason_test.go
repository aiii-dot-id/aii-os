package pluginhost

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestARefusalBeforeDispatchCarriesItsReason(t *testing.T) {
	reg := newRegistry(t)
	var safe atomic.Bool
	reg.SetSafeSource(func() (string, bool) { return "fixture", safe.Load() })
	ap := auditCommitActivation(t, reg)
	input, err := jsonschema.Compile(map[string]any{"type": "object",
		"properties": map[string]any{"address": map[string]any{"type": "string", "pattern": "^[0-9]+$"}}})
	if err != nil {
		t.Fatal(err)
	}
	ap.pending = append(ap.pending, pendingTool{tool: ap.newOperationTool("audit.typed", "declared", "audit", &opDescriptor{input: input}, tools.Discovery{})})
	invoked := ap.inv.(*auditCommitInvoker)
	want := func(what string, r tools.Result, err error, code string) {
		t.Helper()
		if err != nil || r.Error == "" || r.ReasonCode != code {
			t.Fatalf("%s: answered %+v %v, want a refusal typed %s", what, r, err, code)
		}
	}

	r, err := ap.pending[0].tool.Execute(context.Background(), nil)
	want("a release not yet admitted", r, err, ReasonNotAdmitted)
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })

	r, err = reg.Execute(context.Background(), "audit.typed", map[string]any{"address": "@friend"})
	want("arguments outside the declared input", r, err, broker.ReasonArgumentInvalid)

	if err := ap.inFlight.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = reg.Execute(cancelled, "audit.declared", nil)
	want("cancelled while waiting for the slot", r, err, ReasonCancelledBeforeDispatch)

	// Changed while the call waited for the slot: the registry's own gates
	// were passed on arrival, and the check after the wait refuses.
	for _, tc := range []struct {
		what         string
		change, undo func()
		code         string
	}{
		{"the operator disabled the tool", func() { reg.SetToolEnabled("audit.declared", false) }, func() { reg.SetToolEnabled("audit.declared", true) }, tools.ReasonOperatorDisabled},
		{"SAFE was entered", func() { safe.Store(true) }, func() { safe.Store(false) }, tools.ReasonSafeSuspended},
	} {
		arrived := make(chan struct{})
		var once sync.Once
		ap.Authorize(func() bool { once.Do(func() { close(arrived) }); return true })
		done := make(chan tools.Result, 1)
		go func() {
			r, err := reg.Execute(context.Background(), "audit.declared", nil)
			if err != nil {
				r.Error = err.Error()
			}
			done <- r
		}()
		auditAuthorizationWait(t, arrived, tc.what+": the call never arrived")
		tc.change()
		ap.inFlight.release()
		r := <-done
		tc.undo()
		want(tc.what, r, nil, tc.code)
		if err := ap.inFlight.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ap.inFlight.release()
	if n := invoked.calls.Load(); n != 0 {
		t.Fatalf("the guest was invoked %d time(s) by calls refused before dispatch", n)
	}
}
