package app

import (
	"context"
	"testing"
)

// .
// .
// .
func TestAWebhookFedAdapterIsNotPolled(t *testing.T) {
	a := liveApp(t)
	calls := []string{}
	installAdapter(t, a, "org.example.poll", "matrix", "", "[]", &calls)
	// .
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
	a.plugins = append(a.plugins, activeChannel("org.example.push", send, recv, desc))
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
	a.plugins = nil
	a.convergeChannels(ctx)
}
