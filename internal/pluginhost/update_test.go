package pluginhost

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
)

// .
// .
// .
// .
func TestActivateShadowRegistersNothingThenRedirects(t *testing.T) {
	reg := newRegistry(t)
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("Activate the running release: %v", err)
	}
	const name = "pl_org_example_up_ping"
	if _, ok := reg.Get(name); !ok {
		t.Fatalf("the running release registered %s", name)
	}

	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("ActivateShadow: %v", err)
	}
	if len(newAp.ToolNames) != 0 {
		t.Fatalf("a shadow registers no names, got %v", newAp.ToolNames)
	}
	if got, _ := reg.Get(name); got == nil {
		t.Fatal("the running release still owns the name during the shadow")
	}
	if err := newAp.Health(context.Background()); err != nil {
		t.Fatalf("the candidate is healthy: %v", err)
	}

	if err := newAp.Redirect(oldAp); err != nil {
		t.Fatalf("redirect: %v", err)
	}
	if len(newAp.ToolNames) != 1 || newAp.ToolNames[0] != name {
		t.Fatalf("the candidate owns the name after redirect: %v", newAp.ToolNames)
	}
	if len(oldAp.ToolNames) != 0 {
		t.Fatalf("the predecessor released its names: %v", oldAp.ToolNames)
	}
	if !oldAp.WaitIdle(2 * time.Second) {
		t.Fatal("an idle predecessor drains at once")
	}
	if err := oldAp.CloseQuiet(context.Background()); err != nil {
		t.Fatalf("CloseQuiet: %v", err)
	}
	if _, ok := reg.Get(name); !ok {
		t.Fatal("the name survives the predecessor's quiet close — it is the candidate's now")
	}
	t.Cleanup(func() { _ = newAp.Deactivate(context.Background()) })
}

// .
// .
func TestHealthRefusesAnOperationlessCandidate(t *testing.T) {
	reg := newRegistry(t)
	ap, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.empty", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatalf("ActivateShadow: %v", err)
	}
	ap.pending = nil
	if err := ap.Health(context.Background()); err == nil {
		t.Fatal("health must refuse a candidate that exposes no operations")
	}
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestASupersededReleaseCannotPublishOrWithdraw(t *testing.T) {
	reg := newRegistry(t)
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.up": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	// .
	if _, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.one", Summary: "s", Effects: "read.internal"}); err != nil {
		t.Fatalf("the running release publishes: %v", err)
	}

	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	if err := newAp.Redirect(oldAp); err != nil {
		t.Fatalf("redirect: %v", err)
	}

	if _, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.two", Summary: "s", Effects: "read.internal"}); err == nil {
		t.Fatal("a superseded release must not admit new work")
	} else if !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("and it must say why: %v", err)
	}
	if err := oldAp.WithdrawTool("dyn.one"); err == nil {
		t.Fatal("a superseded release must not withdraw, the name may be the successor's now")
	}
	// .
	if _, err := newAp.PublishTool(broker.PublishedTool{Name: "dyn.three", Summary: "s", Effects: "read.internal"}); err != nil {
		t.Fatalf("the successor publishes: %v", err)
	}
}

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
func TestAnOverlappingPublishOrWithdrawCannotSlipThroughRetirement(t *testing.T) {
	reg := newRegistry(t)
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.up": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	oldAp, err := Activate(context.Background(), buildPkg(t, "org.example.up", "0.1.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}
	newAp, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.up", "0.2.0", []string{"ping"}, "responder.wasm"), reg, &Options{Broker: h})
	if err != nil {
		t.Fatal(err)
	}

	// .
	// .
	// .
	// .
	// .
	swapped, release := make(chan struct{}), make(chan struct{})
	prev := redirectSwapped
	var once sync.Once
	redirectSwapped = func() { once.Do(func() { close(swapped); <-release }) }
	t.Cleanup(func() { redirectSwapped = prev })

	done := make(chan error, 1)
	go func() { done <- newAp.Redirect(oldAp) }()
	<-swapped
	pubDone := make(chan error, 1)
	go func() {
		_, err := oldAp.PublishTool(broker.PublishedTool{Name: "dyn.sneak", Summary: "s", Effects: "read.internal"})
		pubDone <- err
	}()
	// .
	time.Sleep(150 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("redirect: %v", err)
	}
	pubErr := <-pubDone

	if _, ok := reg.Get(ToolNameFor("org.example.up", "dyn.sneak")); ok {
		t.Fatal("a publish inside the swap window must not leave a live tool on the retired release")
	}
	if pubErr == nil {
		t.Fatal("and it must be told it was refused, not answered with success")
	}
	if _, ok := reg.Get("pl_org_example_up_ping"); !ok {
		t.Fatal("the successor's declared tool is still the registry's")
	}
}
