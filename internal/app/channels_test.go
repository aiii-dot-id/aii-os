package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .

// .
type fakeOp struct {
	name   string
	out    string
	fail   string
	calls  *[]string
	record bool
	// .
	// .
	// .
	allArgs *[]string
}

func (f *fakeOp) Name() string        { return f.name }
func (f *fakeOp) Description() string { return "fake channel operation" }
func (f *fakeOp) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (f *fakeOp) Execute(_ context.Context, args map[string]interface{}) (tools.Result, error) {
	if f.record && f.calls != nil {
		addr, _ := args["address"].(string)
		body, _ := args["body"].(string)
		*f.calls = append(*f.calls, addr+"|"+body)
	}
	if f.allArgs != nil {
		raw, _ := json.Marshal(args)
		*f.allArgs = append(*f.allArgs, string(raw))
	}
	if f.fail != "" {
		return tools.Result{Error: f.fail}, nil
	}
	return tools.Result{Output: f.out}, nil
}

// .
func installAdapter(t *testing.T, a *App, pluginID, channel, sendFails, inbox string, calls *[]string) {
	t.Helper()
	desc, send, recv := "pl_"+pluginID+"_describe", "pl_"+pluginID+"_send", "pl_"+pluginID+"_receive"
	if inbox == "" {
		inbox = "[]"
	}
	for _, op := range []*fakeOp{
		{name: desc, out: fmt.Sprintf(`{"channel":%q}`, channel)},
		{name: send, fail: sendFails, out: `{"receipt":"ok"}`, calls: calls, record: true},
		{name: recv, out: inbox},
	} {
		if err := a.toolReg.RegisterHostOp(op, pluginID); err != nil {
			t.Fatal(err)
		}
	}
	a.plugins = append(a.plugins, &pluginhost.ActivePlugin{
		ID:      pluginID,
		Channel: &pluginhost.Channel{PluginID: pluginID, Send: send, Receive: recv, Describe: desc},
	})
}

// .
// .
// .
func activeChannel(pluginID, send, recv, desc string) *pluginhost.ActivePlugin {
	return &pluginhost.ActivePlugin{
		ID:      pluginID,
		Channel: &pluginhost.Channel{PluginID: pluginID, Send: send, Receive: recv, Describe: desc},
	}
}

func liveApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	st.SetWorkObserver(a.workObserved)
	a.toolReg = tools.NewRegistry(dir, nil, tools.Timeouts{})
	return a
}

// .
// .
func contact(t *testing.T, a *App, cs ...Contact) {
	t.Helper()
	a.cfgMu.Lock()
	a.cfg.Contacts = append(a.cfg.Contacts, cs...)
	a.cfgMu.Unlock()
}

func queueFor(t *testing.T, a *App, label, channel, address, body string) string {
	t.Helper()
	contact(t, a, Contact{Name: label, Channel: channel, Address: address})
	id := "msg_" + label + "_1"
	if err := a.store.AddOutboxMessage(id, "peer", label, body, nil); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAQueuedMessageLeavesThroughItsAdapter(t *testing.T) {
	a := liveApp(t)
	var sent []string
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", &sent)
	id := queueFor(t, a, "sam", "telegram", "@sam", "the build is green")

	n, err := a.deliverOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("delivered %d, want 1", n)
	}
	if len(sent) != 1 || sent[0] != "@sam|the build is green" {
		t.Fatalf("the adapter was called with %v", sent)
	}
	msgs, _ := a.store.UndeliveredFor("peer")
	if len(msgs) != 0 {
		t.Fatalf("a delivered message is still queued: %+v", msgs)
	}
	// .
	var via string
	if err := a.store.DB().QueryRow(`SELECT delivered_via FROM outbox WHERE id = ?`, id).Scan(&via); err != nil {
		t.Fatal(err)
	}
	if via != "org.example.telegram" {
		t.Fatalf("delivered_via records %q, not the adapter that carried it", via)
	}
}

// .
// .
func TestAMessageWithNoAdapterStaysQueued(t *testing.T) {
	a := liveApp(t)
	queueFor(t, a, "sam", "signal", "+15550001111", "hello")

	n, err := a.deliverOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("delivered %d with no adapter installed", n)
	}
	msgs, _ := a.store.UndeliveredFor("peer")
	if len(msgs) != 1 {
		t.Fatalf("the message was dropped rather than left queued: %+v", msgs)
	}
}

