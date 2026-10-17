package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// scriptedSend is a channel send whose answer the test writes.
type scriptedSend struct {
	name string
	run  func(context.Context) (tools.Result, error)
}

func (s *scriptedSend) Name() string        { return s.name }
func (s *scriptedSend) Description() string { return "a channel send the test answers" }
func (s *scriptedSend) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *scriptedSend) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	return s.run(ctx)
}

// installScriptedAdapter registers a channel adapter whose send runs run.
func installScriptedAdapter(t *testing.T, a *App, pluginID, channel string, run func(context.Context) (tools.Result, error)) {
	t.Helper()
	send := &scriptedSend{name: "pl_" + pluginID + "_send", run: run}
	desc := &fakeOp{name: "pl_" + pluginID + "_describe", out: fmt.Sprintf(`{"channel":%q}`, channel)}
	recv := &fakeOp{name: "pl_" + pluginID + "_receive", out: "[]"}
	for _, op := range []tools.Tool{send, desc, recv} {
		if err := a.toolReg.RegisterHostOp(op, pluginID); err != nil {
			t.Fatal(err)
		}
	}
	a.pluginMu.Lock() // a booted identity's outbox reads the set meanwhile
	a.plugins = append(a.plugins, activeChannel(pluginID, send.name, recv.name, desc.name))
	a.pluginMu.Unlock()
}

// claimRow claims a queued row as a walk would, or fails the test.
func claimRow(t *testing.T, a *App, id string) store.Dispatch {
	t.Helper()
	d, err := a.store.ClaimDispatch(id)
	if err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return d
}

// queueToFriend puts one message to a person with a primary and a
// secondary channel.
func queueToFriend(t *testing.T, a *App) {
	t.Helper()
	contact(t, a,
		Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"},
		Contact{Name: "friend", Channel: "secondary", Address: "@friend"})
	if err := a.store.AddOutboxMessage("msg_c2", "peer", "friend", "the build is green", nil); err != nil {
		t.Fatal(err)
	}
}

