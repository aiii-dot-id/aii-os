package app

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// An adapter that receives by webhook is never polled (S9): its route
// exists for sending and for its arrivals, and no receive loop is
// started for it, while a polling adapter beside it is listened to.
func TestAWebhookFedAdapterIsNotPolled(t *testing.T) {
	a := liveApp(t)
	calls := []string{}
	installAdapter(t, a, "org.example.poll", "matrix", "", "[]", &calls)
	// A push adapter: describe says so.
	desc, send, recv := "pl_org_example_push_describe", "pl_org_example_push_send", "pl_org_example_push_receive"
	for _, op := range []*fakeOp{
		{name: desc, out: `{"channel":"sms","receive":"webhook"}`},
		{name: send, out: `{"receipt":"ok"}`},
		{name: recv, fail: "receive is not how this adapter hears; its webhooks are"},
	} {
		if err := a.toolReg.RegisterHostOp(op, "org.example.push"); err != nil {
			t.Fatal(err)
		}
	}
	addPlugin(a, activeChannel("org.example.push", send, recv, desc))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	routes := a.channelRoutes(ctx)
	if !routes["sms"].Push || routes["matrix"].Push {
		t.Fatalf("describe names how each adapter receives: %+v", routes)
	}
	a.convergeChannels(ctx)
	if _, listening := a.listening["org.example.push"]; listening {
		t.Fatal("a webhook-fed adapter got a receive loop")
	}
	if _, listening := a.listening["org.example.poll"]; !listening {
		t.Fatal("the polling adapter beside it is listened to")
	}
	setPlugins(a, nil)
	a.convergeChannels(ctx) // stops what was listening, the adapter's way
}

func TestAnAdapterReplacedUnderItsIDIsRoutedByItsNewDescription(t *testing.T) {
	a := liveApp(t)
	shortStopGrace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, email := installBlockingAdapter(t, a, "org.example.email", "email", 1)

	const id = "org.example.chat"
	desc, send, recv := "pl_"+id+"_describe", "pl_"+id+"_send", "pl_"+id+"_receive"
	for _, op := range []*fakeOp{
		{name: desc, out: `{"channel":"chat","receive":"webhook"}`},
		{name: send, out: `{"receipt":"ok"}`},
		{name: recv, fail: "receive is not how this release hears; its webhooks are"},
	} {
		if err := a.toolReg.RegisterHostOp(op, id); err != nil {
			t.Fatal(err)
		}
	}
	old := &pluginhost.Activation{ID: id, Version: "1.0.0", Plugin: activeChannel(id, send, recv, desc)}
	a.adoptPlugin(old, nil)
	a.convergeChannels(ctx)
	<-email.entered
	if r := a.channelRoutes(ctx)["chat"]; !r.Push || r.Acknowledges {
		t.Fatalf("the first release hears by webhook: %+v", r)
	}
	if _, listening := a.listening[id]; listening {
		t.Fatal("the webhook-fed release got a receive loop")
	}
	beside := a.listening["org.example.email"]

	polled := &answersOp{name: recv}
	if err := a.toolReg.SupersedeOrigin(id, []tools.Tool{
		&fakeOp{name: desc, out: `{"channel":"chat","acknowledges":true,"budget_seconds":1}`},
		&fakeOp{name: send, out: `{"receipt":"ok"}`},
		polled,
	}, map[string]bool{desc: true, send: true, recv: true}); err != nil {
		t.Fatal(err)
	}
	a.adoptPlugin(&pluginhost.Activation{ID: id, Version: "1.1.0", Plugin: activeChannel(id, send, recv, desc)}, old.Plugin)
	a.convergeChannels(ctx) // the sweep the commit woke

	if r := a.channelRoutes(ctx)["chat"]; r.Push || !r.Acknowledges || r.Budget != time.Second {
		t.Fatalf("the host kept the replaced activation's description: %+v", r)
	}
	if _, listening := a.listening[id]; !listening {
		t.Fatal("the release that polls now has no receive loop")
	}
	waitUntil(t, func() bool { return len(polled.told()) > 0 }, "the new release's first receive")
	if got := polled.told()[0]; got != `{"recorded":[]}` {
		t.Fatalf("the release that acknowledges was asked %s, want the host's recorded list", got)
	}
	if a.listening["org.example.email"] != beside || email.cancelled.Load() != 0 || email.returned.Load() != 0 {
		t.Fatal("the unrelated adapter's listener was disturbed by a change that was not its own")
	}
	setPlugins(a, nil)
	a.convergeChannels(ctx)
	cancel()
	a.bgWG.Wait()
}
