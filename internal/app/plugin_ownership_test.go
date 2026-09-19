package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

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

func ownedSection(id string) *sections.Section {
	return &sections.Section{Decl: sections.Decl{ID: id}}
}

func TestASectionRouteBelongsToTheActivationThatRegisteredIt(t *testing.T) {
	const id = "id.example.section"
	a := &App{sections: sections.NewRegistry()}
	h := pluginRuntime{a: a}
	ctx := context.Background()
	answers := func(when string, want *running) {
		t.Helper()
		got, ok := a.sections.Get(id)
		if want == nil {
			if ok {
				t.Fatalf("%s: the id is still answered", when)
			}
			return
		}
		if !ok || got != want.sec {
			t.Fatalf("%s: the id is not answered by the activation that should hold it", when)
		}
		a.pluginMu.Lock()
		meta, has := a.activeMeta[id]
		a.pluginMu.Unlock()
		if !has || meta.owner != want {
			t.Fatalf("%s: the metadata does not name that activation", when)
		}
	}
	first := &running{id: id, pkg: "plugins/x/one.aiiospkg", kind: "section", sec: ownedSection(id)}
	if err := h.Redirect(nil, first); err != nil {
		t.Fatal(err)
	}
	answers("first admission", first)

	// .
	// .
	failed := &running{id: id, pkg: first.pkg, kind: "section", sec: ownedSection(id)}
	if _, err := h.Stop(ctx, failed); err != nil {
		t.Fatal(err)
	}
	answers("after a failed candidate's cleanup", first)

	// .
	// .
	next := &running{id: id, pkg: "plugins/x/two.aiiospkg", kind: "section", sec: ownedSection(id)}
	if err := h.Redirect(first, next); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := h.Stop(ctx, first); err != nil {
			t.Fatal(err)
		}
		answers("after the predecessor's retirement", next)
	}

	// .
	// .
	stale := &running{id: id, pkg: "plugins/x/three.aiiospkg", kind: "section", sec: ownedSection(id)}
	if err := h.Redirect(first, stale); err == nil {
		t.Fatal("a redirect from an activation that no longer holds the id was accepted")
	}
	answers("after a refused redirect", next)

	// .
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
	h := pluginRuntime{a: a}
	ctx := context.Background()
	subs := []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"}}
	owner := func() *pluginhost.ActivePlugin {
		a.subMu.RLock()
		defer a.subMu.RUnlock()
		if s := a.subscribers[id]; s != nil {
			return s.owner
		}
		return nil
	}
	first := &pluginhost.ActivePlugin{ID: id, Subscriptions: subs}
	next := &pluginhost.ActivePlugin{ID: id, Subscriptions: subs}
	a.startSubscriber(first)
	a.startSubscriber(next)
	if owner() != next {
		t.Fatal("the successor does not hold the id's delivery after the commit")
	}
	// .
	// .
	for _, leaving := range []*pluginhost.ActivePlugin{first, first, {ID: id, Subscriptions: subs}} {
		if _, err := h.Stop(ctx, &running{id: id, kind: "plugin", ap: leaving}); err != nil {
			t.Fatal(err)
		}
		if owner() != next {
			t.Fatal("an activation that was not delivering took the delivery of the one that was")
		}
	}
	// .
	if _, err := h.Stop(ctx, &running{id: id, kind: "plugin", ap: next}); err != nil {
		t.Fatal(err)
	}
	if owner() != nil {
		t.Fatal("the uninstall left the delivery running")
	}
	// .
	// .
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
		// .
		// .
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