// pokeTwice is two turn ends' worth of delivery.
func pokeTwice(t *testing.T, a *App) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, err := a.deliverOutbox(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

// deliveryRow reads a row whatever its state.
func deliveryRow(t *testing.T, a *App, id string) store.OutboxMessage {
	t.Helper()
	var m store.OutboxMessage
	var via *string
	var parked int
	if err := a.store.DB().QueryRow(`SELECT delivered, delivered_via, attempts, parked, effect, last_error FROM outbox WHERE id = ?`, id).
		Scan(&m.Delivered, &via, &m.Attempts, &parked, &m.Effect, &m.LastError); err != nil {
		t.Fatal(err)
	}
	if via != nil {
		m.DeliveredVia = *via
	}
	m.Parked = parked != 0
	return m
}

// AN ANSWER THAT MAY HAVE LEFT PARKS, SAID, AND NO SECONDARY IS ASKED.
// Before the claim only an explicit NET_EFFECT_UNKNOWN stopped the walk;
// a passed-through 5xx, an oversized response, an untyped failure and an
// invocation lost after the effect each went on to the secondary — two
// messages where the person should have had one. A POST whose answer
// named a redirect off the grant was performed; the broker says so with
// NET_REMOTE_OUTCOME_FAILED (TestNoEffectNamesNoCodeAWrittenRequestAnswers).
func TestAnAnswerThatMayHaveLeftParksWithoutTheSecondary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer tools.Result
		err    error
	}{
		{"the adapter says the effect is unknown", tools.Result{Error: "the send was written and the response was lost", ReasonCode: broker.ReasonNetEffectUnknown}, nil},
		{"a two-part send whose second part was lost", tools.Result{Error: "first part accepted; second part's response lost", ReasonCode: broker.ReasonNetEffectUnknown}, nil},
		{"the broker's 5xx passed through", tools.Result{Error: "operation failed: 503 (NET_REMOTE_OUTCOME_FAILED)", ReasonCode: "NET_REMOTE_OUTCOME_FAILED"}, nil},
		{"the broker's oversized response passed through", tools.Result{Error: "operation failed (NET_RESPONSE_TOO_LARGE)", ReasonCode: "NET_RESPONSE_TOO_LARGE"}, nil},
		{"an answer whose redirect left the grant", tools.Result{Error: "operation failed: redirect refused (NET_REMOTE_OUTCOME_FAILED)", ReasonCode: "NET_REMOTE_OUTCOME_FAILED"}, nil},
		{"an untyped failure", tools.Result{Error: "something went wrong"}, nil},
		{"a result the host refused", tools.Result{Error: "result refused: result outside the declared output schema"}, nil},
		{"a result the host refused, as it says it now", tools.Result{Error: "result refused: result outside the declared output schema; the operation ran, and whether it had its effect on the service is not known — check before asking again", ReasonCode: pluginhost.ReasonResultRefused}, nil},
		{"an invocation lost after the effect", tools.Result{}, errors.New("plugin worker exited during the call")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := liveApp(t)
			queueToFriend(t, a)
			var primary, secondary atomic.Int32
			installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
				primary.Add(1)
				return tc.answer, tc.err
			})
			installScriptedAdapter(t, a, "org.example.secondary", "secondary", func(context.Context) (tools.Result, error) {
				secondary.Add(1)
				return tools.Result{Output: `{"receipt":"ok"}`}, nil
			})
			pokeTwice(t, a)
			row := deliveryRow(t, a, "msg_c2")
			if primary.Load() != 1 || secondary.Load() != 0 {
				t.Fatalf("primary asked %d, secondary %d — want 1 and 0: an uncertain send is never repeated or handed on", primary.Load(), secondary.Load())
			}
			if row.Delivered != 0 || !row.Parked || row.Effect != "unknown" || row.DeliveredVia != "org.example.primary" || row.Attempts != 1 {
				t.Fatalf("want parked, effect unknown via the primary, one attempt: %+v", row)
			}
			text := deliveryOutcomeLines([]store.OutboxMessage{{ToIdentity: "friend", DeliveredVia: row.DeliveredVia, Effect: row.Effect, Parked: true, Attempts: 1, LastError: row.LastError}})
			if !strings.Contains(text, "cannot tell whether it arrived") || !strings.Contains(text, "not resent") {
				t.Fatalf("the identity is not told it may have arrived:\n%s", text)
			}
		})
	}
}

func TestAnAnswerThatNothingLeftWalksToTheSecondary(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"the service accepted nothing", "NET_REMOTE_FAILED"},
		{"the address cannot take a message", broker.ReasonArgumentInvalid},
		{"outside the signed envelope, before dispatch", "CAPABILITY_NOT_IN_STATIC_ENVELOPE"},
		{"an auth profile not admitted, before dispatch", "NET_AUTH_PROFILE_NOT_ADMITTED"},
		{"a host the operator never granted, before dispatch", "POLICY_DENY"},
		{"a 401 whose profile the authority disconnected", "NET_AUTH_PROFILE_DISCONNECTED"},
		{"a 401 whose renewal failed just now", "NET_AUTH_PROFILE_UNAVAILABLE"},
		{"a 401 renewed with an unusable token", "NET_HEADER_INVALID"},
		{"the operator's grant refuses", pluginhost.ReasonOperatorGrant},
		{"the activation was withdrawn", pluginhost.ReasonActivationWithdrawn},
		{"SAFE suspended the call", tools.ReasonSafeSuspended},
		{"the operator disabled the operation", tools.ReasonOperatorDisabled},
		{"the target changed before dispatch", tools.ReasonDispatchTargetChanged},
		{"the release is not yet admitted", pluginhost.ReasonNotAdmitted},
		{"cancelled while waiting for the adapter's turn", pluginhost.ReasonCancelledBeforeDispatch},
		{"the send's declared input refused the arguments", broker.ReasonArgumentInvalid},
		{"the send's required arguments are not the host's", tools.ReasonArgumentsRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := liveApp(t)
			queueToFriend(t, a)
			primary := installReasonedAdapter(t, a, "org.example.primary", "primary", "refused: nothing ran", tc.reason)
			secondary := installReasonedAdapter(t, a, "org.example.secondary", "secondary", "", "")
			pokeTwice(t, a)
			row := deliveryRow(t, a, "msg_c2")
			if len(primary.calls) != 1 || len(secondary.calls) != 1 {
				t.Fatalf("primary asked %d, secondary %d — want one each", len(primary.calls), len(secondary.calls))
			}
			if row.Delivered != 1 || row.Effect != "performed" || row.DeliveredVia != "org.example.secondary" {
				t.Fatalf("the secondary did not carry what the primary never sent: %+v", row)
			}
		})
	}
}

