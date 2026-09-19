package pluginhost

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
)

// .
// .
// .
// .
// .

func TestAWithdrawnActivationPublishesNothingAndIsNotDispatchedTo(t *testing.T) {
	spec := broker.PublishedTool{Name: "runtime.list", Summary: "list", Effects: broker.EffectsReadInternal}
	t.Run("withdrawn before the commit: nothing is opened or registered", func(t *testing.T) {
		reg := newRegistry(t)
		ap := auditCommitActivation(t, reg)
		var ok atomic.Bool
		ap.Authorize(ok.Load)
		if err := ap.Redirect(nil); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("a withdrawn activation was published: %v", err)
		}
		if ap.admitted.Load() {
			t.Error("the refused commit opened the dispatch gate")
		}
		if _, found := reg.Get("audit.declared"); found {
			t.Error("the refused commit registered a route")
		}
		if err := ap.registerTools(); !errors.Is(err, ErrWithdrawn) {
			t.Errorf("the first-activation commit published a withdrawn activation: %v", err)
		}
		if _, err := ap.PublishTool(spec); err == nil {
			t.Error("a withdrawn activation published a tool at run time")
		}
	})
	t.Run("withdrawn after the swap: no NEW call reaches it", func(t *testing.T) {
		reg := newRegistry(t)
		ap := auditCommitActivation(t, reg)
		var ok atomic.Bool
		ok.Store(true)
		ap.Authorize(ok.Load)
		if err := ap.Redirect(nil); err != nil {
			t.Fatal(err)
		}
		if r, err := reg.Execute(context.Background(), "audit.declared", nil); err != nil || r.Error != "" {
			t.Fatalf("an authorized, committed operation was refused: %+v %v", r, err)
		}
		before := ap.inv.(*auditCommitInvoker).calls.Load()
		ok.Store(false)
		r, err := reg.Execute(context.Background(), "audit.declared", nil)
		if err != nil || !strings.Contains(r.Error, "withdrawn") {
			t.Fatalf("a withdrawn release answered a new call: %+v %v", r, err)
		}
		if got := ap.inv.(*auditCommitInvoker).calls.Load(); got != before {
			t.Errorf("the guest was invoked %d more time(s) after the withdrawal", got-before)
		}
		if _, err := ap.PublishTool(spec); err == nil {
			t.Error("a withdrawn release published a tool")
		}
		if err := ap.Deactivate(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("never withdrawn, and an activation nobody owns: unaffected", func(t *testing.T) {
		reg := newRegistry(t)
		ap := auditCommitActivation(t, reg)
		if err := ap.Redirect(nil); err != nil {
			t.Fatal(err)
		}
		if r, err := reg.Execute(context.Background(), "audit.declared", nil); err != nil || r.Error != "" {
			t.Fatalf("%+v %v", r, err)
		}
		if err := ap.Deactivate(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
