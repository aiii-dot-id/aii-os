package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

func ownedSection(id string) *sections.Section {
	return &sections.Section{Decl: sections.Decl{ID: id}}
}

func TestASectionRouteBelongsToTheActivationThatRegisteredIt(t *testing.T) {
	const id = "id.example.section"
	a := &App{sections: sections.NewRegistry()}
	h := newPluginRuntime(a)
	ctx := context.Background()
	answers := func(when string, want *pluginhost.Activation) {
		t.Helper()
		got, ok := a.sections.Get(id)
		if want == nil {
			if ok {
				t.Fatalf("%s: the id is still answered", when)
			}
			return
		}
		if !ok || got != want.Section {
			t.Fatalf("%s: the id is not answered by the activation that should hold it", when)
		}
		a.pluginMu.Lock()
		meta, has := a.activeMeta[id]
		a.pluginMu.Unlock()
		if !has || meta.owner != want {
			t.Fatalf("%s: the metadata does not name that activation", when)
		}
	}
	first := &pluginhost.Activation{ID: id, Package: "plugins/x/one.aiiospkg", Section: ownedSection(id)}
	if err := h.Redirect(nil, first); err != nil {
		t.Fatal(err)
	}
	answers("first admission", first)

	// A candidate that never served is stopped; what serves is untouched.
	// It was built from the SAME PATH — an archive rewritten in place.
	failed := &pluginhost.Activation{ID: id, Package: first.Package, Section: ownedSection(id)}
	if _, err := h.Stop(ctx, failed); err != nil {
		t.Fatal(err)
	}
	answers("after a failed candidate's cleanup", first)

	// An ordinary update; the predecessor retires AFTER the commit, and
	// is asked to more than once.
	next := &pluginhost.Activation{ID: id, Package: "plugins/x/two.aiiospkg", Section: ownedSection(id)}
	if err := h.Redirect(first, next); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := h.Stop(ctx, first); err != nil {
			t.Fatal(err)
		}
		answers("after the predecessor's retirement", next)
	}

	// A successor that cannot be registered leaves the one that answers
	// answering: the predecessor is not removed on the way to a refusal.
	stale := &pluginhost.Activation{ID: id, Package: "plugins/x/three.aiiospkg", Section: ownedSection(id)}
	if err := h.Redirect(first, stale); err == nil {
		t.Fatal("a redirect from an activation that no longer holds the id was accepted")
	}
	answers("after a refused redirect", next)

	// The final uninstall takes the current owner's registration away.
	if _, err := h.Stop(ctx, next); err != nil {
		t.Fatal(err)
	}
	answers("after the uninstall", nil)
	a.pluginMu.Lock()
	_, has := a.activeMeta[id]
	a.pluginMu.Unlock()
	if has {
		t.Error("the uninstall left the metadata behind")
	}
}

func TestEventDeliveryBelongsToTheActivationThatStartedIt(t *testing.T) {
	const id = "id.example.subscriber"
	a := &App{}
	h := newPluginRuntime(a)
	ctx := context.Background()
	subs := []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"}}
	owner := func() *pluginhost.ActivePlugin {
		a.pluginMu.RLock()
		defer a.pluginMu.RUnlock()
		if s := a.subscribers[id]; s != nil {
			return s.owner
		}
		return nil
	}
	first := &pluginhost.ActivePlugin{ID: id, Subscriptions: subs}
	next := &pluginhost.ActivePlugin{ID: id, Subscriptions: subs}
	a.startSubscriber(first)
	a.startSubscriber(next) // the commit: delivery changes hands
	if owner() != next {
		t.Fatal("the successor does not hold the id's delivery after the commit")
	}
	// The predecessor retires late, twice; a candidate that never served
	// is stopped. Neither is the one delivering.
	for _, leaving := range []*pluginhost.ActivePlugin{first, first, {ID: id, Subscriptions: subs}} {
		if _, err := h.Stop(ctx, &pluginhost.Activation{ID: id, Plugin: leaving}); err != nil {
			t.Fatal(err)
		}
		if owner() != next {
			t.Fatal("an activation that was not delivering took the delivery of the one that was")
		}
	}
	// The final uninstall ends the owner's delivery.
	if _, err := h.Stop(ctx, &pluginhost.Activation{ID: id, Plugin: next}); err != nil {
		t.Fatal(err)
	}
	if owner() != nil {
		t.Fatal("the uninstall left the delivery running")
	}
	// A successor that declares NO subscriptions still ends its
	// predecessor's: one id names one subscriber, and the id is its now.
	a.startSubscriber(first)
	a.startSubscriber(&pluginhost.ActivePlugin{ID: id})
	if owner() != nil {
		t.Error("a predecessor's delivery outlived the commit of a successor that subscribes to nothing")
	}
}

func TestEveryPolicyInputMovesTheRevisionAndNothingElseDoes(t *testing.T) {
	a := &App{}
	var cfg Config
	cfg.Plugins.Autoload = "T1"
	rev := a.pluginPolicy(cfg, 1, false).Revision
	for i := 0; i < 3; i++ {
		// A rescan of an unchanged configuration asks nothing new — and
		// so resets no retry budget.
		if got := a.pluginPolicy(cfg, 1, false).Revision; got != rev {
			t.Fatalf("reading the same configuration again moved the revision %d -> %d", rev, got)
		}
	}
	for name, change := range map[string]func(){
		"the autoload level": func() { cfg.Plugins.Autoload = "T2" },
		"the reserve":        func() { cfg.Plugins.Runtime.AdmissionMemoryReserveBytes = 1 << 30 },
		"the budget":         func() { cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes = 4 << 30 },
		"the cap on starts":  func() { cfg.Plugins.Runtime.MaxConcurrentStarts = 2 },
	} {
		change()
		pol := a.pluginPolicy(cfg, 1, false)
		if pol.Revision != rev+1 {
			t.Errorf("changing %s moved the revision %d -> %d, want one step", name, rev, pol.Revision)
		}
		rev = pol.Revision
		if got := a.pluginPolicy(cfg, 1, false).Revision; got != rev {
			t.Errorf("after changing %s, reading it again moved the revision", name)
		}
	}
	pol := a.pluginPolicy(cfg, 1, false)
	if pol.Admission.ReserveBytes != 1<<30 || pol.Admission.BudgetBytes != 4<<30 || pol.Admission.MaxConcurrentStarts != 2 {
		t.Errorf("the policy does not carry what was configured: %+v", pol.Admission)
	}
}