// THE REGISTRY'S OWN REFUSALS BEFORE DISPATCH WALK ON. A send whose
// declared required arguments the host never passes, and a route whose
// send the registry no longer holds (an adapter going away mid-walk), are
// refused before anything runs, typed: the secondary carries the message.
func TestARegistryRefusalBeforeDispatchWalksToTheSecondary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		install func(t *testing.T, a *App)
	}{
		{"the send requires an argument the host never passes", func(t *testing.T, a *App) {
			send := &requiringSend{name: "pl_org.example.primary_send"}
			desc := &fakeOp{name: "pl_org.example.primary_describe", out: `{"channel":"primary"}`}
			recv := &fakeOp{name: "pl_org.example.primary_receive", out: "[]"}
			for _, op := range []tools.Tool{send, desc, recv} {
				if err := a.toolReg.RegisterHostOp(op, "org.example.primary"); err != nil {
					t.Fatal(err)
				}
			}
			addPlugin(a, activeChannel("org.example.primary", send.name, recv.name, desc.name))
		}},
		{"the send is no longer registered", func(t *testing.T, a *App) {
			desc := &fakeOp{name: "pl_org.example.primary_describe", out: `{"channel":"primary"}`}
			recv := &fakeOp{name: "pl_org.example.primary_receive", out: "[]"}
			for _, op := range []tools.Tool{desc, recv} {
				if err := a.toolReg.RegisterHostOp(op, "org.example.primary"); err != nil {
					t.Fatal(err)
				}
			}
			addPlugin(a, activeChannel("org.example.primary", "pl_org.example.primary_send", recv.name, desc.name))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := liveApp(t)
			queueToFriend(t, a)
			tc.install(t, a)
			secondary := installReasonedAdapter(t, a, "org.example.secondary", "secondary", "", "")
			if _, err := a.deliverOutbox(t.Context()); err != nil {
				t.Fatal(err)
			}
			if row := deliveryRow(t, a, "msg_c2"); len(secondary.calls) != 1 || row.Delivered != 1 || row.DeliveredVia != "org.example.secondary" {
				t.Fatalf("the secondary did not carry what the registry refused before dispatch: asked %d, %+v", len(secondary.calls), row)
			}
		})
	}
}

// requiringSend declares a required argument no channel walk passes.
type requiringSend struct{ name string }

func (r *requiringSend) Name() string        { return r.name }
func (r *requiringSend) Description() string { return "a send that requires a subject" }
func (r *requiringSend) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"address", "body", "subject"}}
}
func (r *requiringSend) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Output: `{"receipt":"should not have run"}`}, nil
}