// .
// .
func TestARefusedPrimaryFallsToTheSecondary(t *testing.T) {
	a := liveApp(t)
	var viaTelegram []string
	installAdapter(t, a, "org.example.email", "email", "smtp: connection refused", "", nil)
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", &viaTelegram)

	// .
	contact(t, a,
		Contact{Name: "sam", Channel: "email", Address: "j@x.test"},
		Contact{Name: "sam", Channel: "telegram", Address: "@sam"})
	if err := a.store.AddOutboxMessage("msg_1", "peer", "sam", "are you there?", nil); err != nil {
		t.Fatal(err)
	}

	n, err := a.deliverOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("delivered %d — the secondary did not carry what the primary refused", n)
	}
	if len(viaTelegram) != 1 {
		t.Fatalf("the secondary adapter was not called: %v", viaTelegram)
	}
}

// .
// .
func TestTwoAdaptersForOneChannelCarryNothing(t *testing.T) {
	a := liveApp(t)
	var first, second []string
	installAdapter(t, a, "org.example.tg1", "telegram", "", "", &first)
	installAdapter(t, a, "org.example.tg2", "telegram", "", "", &second)
	queueFor(t, a, "sam", "telegram", "@sam", "which of you?")

	n, _ := a.deliverOutbox(context.Background())
	if n != 0 {
		t.Fatalf("an ambiguous channel delivered %d", n)
	}
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("the host guessed which adapter to use: %v %v", first, second)
	}
}

// .
func TestOperatorMailIsNotAnAdaptersToCarry(t *testing.T) {
	a := liveApp(t)
	var sent []string
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", &sent)
	if err := a.store.AddOutboxMessage("msg_op", "operator", "", "yours", nil); err != nil {
		t.Fatal(err)
	}

	n, _ := a.deliverOutbox(context.Background())
	if n != 0 || len(sent) != 0 {
		t.Fatalf("operator mail went out through a channel adapter: n=%d sent=%v", n, sent)
	}
}

// .

// .
// .
func TestAReplayedArrivalIsNotASecondMessage(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"42","from":"@sam","body":"you up?"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	for i := 0; i < 3; i++ {
		if _, err := a.receiveFrom(context.Background(), route); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	unseen, err := a.store.InboundSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unseen) != 1 {
		t.Fatalf("three reads of the same update recorded %d messages", len(unseen))
	}
	if unseen[0].Body != "you up?" || unseen[0].Address != "@sam" {
		t.Fatalf("the arrival did not survive intact: %+v", unseen[0])
	}
}

// .
// .
func TestAnArrivalWithNoIdIsDroppedNotRecorded(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"","from":"@sam","body":"who am i"},{"id":"7","from":"","body":"from nobody"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	if _, err := a.receiveFrom(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 0 {
		t.Fatalf("an unidentifiable arrival was recorded anyway: %+v", unseen)
	}
}

// .
// .
func TestAnUndeliverableArrivalStaysUnseen(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"9","from":"@stranger","body":"click here"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	if _, err := a.receiveFrom(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 1 {
		t.Fatalf("the arrival was marked seen without reaching anyone: %+v", unseen)
	}
}

// .
// .
func TestAnAdapterThatCannotNameItsChannelIsNotARoute(t *testing.T) {
	a := liveApp(t)
	installAdapter(t, a, "org.example.mute", "", "", "", nil)

	if routes := a.channelRoutes(context.Background()); len(routes) != 0 {
		t.Fatalf("an adapter that named no channel became a route: %+v", routes)
	}
}

// .
// .
// .
// .
// .
func TestTheIdentityCannotSeeAChannelAdaptersMethods(t *testing.T) {
	a := liveApp(t)
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", nil)

	for _, d := range a.buildToolDefinitions() {
		switch d.Function.Name {
		case "pl_org.example.telegram_send", "pl_org.example.telegram_receive", "pl_org.example.telegram_describe":
			t.Fatalf("the identity was offered %q — it could address a stranger directly, "+
				"or pull foreign text into its own context unwrapped", d.Function.Name)
		}
	}
	// .
	if routes := a.channelRoutes(context.Background()); len(routes) != 1 {
		t.Fatalf("hiding the methods broke the host's own use of them: %+v", routes)
	}
}
