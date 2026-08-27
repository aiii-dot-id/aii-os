package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Carrying a message out through a channel adapter, and taking one in.
//
// The host resolves and the adapter speaks. What must never happen is the
// failure this whole surface exists to end: a message the identity
// believes it sent, quietly going nowhere.

// fakeOp is one operation of a fake channel adapter.
type fakeOp struct {
	name   string
	out    string
	fail   string
	calls  *[]string
	record bool
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
	if f.fail != "" {
		return tools.Result{Error: f.fail}, nil
	}
	return tools.Result{Output: f.out}, nil
}

// installAdapter registers a fake channel adapter serving one channel.
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

// Delivery needs a store, a registry and the activated plugins — not a
// born identity, an LLM or a ledger. Assembling only what it uses keeps
// the test measuring delivery rather than birth.
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
	a.toolReg = tools.NewRegistry(dir, nil, tools.Timeouts{})
	return a
}

// contact adds to the OPERATOR's address book — its file, in the order
// they wrote it, which is the preference order.
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
	id := queueFor(t, a, "james", "telegram", "@james", "the build is green")

	n, err := a.deliverOutbox(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("delivered %d, want 1", n)
	}
	if len(sent) != 1 || sent[0] != "@james|the build is green" {
		t.Fatalf("the adapter was called with %v", sent)
	}
	msgs, _ := a.store.UndeliveredFor("peer")
	if len(msgs) != 0 {
		t.Fatalf("a delivered message is still queued: %+v", msgs)
	}
	// delivered_via is the only durable answer to "how did that reach them?"
	var via string
	if err := a.store.DB().QueryRow(`SELECT delivered_via FROM outbox WHERE id = ?`, id).Scan(&via); err != nil {
		t.Fatal(err)
	}
	if via != "org.example.telegram" {
		t.Fatalf("delivered_via records %q, not the adapter that carried it", via)
	}
}

// No adapter for the only channel someone has is a fact about the
// operator's setup, not a reason to drop what the identity said.
func TestAMessageWithNoAdapterStaysQueued(t *testing.T) {
	a := liveApp(t)
	queueFor(t, a, "james", "signal", "+15550001111", "hello")

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

// This is what "secondary" MEANS: the primary refused and the next one
// carried it. Rank IS the operator's ordering; nothing re-sorts it.
func TestARefusedPrimaryFallsToTheSecondary(t *testing.T) {
	a := liveApp(t)
	var viaTelegram []string
	installAdapter(t, a, "org.example.email", "email", "smtp: connection refused", "", nil)
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", &viaTelegram)

	// Order IS preference: email first, telegram as the fallback.
	contact(t, a,
		Contact{Name: "james", Channel: "email", Address: "j@x.test"},
		Contact{Name: "james", Channel: "telegram", Address: "@james"})
	if err := a.store.AddOutboxMessage("msg_1", "peer", "james", "are you there?", nil); err != nil {
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

// Two adapters for one channel is ambiguous, and guessing which carries
// the mail is worse than carrying none.
func TestTwoAdaptersForOneChannelCarryNothing(t *testing.T) {
	a := liveApp(t)
	var first, second []string
	installAdapter(t, a, "org.example.tg1", "telegram", "", "", &first)
	installAdapter(t, a, "org.example.tg2", "telegram", "", "", &second)
	queueFor(t, a, "james", "telegram", "@james", "which of you?")

	n, _ := a.deliverOutbox(context.Background())
	if n != 0 {
		t.Fatalf("an ambiguous channel delivered %d", n)
	}
	if len(first) != 0 || len(second) != 0 {
		t.Fatalf("the host guessed which adapter to use: %v %v", first, second)
	}
}

// The operator's own mail is the dashboard's to deliver, not an adapter's.
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

// ── in ───────────────────────────────────────────────────────────────

// A blocking read replays updates it has not seen acknowledged. A replay
// is not a second message, and the database says so — not the adapter.
func TestAReplayedArrivalIsNotASecondMessage(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"42","from":"@james","body":"you up?"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	for i := 0; i < 3; i++ {
		if err := a.receiveFrom(context.Background(), route); err != nil {
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
	if unseen[0].Body != "you up?" || unseen[0].Address != "@james" {
		t.Fatalf("the arrival did not survive intact: %+v", unseen[0])
	}
}

// An arrival with no id cannot be de-duplicated, so it is dropped with a
// log line rather than recorded under a key that will collide.
func TestAnArrivalWithNoIdIsDroppedNotRecorded(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"","from":"@james","body":"who am i"},{"id":"7","from":"","body":"from nobody"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	if err := a.receiveFrom(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 0 {
		t.Fatalf("an unidentifiable arrival was recorded anyway: %+v", unseen)
	}
}

// A message that could not reach the identity STAYS UNSEEN. A mind that
// could not wake has not lost the mail.
func TestAnUndeliverableArrivalStaysUnseen(t *testing.T) {
	a := liveApp(t)
	inbox := `[{"id":"9","from":"@stranger","body":"click here"}]`
	installAdapter(t, a, "org.example.telegram", "telegram", "", inbox, nil)
	route := a.channelRoutes(context.Background())["telegram"]

	if err := a.receiveFrom(context.Background(), route); err != nil {
		t.Fatal(err)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 1 {
		t.Fatalf("the arrival was marked seen without reaching anyone: %+v", unseen)
	}
}

// An adapter that cannot say which channel it serves is INSTALLED AND
// CARRYING NOTHING, and must not be treated as a route.
func TestAnAdapterThatCannotNameItsChannelIsNotARoute(t *testing.T) {
	a := liveApp(t)
	installAdapter(t, a, "org.example.mute", "", "", "", nil)

	if routes := a.channelRoutes(context.Background()); len(routes) != 0 {
		t.Fatalf("an adapter that named no channel became a route: %+v", routes)
	}
}

// The identity must never be handed the raw pipe beside the governed
// one. send resolves through the address book; receive's output must
// pass internal/untrusted before it reaches a prompt. A channel
// adapter's three methods are the host's plumbing, and the model's
// function list is where that either holds or does not.
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
	// And the host still drives them: hidden must not mean broken.
	if routes := a.channelRoutes(context.Background()); len(routes) != 1 {
		t.Fatalf("hiding the methods broke the host's own use of them: %+v", routes)
	}
}