// THE OPERATOR'S GRANT REFUSING IS PERMANENT. Before, only an invalid
// address parked a row after its walk; a read-only grant was asked again
// at every turn end until the attempt ceiling.
func TestAGrantRefusalParksAfterTheWalk(t *testing.T) {
	a := liveApp(t)
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	if err := a.store.AddOutboxMessage("msg_c2", "peer", "friend", "hello", nil); err != nil {
		t.Fatal(err)
	}
	send := installReasonedAdapter(t, a, "org.example.primary", "primary", "the operator granted this plugin read only; nothing ran", pluginhost.ReasonOperatorGrant)
	pokeTwice(t, a)
	if row := deliveryRow(t, a, "msg_c2"); len(send.calls) != 1 || !row.Parked || row.Attempts != 1 || row.Effect != "" {
		t.Fatalf("asked %d times, want once and parked with no effect: %+v", len(send.calls), row)
	}
}

// A CLAIM THAT CANNOT BE WRITTEN SENDS NOTHING. Every write to the
// outbox is refused before the walk; the adapter must not be asked.
func TestAFailedClaimWriteSendsNothing(t *testing.T) {
	a := liveApp(t)
	queueToFriend(t, a)
	var effects atomic.Int32
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		effects.Add(1)
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	if _, err := a.store.DB().Exec(`CREATE TRIGGER no_outbox_writes BEFORE UPDATE ON outbox BEGIN SELECT RAISE(ABORT, 'the store refuses this write'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if effects.Load() != 0 {
		t.Fatalf("the adapter was asked %d time(s) with no claim on the row", effects.Load())
	}
}

// AN OUTCOME THAT CANNOT BE WRITTEN LEAVES THE ROW IN FLIGHT, NEVER
// RESENT. The adapter took the message; the store then refused the
// outcome. Before, the next poke found the row still queued and sent it
// again.
func TestAFailedOutcomeWriteIsNeverSentAgain(t *testing.T) {
	a := liveApp(t)
	queueToFriend(t, a)
	var effects atomic.Int32
	var injected error
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		if effects.Add(1) == 1 {
			_, injected = a.store.DB().Exec(`CREATE TRIGGER no_outcome BEFORE UPDATE ON outbox BEGIN SELECT RAISE(ABORT, 'the store refuses this write'); END`)
		}
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	if _, err := a.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if injected != nil {
		t.Fatal(injected)
	}
	if _, err := a.store.DB().Exec(`DROP TRIGGER no_outcome`); err != nil {
		t.Fatal(err)
	}
	pokeTwice(t, a)
	if effects.Load() != 1 {
		t.Fatalf("the same row was sent %d times after its outcome write failed", effects.Load())
	}
	if pending, err := a.store.PendingPeerDeliveries(); err != nil || len(pending) != 0 {
		t.Fatalf("a row in flight is not the outbox's to carry: %d %v", len(pending), err)
	}
}

// TWO DELIVERY POKES AT ONCE SEND ONCE. The first is held inside the
// adapter. A second poke, and a walk that read the row before the first
// claimed it (two pokes whose reads both came first), must not reach it.
func TestTwoDeliveryPokesAtOnceSendOnce(t *testing.T) {
	a := liveApp(t)
	queueToFriend(t, a)
	var effects atomic.Int32
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(ctx context.Context) (tools.Result, error) {
		effects.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return tools.Result{Output: `{"receipt":"ok"}`}, nil
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
	})
	stale, err := a.store.PendingPeerDeliveries()
	if err != nil || len(stale) != 1 {
		t.Fatalf("fixture: %d pending (%v)", len(stale), err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first, second, early := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	go func() { _, err := a.deliverOutbox(ctx); first <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("the first poke never reached the adapter")
	}
	go func() { _, err := a.deliverOutbox(ctx); second <- err }()
	go func() { early <- a.deliverOne(ctx, stale[0], a.channelRoutes(ctx)) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-second: // finds the row in flight and carries nothing
			if err != nil {
				t.Fatal(err)
			}
		case err := <-early:
			if !errors.Is(err, store.ErrClaimRefused) {
				t.Fatalf("a walk that read the row before the claim was not refused: %v", err)
			}
		case <-entered:
			t.Fatal("a second walk reached the adapter while the first held the row")
		case <-ctx.Done():
			t.Fatal("a second walk neither returned nor reached the adapter")
		}
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if effects.Load() != 1 {
		t.Fatalf("one row was sent %d times", effects.Load())
	}
	if row := deliveryRow(t, a, "msg_c2"); row.Delivered != 1 || row.Attempts != 1 {
		t.Fatalf("delivered once, one attempt: %+v", row)
	}
}

// A STOPPING OUTBOX CLAIMS NOTHING MORE. Its context is done: a send
// begun now would be refused before dispatch in words the host cannot
// classify, and parked as unknown for nothing. The rows stay queued and
// unclaimed for the next boot.
func TestAStoppingOutboxClaimsNothingMore(t *testing.T) {
	a := liveApp(t)
	queueToFriend(t, a)
	var asked atomic.Int32
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		asked.Add(1)
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.deliverOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, err := a.store.PendingPeerDeliveries(); err != nil || len(pending) != 1 || asked.Load() != 0 {
		t.Fatalf("a stopped outbox asked %d time(s); pending %d (%v), want the row queued and unclaimed", asked.Load(), len(pending), err)
	}
}

func TestALateResultDoesNotOverwriteASettlement(t *testing.T) {
	a := liveApp(t)
	queueToFriend(t, a)
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		a.settleInterruptedSends()
		return tools.Result{Output: `{"receipt":"late"}`}, nil
	})
	if _, err := a.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := deliveryRow(t, a, "msg_c2"); row.Delivered != 0 || !row.Parked || row.Effect != "unknown" || row.Attempts != 1 {
		t.Fatalf("the late result overwrote the settled row: %+v", row)
	}
}

func TestACrashBetweenSendAndOutcomeParksUnknownAtBoot(t *testing.T) {
	if dir := os.Getenv("AII_C2_CRASH_CHILD_DIR"); dir != "" {
		crashChild(t, dir)
		return
	}
	dir := t.TempDir()
	a, _ := promptCacheApp(t)
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	cfg := a.configSnapshot()
	stopApp(t, a)
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, exe, "-test.run=^TestACrashBetweenSendAndOutcomeParksUnknownAtBoot$", "-test.timeout=50s")
	child.Env = append(os.Environ(), "AII_C2_CRASH_CHILD_DIR="+dir)
	out, err := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 91 {
		t.Fatalf("the child did not die inside the send: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "WARNING: DATA RACE") {
		t.Fatalf("the deliberate child exit hid a race report:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "effect")); err != nil {
		t.Fatalf("the child died before its effect: %v", err)
	}

	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, next) })
	row := deliveryRow(t, next, "msg_c2")
	if row.Delivered != 0 || !row.Parked || row.Effect != "unknown" || row.Attempts != 1 || row.DeliveredVia != "" {
		t.Fatalf("the boot did not settle the interrupted send as unknown: %+v", row)
	}
	events, err := next.store.PendingTurnEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	told := false
	for _, e := range events {
		if e.Outcome != nil && e.Outcome.ID == "msg_c2" && e.Outcome.Attempts == 1 {
			told = strings.Contains(deliveryOutcomeLines([]store.OutboxMessage{*e.Outcome}), "the process stopped while it was being sent")
		}
	}
	if !told {
		t.Fatalf("the identity's next turn is not told the send was interrupted: %+v", events)
	}
	var again atomic.Int32
	installScriptedAdapter(t, next, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		again.Add(1)
		return tools.Result{Output: `{"receipt":"again"}`}, nil
	})
	if _, err := next.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again.Load() != 0 {
		t.Fatalf("the interrupted send was sent again after the boot (%d)", again.Load())
	}
}

// crashChild boots the identity the parent left, sends its one message
// through an adapter that lands its effect and kills the process.
func crashChild(t *testing.T, dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SourcePath = filepath.Join(filepath.Dir(cfg.Identity.DBPath), "config.json") // runtime-only; not in the JSON
	t.Chdir(dir)
	a := New(&cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		f, err := os.Create(filepath.Join(dir, "effect"))
		if err == nil {
			_, err = f.WriteString("the message left\n")
		}
		if err == nil {
			err = f.Sync()
		}
		if err == nil {
			err = f.Close()
		}
		if err != nil {
			os.Exit(2)
		}
		os.Exit(91)
		return tools.Result{}, nil
	})
	// Install the crash adapter before making mail visible. The booted
	// outbox is the one dispatch owner: a competing synchronous walk can
	// return after the background walk claims the row but before it sends.
	if err := a.store.AddOutboxMessage("msg_c2", "peer", "friend", "the build is green", nil); err != nil {
		t.Fatal(err)
	}
	a.pokeOutbox()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	<-ctx.Done()
	t.Fatal("the outbox did not reach the crash adapter")
}

func TestSendsQueuedBeforeSafeLeaveOnceAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	_, srv := newScriptedToolLLM(t, "NO-SCRIPTED-CALLS", nil, "noted", -1)
	a, _ := liveIdentity(t, dir, srv.URL)
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, a)
		}
	}()
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
	for _, id := range []string{"msg_one", "msg_two"} { // msg_one leaves first: created_ms, then id
		if err := a.store.AddOutboxMessage(id, "peer", "friend", "the build is green: "+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	var asked atomic.Int32
	entered, release := make(chan struct{}, 4), make(chan struct{})
	installScriptedAdapter(t, a, "org.example.primary", "primary", func(ctx context.Context) (tools.Result, error) {
		asked.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return tools.Result{Output: `{"receipt":"ok"}`}, nil
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
	})
	a.pokeOutbox() // the running outbox takes it, as a turn's end would have it
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the outbox never reached the adapter")
	}
	a.enterSafe("test: integrity lost while a send was in flight")
	close(release)
	if n, err := a.deliverOutbox(t.Context()); err != nil || n != 0 {
		t.Fatalf("a walk under SAFE carried %d (%v)", n, err)
	}
	if row := deliveryRow(t, a, "msg_two"); row.Delivered != 0 || row.Attempts != 0 || row.Parked {
		t.Fatalf("under SAFE the queued row is neither sent, counted nor parked: %+v", row)
	}
	cfg := a.configSnapshot()
	stopApp(t, a) // SAFE ends with the process; the walk in flight ends before Stop returns
	stopped = true
	if n := asked.Load(); n != 1 {
		t.Fatalf("under SAFE the adapter was asked %d time(s); only the send dispatched before it may have been", n)
	}

	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, next)
	if _, safe := next.SafeMode(); safe {
		t.Fatal("fixture: the next boot is NORMAL")
	}
	var again atomic.Int32
	installScriptedAdapter(t, next, "org.example.primary", "primary", func(context.Context) (tools.Result, error) {
		again.Add(1)
		return tools.Result{Output: `{"receipt":"again"}`}, nil
	})
	waitUntil(t, func() bool {
		if _, err := next.deliverOutbox(t.Context()); err != nil {
			t.Fatal(err)
		}
		return deliveryRow(t, next, "msg_two").Delivered == 1
	}, "the row held under SAFE leaves after it")
	if _, err := next.deliverOutbox(t.Context()); err != nil { // one more turn's end
		t.Fatal(err)
	}
	if n := again.Load(); n != 1 {
		t.Fatalf("after SAFE the adapter was asked %d time(s); the held row leaves once and the interrupted one never", n)
	}
	if row := deliveryRow(t, next, "msg_two"); row.Delivered != 1 || row.Attempts != 1 || row.DeliveredVia != "org.example.primary" {
		t.Fatalf("the held row is delivered once: %+v", row)
	}
	if row := deliveryRow(t, next, "msg_one"); row.Delivered != 0 || !row.Parked || row.Effect != "unknown" || row.Attempts != 1 {
		t.Fatalf("the send in flight when SAFE began is settled as unknown, not resent: %+v", row)
	}
}
