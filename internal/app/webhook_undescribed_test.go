package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAChannelAdaptersWebhookWithNoDescribedChannelIsAnswered503(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.sms"
	given := &[]string{}
	decl := pluginhost.WebhookDecl{Path: "notify", Operation: "sms.notify",
		Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-T", SecretSetting: "tok"}}
	op := &fakeOp{name: pluginhost.ToolNameFor(id, decl.Operation), allArgs: given, out: arrivals("s1")}
	if err := a.toolReg.RegisterDynamic(op, id); err != nil {
		t.Fatal(err)
	}
	// The adapter's describe fails this round.
	describe := &fakeOp{name: "pl_" + id + "_describe", fail: "not ready"}
	if err := a.toolReg.RegisterHostOp(describe, id); err != nil {
		t.Fatal(err)
	}
	addPlugin(a, &pluginhost.ActivePlugin{ID: id, Webhooks: []pluginhost.WebhookDecl{decl},
		Channel: &pluginhost.Channel{PluginID: id, Describe: describe.name, Send: "pl_" + id + "_send", Receive: "pl_" + id + "_receive"}})
	grantSecret(t, a, id, "tok", "push", "AII_TEST_PUSH_TOKEN", "sekret")

	if rec := notify(a, id); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a verified webhook of an adapter with no described channel was answered %d, want 503 so the sender delivers it again", rec.Code)
	}
	if len(*given) != 0 {
		t.Fatalf("the operation ran %d time(s) for a channel the host cannot carry yet", len(*given))
	}
	if rows, _ := a.store.InboundSince(0); len(rows) != 0 {
		t.Fatalf("an adapter's arrival was taken in with no channel to land on: %+v", rows)
	}

	// The control half: the sweep's next round describes the channel, and the
	// sender's redelivery lands on it, once.
	describe.fail, describe.out = "", `{"channel":"sms","receive":"webhook"}`
	a.routesFor(context.Background(), true)
	for range 2 {
		if rec := notify(a, id); rec.Code != http.StatusOK {
			t.Fatalf("once described, the webhook was answered %d", rec.Code)
		}
	}
	rows, _ := a.store.InboundSince(0)
	if len(rows) != 1 || rows[0].Channel != "sms" {
		t.Fatalf("the redelivery landed on the adapter's channel once: %+v", rows)
	}
}

// A scheduled call of such an adapter is not made: its answer would have no
// channel to land on, and an adapter not told what was recorded moves on past
// what it answered. A plugin that is no adapter is called as before, its
// arrivals on its event route.
func TestAScheduledCallOfAChannelAdapterWithNoDescribedChannelIsNotMade(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.mail"
	// describe answers without naming a channel: not described this round.
	op, d := scheduled(t, a, id, `{"receive":"webhook"}`)
	op.set(tools.Result{Output: arrivals("m1")}, nil)
	a.scheduledCall(context.Background(), id, d)
	if calls := op.calls(); len(calls) != 0 {
		t.Fatalf("the scheduled call was made %d time(s) for an adapter with no described channel", len(calls))
	}
	if rows, _ := a.store.InboundSince(0); len(rows) != 0 {
		t.Fatalf("an adapter's arrival was taken in with no channel to land on: %+v", rows)
	}

	const plain = "org.example.plain"
	pop, pd := scheduled(t, a, plain, "")
	pop.set(tools.Result{Output: arrivals("p1")}, nil)
	a.scheduledCall(context.Background(), plain, pd)
	rows, _ := a.store.InboundSince(0)
	if len(pop.calls()) != 1 || len(rows) != 1 || rows[0].Channel != "hook:"+plain {
		t.Fatalf("a plugin that is no adapter is called and its arrival lands on its event route: calls %d, rows %+v", len(pop.calls()), rows)
	}
}

func TestAnAdapterDescribedOnceKeepsCarryingItsChannelWhileALaterDescribeFails(t *testing.T) {
	a := liveApp(t)
	const id = "com.example.sms"
	given := &[]string{}
	decl := pluginhost.WebhookDecl{Path: "notify", Operation: "sms.notify",
		Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-T", SecretSetting: "tok"}}
	op := &fakeOp{name: pluginhost.ToolNameFor(id, decl.Operation), allArgs: given, out: arrivals("s1")}
	if err := a.toolReg.RegisterDynamic(op, id); err != nil {
		t.Fatal(err)
	}
	describe := &fakeOp{name: "pl_" + id + "_describe", out: `{"channel":"sms","receive":"webhook"}`}
	if err := a.toolReg.RegisterHostOp(describe, id); err != nil {
		t.Fatal(err)
	}
	channel := func() *pluginhost.ActivePlugin {
		return &pluginhost.ActivePlugin{ID: id, Webhooks: []pluginhost.WebhookDecl{decl},
			Channel: &pluginhost.Channel{PluginID: id, Describe: describe.name, Send: "pl_" + id + "_send", Receive: "pl_" + id + "_receive"}}
	}
	addPlugin(a, channel())
	grantSecret(t, a, id, "tok", "push", "AII_TEST_PUSH_TOKEN", "sekret")
	if rec := notify(a, id); rec.Code != http.StatusOK {
		t.Fatalf("a described adapter's webhook was answered %d", rec.Code)
	}

	// The set changes (another adapter is activated), so describe is asked
	// again, and this adapter's describe fails this round.
	describe.fail, describe.out = "not ready", ""
	const other = "org.example.chat"
	otherDescribe := &fakeOp{name: "pl_" + other + "_describe", out: `{"channel":"chat"}`}
	if err := a.toolReg.RegisterHostOp(otherDescribe, other); err != nil {
		t.Fatal(err)
	}
	addPlugin(a, activeChannel(other, "pl_"+other+"_send", "pl_"+other+"_receive", otherDescribe.name))
	// Two rounds in a row fail (the sweep asks again after the first): the
	// last good description is carried from one round to the next.
	for range 2 {
		if _, complete := a.routesFor(context.Background(), true); complete {
			t.Fatal("the round answered complete with a describe that failed, so this proves nothing")
		}
	}
	*op = fakeOp{name: op.name, allArgs: given, out: arrivals("s2")}
	if rec := notify(a, id); rec.Code != http.StatusOK {
		t.Fatalf("AN ADAPTER DESCRIBED ONCE WAS ANSWERED %d WHEN ITS DESCRIBE FAILED LATER: a sender that does not deliver again loses the message", rec.Code)
	}
	rows, _ := a.store.InboundSince(0)
	if len(rows) != 2 {
		t.Fatalf("both arrivals were taken in: %+v", rows)
	}
	for _, r := range rows {
		if r.Channel != "sms" {
			t.Fatalf("an arrival while describe failed landed on %q, want the adapter's own channel sms (a person's message, not a plugin's event): %+v", r.Channel, rows)
		}
	}

	// An update is a new activation, never described: 503.
	removePlugin(a, id)
	addPlugin(a, channel())
	a.routesFor(context.Background(), true)
	*op = fakeOp{name: op.name, allArgs: given, out: arrivals("s3")}
	before := len(*given)
	if rec := notify(a, id); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an update never described was answered %d, want 503", rec.Code)
	}
	if len(*given) != before {
		t.Fatal("the operation ran for an update never described")
	}
}
