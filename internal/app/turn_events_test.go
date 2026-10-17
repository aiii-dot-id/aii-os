package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTurnEventsFailedTurnAndRestartPreserveDelivery(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("restart-mail", "mail", "friend", "kept after failed model"); err != nil {
		t.Fatal(err)
	}
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture model failure", http.StatusServiceUnavailable)
	}))
	defer fail.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: fail.URL, Model: "fake", Retries: -1}))
	if _, err := a.handleMessage(t.Context(), "read my mail"); err == nil {
		t.Fatal("failed model was successful")
	}
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("failed turn consumed events: %d %v", len(rows), err)
	}
	cfg := a.configSnapshot()
	stopApp(t, a)
	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, next)
	obs := &contScript{}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body strings.Builder
		_, _ = io.Copy(&body, r.Body)
		obs.add(body.String())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textReply("noted"))
	}))
	defer good.Close()
	next.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: good.URL, Model: "fake", Retries: -1}))
	operatorTurn(t, next, "read my mail")
	if reqs := obs.snapshot(); len(reqs) != 1 || !strings.Contains(reqs[0], "kept after failed model") {
		t.Fatalf("restart lost input: %v", reqs)
	}
	if rows, err := next.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("completed input not kept: %d %v", len(rows), err)
	}
}

func TestTurnEventsFoldingAndWriteFailureDoNotConsume(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("keep", "mail", "friend", "important"); err != nil {
		t.Fatal(err)
	}
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	a.recordTurnEventDelivery(t.Context(), "folded: inspect pending input")
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("folded event consumed: %d %v", len(rows), err)
	}
	if _, err := a.store.DB().Exec(`CREATE TRIGGER fail_event_input BEFORE INSERT ON turn_annotations WHEN new.kind LIKE 'turn_event.%' BEGIN SELECT RAISE(ABORT,'fixture refusal'); END`); err != nil {
		t.Fatal(err)
	}
	a.recordTurnEventDelivery(t.Context(), facts)
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("unkept input consumed: %d %v", len(rows), err)
	}
	if _, err := a.store.DB().Exec(`DROP TRIGGER fail_event_input`); err != nil {
		t.Fatal(err)
	}
	operatorTurn(t, a, "retry reading the retained event")
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("retry did not recover: %d %v", len(rows), err)
	}
}

func TestTurnEventsBoundAndFrameTheActualBatch(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("oversized-source", "mail", strings.Repeat("x", interaction.MaxPageBytes*2), "retained full body"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		if _, err := a.store.RecordInbound(fmt.Sprintf("mail-%03d", i), "mail", "friend", strings.Repeat("界", 20000)+untrusted.Close); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.buildTurnFacts(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if len(a.composedEvents) == 0 || len(a.composedEvents) >= 150 || len(a.composedEventText) > interaction.MaxPageBytes || !strings.Contains(a.composedEventText, "More pending") {
		t.Fatal("batch silently lost its bound/remainder")
	}
	for _, e := range a.composedEvents {
		if strings.Count(e.Text, untrusted.Open) != 1 || strings.Count(e.Text, untrusted.Close) != 1 || !strings.Contains(e.Text, "Excerpt") {
			t.Fatal("excerpt lost its untrusted frame")
		}
	}
	n := len(a.composedEvents)
	a.recordTurnEventDelivery(t.Context(), a.composedEventText)
	var kept int
	if err := a.store.DB().QueryRow(`SELECT count(*) FROM turn_annotations WHERE kind='turn_event.arrival'`).Scan(&kept); err != nil || kept != n {
		t.Fatalf("unrendered events acknowledged: %d want %d %v", kept, n, err)
	}
}

func TestTurnEventsDeliveryExcerptKeepsValidUTF8(t *testing.T) {
	a, _ := promptCacheApp(t)
	if err := a.store.AddOutboxMessage("unicode-delivery", "peer", "friend", "sent", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.RecordDeliveryAttempt(claimRow(t, a, "unicode-delivery"), strings.Repeat("界", 100), true, 1); err != nil {
		t.Fatal(err)
	}
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	a.recordTurnEventDelivery(t.Context(), facts)
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("invalid UTF-8 prevented keeping the presented outcome: %d %v", len(rows), err)
	}
}

func TestTurnEventsDirectParticipantInputIsNotRepeated(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("direct", "mail", "friend", "current arrival"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), inboundRecordKey{}, "direct")
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.wake(ctx, "participant", "current arrival"); err != nil {
		t.Fatal(err)
	}
	a.releaseTurn()
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("recoverable participant input repeated: %d %v", len(rows), err)
	}
}

func TestTurnEventsComposeSteerInterleavingDoesNotDuplicateArrival(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("overlap", "mail", "friend", "only once"); err != nil {
		t.Fatal(err)
	}
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer a.releaseTurn()
	if _, err := a.buildTurnFacts(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	a.carriedEvents = true // exactly the model-request admission boundary
	ctx := context.WithValue(t.Context(), inboundRecordKey{}, "overlap")
	if ok, err := a.steer(roleParticipant, "only once", nil, ctx); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	if got := a.DrainSteering(); len(got) != 0 {
		t.Fatalf("already-carried arrival repeated: %v", got)
	}
	// A failed turn still has no durable input acknowledgment for the batch.
	if rows, err := a.store.PendingTurnEvents(t.Context()); err != nil || len(rows) != 1 {
		t.Fatalf("queue dedupe swallowed recovery: %d %v", len(rows), err)
	}
}

// seedPendingEvents leaves one event of each kind pending: an arrival, a
// timer firing no turn was woken for, and a send whose effect is unknown.
func seedPendingEvents(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.store.RecordInbound("in_mail_seed", "mail", "friend", "L12_ARRIVAL_7be9"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddOutboxMessage("timer_seed_other_1", "operator", "", "L12_OTHER_TIMER_41af", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddOutboxMessage("msg_l12_outcome", "peer", "friend", "sent earlier", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.MarkEffectUnknown(claimRow(t, a, "msg_l12_outcome"), "mail", "fixture response lost"); err != nil {
		t.Fatal(err)
	}
}

var seededEventMarkers = []string{"L12_ARRIVAL_7be9", "L12_OTHER_TIMER_41af", "msg_l12_outcome"}

func pendingEvents(t *testing.T, a *App) []store.TurnEvent {
	t.Helper()
	rows, err := a.store.PendingTurnEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// fixtureModel answers every model request with f and keeps each body.
func fixtureModel(t *testing.T, a *App, f func(n int, w http.ResponseWriter)) *contScript {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		f(n, w)
	}))
	t.Cleanup(srv.Close)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, TimeoutSeconds: 5}))
	return obs
}

// requestText is every message the model was sent in one request, decoded.
func requestText(t *testing.T, body string) string {
	t.Helper()
	var b strings.Builder
	for _, m := range wireMessages(t, body) {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// requestWith is the one request that carried marker.
func requestWith(t *testing.T, obs *contScript, marker string) string {
	t.Helper()
	var found []string
	for _, body := range obs.snapshot() {
		if text := requestText(t, body); strings.Contains(text, marker) {
			found = append(found, text)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d requests carried %q, want exactly one", len(found), marker)
	}
	return found[0]
}

// Every way the identity is woken — its own alarm through the real
// scheduler, a contact's message through the channel admission, a capped
// turn's continuation — is shown what happened while it slept, in the
// request the model answered, and that answer acknowledges it. The turn's
// own input reaches the model once, and the firing that woke a timer turn
// is recorded once.
func TestEveryWokenTurnCarriesAndAcknowledgesPendingEvents(t *testing.T) {
	for _, kind := range []string{"timer", "arrival", "continuation"} {
		t.Run(kind, func(t *testing.T) {
			a, obs := promptCacheApp(t)
			seedPendingEvents(t, a)
			var own string
			switch kind {
			case "timer":
				own = "L12_ALARM_c3d1"
				if _, err := a.engine.ExecuteAction(t.Context(), "verb", "timer", map[string]any{
					"action": "set", "id": "l12", "duration": "50ms", "message": own,
				}); err != nil {
					t.Fatal(err)
				}
				waitUntil(t, func() bool {
					msgs, err := a.store.UndeliveredFor("operator")
					if err != nil {
						t.Fatal(err)
					}
					for _, m := range msgs {
						if strings.HasPrefix(m.ID, "wake_l12_") {
							return true
						}
					}
					return false
				}, "the alarm's wake turn to finish")
			case "arrival":
				own = "L12_WOKEN_ARRIVAL_5e0a"
				contact(t, a, Contact{Name: "Robin", Channel: "mail", Address: "robin@example.test", Wake: true})
				if _, err := a.store.RecordInbound("in_mail_l12", "mail", "robin@example.test", own); err != nil {
					t.Fatal(err)
				}
				a.carryInbound("in_mail_l12", channelRoute{Channel: "mail"}, arrival{ID: "l12", From: "robin@example.test", Body: own})
				waitUntil(t, func() bool { return !a.TurnActive() }, "the arrival's wake turn to finish")
			case "continuation":
				own = "[budget checkpoint — continuation 1]"
				a.continueCappedTurn("ws_l12", 1, capDeclared, "")
			}
			req := requestWith(t, obs, own)
			for _, want := range seededEventMarkers {
				if !strings.Contains(req, want) {
					t.Errorf("the woken %s turn's request omitted pending event %q", kind, want)
				}
			}
			if n := strings.Count(req, own); n != 1 {
				t.Errorf("the turn's own input reached the model %d times, want once", n)
			}
			if rows := pendingEvents(t, a); len(rows) != 0 {
				t.Errorf("the answered wake left %d event(s) pending: %+v", len(rows), rows)
			}
			if kind == "timer" {
				var n int
				if err := a.store.DB().QueryRow(`SELECT count(*) FROM turn_annotations WHERE kind='turn_event.timer' AND key LIKE 'timer_l12\_%' ESCAPE '\'`).Scan(&n); err != nil || n != 1 {
					t.Errorf("the waking firing was recorded %d times, want once (%v)", n, err)
				}
			}
		})
	}
}

// A wake whose model call fails was shown the events and acknowledges none
// of them: they wait for a turn that is answered.
func TestAFailedWakeAcknowledgesNothing(t *testing.T) {
	a, _ := promptCacheApp(t)
	seedPendingEvents(t, a)
	obs := fixtureModel(t, a, func(_ int, w http.ResponseWriter) { http.Error(w, "fixture failure", http.StatusServiceUnavailable) })
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := a.wake(t.Context(), "system", "an unrelated worker completed")
	a.releaseTurn()
	if err == nil {
		t.Fatal("the fixture model did not fail")
	}
	reqs := obs.snapshot()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "L12_ARRIVAL_7be9") {
		t.Fatalf("the failed wake was not shown the events: %d request(s)", len(reqs))
	}
	if rows := pendingEvents(t, a); len(rows) != 3 {
		t.Fatalf("a failed wake acknowledged events: %d of 3 still pending", len(rows))
	}
}

// A woken turn that carried an arrival does not say it again when the same
// arrival steers into the turn at its next tool boundary.
func TestAWokenTurnSaysACarriedArrivalOnceWhenItAlsoSteers(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("in_mail_steer", "mail", "friend", "L12_STEERED_ONCE_9b1c"); err != nil {
		t.Fatal(err)
	}
	steered := make(chan error, 1)
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			ok, err := a.steer(roleParticipant, "L12_STEERED_ONCE_9b1c", nil, context.WithValue(t.Context(), inboundRecordKey{}, "in_mail_steer"))
			if err == nil && !ok {
				err = fmt.Errorf("the steer was not queued")
			}
			steered <- err
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
			return
		}
		fmt.Fprint(w, textReply("noted"))
	})
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := a.wake(t.Context(), "system", "an unrelated worker completed")
	a.releaseTurn()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-steered; err != nil {
		t.Fatal(err)
	}
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("fixture rounds=%d", len(reqs))
	}
	for i, req := range reqs {
		if n := strings.Count(requestText(t, req), "L12_STEERED_ONCE_9b1c"); n != 1 {
			t.Errorf("round %d carried the arrival %d times, want once", i+1, n)
		}
	}
	if rows := pendingEvents(t, a); len(rows) != 0 {
		t.Fatalf("the carried arrival stayed pending: %+v", rows)
	}
}

// A send no adapter was asked to carry is told, with why it waits — derived
// as the turn is composed from SAFE, the operator's contacts and the
// installed adapters — and acknowledged at attempt 0. Its first real
// attempt is a new observation.
func TestANeverAttemptedSendIsToldWhyItWaits(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"no_contact", "- to Sasha: not sent — they are not in your operator's contacts; it stays queued until they are added"},
		{"no_adapter", "- to Sasha: not sent — no installed channel can carry it to them yet; it stays queued"},
		{"safe", "- to Sasha: not sent — nothing leaves in safe mode; it stays queued until safe mode ends"},
		{"ready", "- to Sasha: queued, not yet tried"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			a, obs := promptCacheApp(t)
			t.Cleanup(a.resetModeForTest)
			if tc.kind != "no_adapter" {
				installAdapter(t, a, "org.example.mail", "mail", "", "", nil)
			}
			if tc.kind != "no_contact" {
				contact(t, a, Contact{Name: "Sasha", Channel: "mail", Address: "sasha@example.test"})
			}
			if tc.kind == "ready" {
				// Keep this fixture unclaimed while its prompt is composed.
				// The boot outbox pass can otherwise send it before the turn,
				// correctly replacing "not yet tried" with a delivery outcome.
				if _, err := a.store.DB().Exec(`CREATE TRIGGER hold_fixture_dispatch
					BEFORE UPDATE OF dispatched_ms ON outbox WHEN OLD.id = 'msg_l12_waiting'
					BEGIN SELECT RAISE(ABORT, 'fixture keeps this send unclaimed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.store.AddOutboxMessage("msg_l12_waiting", "peer", "Sasha", "L12_WAITING_BODY", nil); err != nil {
				t.Fatal(err)
			}
			if tc.kind == "safe" {
				a.enterSafe("fixture hold")
			}
			if n, err := a.deliverOutbox(t.Context()); err != nil || n != 0 {
				t.Fatalf("the fixture send left: %d %v", n, err)
			}
			if tc.kind == "ready" {
				var attempts, delivered int
				var unclaimed bool
				if err := a.store.DB().QueryRow(`SELECT attempts, delivered, dispatched_ms IS NULL
					FROM outbox WHERE id = 'msg_l12_waiting'`).Scan(&attempts, &delivered, &unclaimed); err != nil || attempts != 0 || delivered != 0 || !unclaimed {
					t.Fatalf("fixture is not an unattempted send: attempts=%d delivered=%d unclaimed=%t: %v", attempts, delivered, unclaimed, err)
				}
			}
			operatorTurn(t, a, "What became of my send?")
			reqs := obs.snapshot()
			if len(reqs) != 1 {
				t.Fatalf("fixture requests=%d", len(reqs))
			}
			if text := requestText(t, reqs[0]); !strings.Contains(text, tc.want) {
				t.Fatalf("the answered request does not say why the send waits; want %q", tc.want)
			}
			if tc.kind != "no_contact" {
				return
			}
			if rows := pendingEvents(t, a); len(rows) != 0 {
				t.Fatalf("the told waiting send is still pending: %+v", rows)
			}
			contact(t, a, Contact{Name: "Sasha", Channel: "mail", Address: "sasha@example.test"})

			if _, err := a.deliverOutbox(t.Context()); err != nil {
				t.Fatalf("the send did not leave once reachable: %v", err)
			}
			waitUntil(t, func() bool {
				rows, err := a.store.OutboxOutcomesSince(0)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range rows {
					if m.ID == "msg_l12_waiting" && m.Delivered == 1 {
						return true
					}
				}
				return false
			}, "the send left once reachable")
			operatorTurn(t, a, "And now?")
			if reqs := obs.snapshot(); len(reqs) != 2 || !strings.Contains(requestText(t, reqs[1]), "- to Sasha: delivered via org.example.mail") {
				t.Fatal("the first real attempt was not a new observation")
			}
		})
	}
}

// A main work session the boot sweep closed is said on the first turn after
// boot, beside the interrupted tool calls — once. A sub-agent's closed
// session reaches the identity as its harvested outcome instead.
func TestASweptMainSessionIsSaidOnceAfterBoot(t *testing.T) {
	a, _ := promptCacheApp(t)
	if err := a.store.StartWorkSession(t.Context(), "ws_l12_main", "L12_SWEPT_SESSION_b627"); err != nil {
		t.Fatal(err)
	}
	queueChild(t, a.store, "ws_l12_child")
	if err := a.store.StartWorkSession(t.Context(), "ws_l12_child", store.SubagentDescription("L12_SWEPT_CHILD_77aa")); err != nil {
		t.Fatal(err)
	}
	cfg := a.configSnapshot()
	stopApp(t, a)
	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, next) })
	obs := fixtureModel(t, next, func(_ int, w http.ResponseWriter) { fmt.Fprint(w, textReply("noted")) })
	if ws, err := next.store.WorkSessionByID("ws_l12_main"); err != nil || ws == nil || ws.Status != "delivered" || ws.Evidence != store.EvidenceExternalUnknown {
		t.Fatalf("the boot did not sweep the session: %+v %v", ws, err)
	}
	operatorTurn(t, next, "What was interrupted?")
	operatorTurn(t, next, "Anything else?")
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("fixture requests=%d", len(reqs))
	}
	first := requestText(t, reqs[0])
	want := "### Interrupted at last shutdown\nWork session ws_l12_main (\"L12_SWEPT_SESSION_b627\") was active at the last shutdown and has been closed as interrupted — its external effects are unknown; verify before repeating them."
	if !strings.Contains(first, want) {
		t.Fatalf("the first turn after boot does not say the swept session; want %q", want)
	}
	if n := strings.Count(first, "L12_SWEPT_CHILD_77aa"); n != 1 {
		t.Fatalf("the swept sub-agent session was said %d times, want once (its harvest)", n)
	}
	if strings.Contains(requestText(t, reqs[1]), "L12_SWEPT_SESSION_b627") {
		t.Fatal("the swept session was said again after an answered turn")
	}
}

// An arrival recorded while the model thinks — with a lower ID and an
// earlier clock than what the turn carried — is not acknowledged by that
// turn and reaches the next one.
func TestAnArrivalDuringInferenceWaitsForTheNextTurn(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("z-first", "mail", "friend", "L12_EARLY_47fe"); err != nil {
		t.Fatal(err)
	}
	late := make(chan error, 1)
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, err := a.store.RecordInbound("a-late", "mail", "friend", "L12_LATE_a4d2")
			if err == nil {
				_, err = a.store.DB().Exec("UPDATE inbound SET received_ms=0 WHERE id='a-late'")
			}
			late <- err
		}
		fmt.Fprint(w, textReply("noted"))
	})
	operatorTurn(t, a, "read events")
	if err := <-late; err != nil {
		t.Fatal(err)
	}
	if reqs := obs.snapshot(); len(reqs) != 1 || strings.Contains(reqs[0], "L12_LATE_a4d2") || !strings.Contains(reqs[0], "L12_EARLY_47fe") {
		t.Fatal("the fixture did not insert the arrival after the request was composed")
	}
	if rows := pendingEvents(t, a); len(rows) != 1 || rows[0].ID() != "a-late" {
		t.Fatalf("pending after the first turn = %+v, want only the late arrival", rows)
	}
	operatorTurn(t, a, "read remaining events")
	if reqs := obs.snapshot(); len(reqs) != 2 || !strings.Contains(reqs[1], "L12_LATE_a4d2") || len(pendingEvents(t, a)) != 0 {
		t.Fatal("the late arrival was lost")
	}
}

// An arrival that steers in during a later tool round reaches the model
// once, in the round after it arrived, and the answered turn acknowledges it.
func TestAnArrivalSteeredInALaterRoundIsSeenOnce(t *testing.T) {
	a, _ := promptCacheApp(t)
	steered := make(chan error, 1)
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
		case 2:
			_, err := a.store.RecordInbound("in_mail_late_steer", "mail", "friend", "L12_STEER_96cc")
			if err == nil {
				var ok bool
				ok, err = a.steer(roleParticipant, "L12_STEER_96cc", nil, context.WithValue(t.Context(), inboundRecordKey{}, "in_mail_late_steer"))
				if err == nil && !ok {
					err = fmt.Errorf("the steer was not queued")
				}
			}
			steered <- err
			fmt.Fprint(w, callReply("", toolCall("r2", "ls", `{"path":"."}`)))
		default:
			fmt.Fprint(w, textReply("noted"))
		}
	})
	operatorTurn(t, a, "inspect the working directory twice")
	if err := <-steered; err != nil {
		t.Fatal(err)
	}
	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("fixture rounds=%d", len(reqs))
	}
	if strings.Contains(reqs[0], "L12_STEER_96cc") || strings.Contains(reqs[1], "L12_STEER_96cc") || strings.Count(requestText(t, reqs[2]), "L12_STEER_96cc") != 1 {
		t.Fatal("the late steer did not appear exactly once, in round three")
	}
	if rows := pendingEvents(t, a); len(rows) != 0 {
		t.Fatalf("the answered steer stayed pending: %+v", rows)
	}
}

// A turn that fails after an answered tool round acknowledges nothing it
// carried.
func TestAFailureAfterAToolRoundKeepsEventsPending(t *testing.T) {
	a, _ := promptCacheApp(t)
	seedPendingEvents(t, a)
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
			return
		}
		http.Error(w, "fixture second round failure", http.StatusServiceUnavailable)
	})
	if _, err := a.handleMessage(t.Context(), "inspect directory"); err == nil {
		t.Fatal("the fixture did not fail")
	}
	if len(obs.snapshot()) != 2 {
		t.Fatal("the fixture did not reach the second round")
	}
	if rows := pendingEvents(t, a); len(rows) != 3 {
		t.Fatalf("a failed multi-round turn consumed events: %d of 3 remain", len(rows))
	}
}

// A reply the loop rejects as empty is a failed turn: its events stay.
func TestARejectedEmptyReplyKeepsEventsPending(t *testing.T) {
	a, _ := promptCacheApp(t)
	seedPendingEvents(t, a)
	obs := fixtureModel(t, a, func(_ int, w http.ResponseWriter) { fmt.Fprint(w, textReply("")) })
	if _, err := a.handleMessage(t.Context(), "No response needed"); err == nil {
		t.Fatal("the empty fixture reply was accepted")
	}
	if len(obs.snapshot()) != 1 || len(pendingEvents(t, a)) != 3 {
		t.Fatal("a rejected empty reply consumed the events it carried")
	}
}

// A block the composer folded to fit was not shown, and a real composition
// under that pressure acknowledges nothing.
func TestAComposerFoldedEventBlockAcknowledgesNothing(t *testing.T) {
	a, obs := promptCacheApp(t)
	if _, err := a.store.RecordInbound("in_mail_fold", "mail", "friend", strings.Repeat("L12_FOLD_BODY_75fe long event sentence.\n", 900)); err != nil {
		t.Fatal(err)
	}
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	whole := a.composedEventText
	work, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	reserve, err := a.promptReserve(llm.Message{Role: "user", Content: "read events"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	folded := false
	for ceiling := 32000; ceiling >= 4000 && !folded; ceiling -= 500 {
		a.composer.SetMaxTokens(ceiling)
		p, err := a.composer.ComposeTurn(work, facts, "", reserve)
		folded = err == nil && !strings.Contains(p.Turn, whole)
	}
	if !folded {
		t.Fatal("the fixture could not produce a valid composition with the event block folded")
	}
	operatorTurn(t, a, "read events")
	reqs := obs.snapshot()
	if len(reqs) != 1 || strings.Contains(requestText(t, reqs[0]), whole) {
		t.Fatal("the fixture did not fold the block before sending")
	}
	if len(pendingEvents(t, a)) != 1 {
		t.Fatal("a folded event block was acknowledged")
	}
}

// deliveredChild makes a sub-agent's session delivered with result and not
// yet harvested.
func deliveredChild(t *testing.T, a *App, id, result string) {
	t.Helper()
	ctx := t.Context()
	if err := a.store.StartWorkSession(ctx, id, store.SubagentDescription("goal "+id)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES (?, ?, '{}', ?, 'identity', 'DONE')`, "wq_"+id, identity.SubagentWorkKind, id); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession(ctx, id, result, "", ""); err != nil {
		t.Fatal(err)
	}
}

// foldAway lowers the prompt budget until a valid composition of this
// turn's facts leaves gone out of the turn, as the operator turn that
// follows composes them.
//
// ONE BUDGET, AS A BOOT SETS IT. The composer's ceiling and the loop's
// admission limit are the same number in a running identity
// (provider_resolve.go sets both from the model's budget). This lowered
// the composer's alone, from 64,000, while the loop kept the test
// provider's fallback of 32,000: wherever the search stopped above that,
// the composer folded to its own ceiling and the client refused the
// request whole. Where it stopped moved with the size of the platform's
// tool descriptions, so a few words added to the shell tool's flipped
// TestAnOutcomeTheTurnDidNotCarryWakesOneTurnNotAChain on one platform.
// Both are set here.
func foldAway(t *testing.T, a *App, gone string) {
	t.Helper()
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(facts, gone) {
		t.Fatalf("rig: the facts do not compose %q", gone)
	}
	work, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	reserve, err := a.promptReserve(llm.Message{Role: "user", Content: "anything new"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for ceiling := 64000; ceiling >= 2000; ceiling -= 250 {
		a.composer.SetMaxTokens(ceiling)
		if p, err := a.composer.ComposeTurn(work, facts, "", reserve); err == nil && !strings.Contains(p.Turn, gone) {
			a.conv.SetModelLimits(ceiling, 0)
			return
		}
	}
	t.Fatalf("rig: no valid composition left %q out", gone)
}

// noHarvestWake leaves the test's own turns the only turns: no harvest wake
// races them for what one left unharvested.
func noHarvestWake(a *App) {
	off := false
	a.cfgMu.Lock()
	a.cfg.Agency.HarvestWake = &off
	a.cfgMu.Unlock()
}

func unharvestedIDs(t *testing.T, a *App) string {
	t.Helper()
	subs, err := a.store.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, w := range subs {
		ids = append(ids, w.ID)
	}
	return strings.Join(ids, ",")
}

func TestAnOutcomeTheTurnDidNotCarryIsNotHarvested(t *testing.T) {
	a, obs := promptCacheApp(t)
	noHarvestWake(a)
	deliveredChild(t, a, "ws_big", "served: the long one\n\n"+strings.Repeat("BIG_OUTCOME a long outcome paragraph with ordinary words in it.\n\n", 500))
	deliveredChild(t, a, "ws_unseen", "served: UNSEEN_OUTCOME_MARKER the second outcome")
	foldAway(t, a, "UNSEEN_OUTCOME_MARKER")
	operatorTurn(t, a, "anything new")
	reqs := obs.snapshot()
	if len(reqs) == 0 || strings.Contains(reqs[len(reqs)-1], "UNSEEN_OUTCOME_MARKER") {
		t.Fatal("rig: the request carried the outcome")
	}
	if got := unharvestedIDs(t, a); got != "ws_big,ws_unseen" {
		t.Fatalf("an outcome the model was never sent whole was marked harvested; unharvested now: %q", got)
	}
	operatorTurn(t, a, "and now?")
	last := obs.snapshot()[len(obs.snapshot())-1]
	if !strings.Contains(last, "UNSEEN_OUTCOME_MARKER") || !strings.Contains(last, "served: the long one") || !strings.Contains(last, "did not fit") {
		t.Fatal("the next turn must carry the second outcome whole and the first by its declared verdict line")
	}
	if got := unharvestedIDs(t, a); got != "" {
		t.Fatalf("what the next turn carried must be harvested; still unharvested: %q", got)
	}
}

func TestAClippedOutcomeNamesTheReadThatReturnsItWhole(t *testing.T) {
	a, obs := promptCacheApp(t)
	noHarvestWake(a)
	whole := "served: the long one\n\n" + strings.Repeat("BIG_OUTCOME a long outcome paragraph with ordinary words in it.\n\n", 500)
	deliveredChild(t, a, "ws_big", whole)
	foldAway(t, a, "BIG_OUTCOME")
	operatorTurn(t, a, "anything new")
	operatorTurn(t, a, "and now?")
	reqs := obs.snapshot()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last, "did not fit") || strings.Contains(last, "BIG_OUTCOME") {
		t.Fatal("rig: the next turn must tell the outcome by its verdict line alone")
	}
	if !strings.Contains(last, "work action=status id=ws_big") {
		t.Error("the clipped verdict does not name the read that returns the whole result")
	}
	var got strings.Builder
	id := "ws_big"
	for part := 1; ; part++ {
		var call llm.ToolCall
		call.Type = "function"
		call.Function.Name = "work"
		call.Function.Arguments = fmt.Sprintf(`{"action":"status","id":%q}`, id)
		o := a.executeToolCall(t.Context(), call)
		_, body, framed := strings.Cut(o.Text, "\n\n")
		end := strings.LastIndex(body, "\n\n[end of ")
		if o.Failed || !framed || end < 0 || part > 10 {
			t.Fatalf("the named read did not return part %d: %.300q", part, o.Text)
		}
		got.WriteString(body[:end])
		_, next, more := strings.Cut(body[end:], `continues: work action=status id="`)
		if !more {
			break
		}
		id, _, _ = strings.Cut(next, `"`)
	}
	if got.String() != whole {
		t.Fatalf("the named read returned %d of the result's %d bytes", got.Len(), len(whole))
	}
}

// AN OUTCOME NO TURN CARRIES WAKES ONE MORE TURN, NOT A CHAIN. The sweep
// at a turn's end wakes a harvest turn for any delivery still unharvested,
// which meant one that landed after the prompt was built. An outcome the
// turn composed and did not carry is unharvested too, and waking for it
// woke a turn that could not carry it either, and so on for as long as
// the facts could not hold it. It wakes once, to be told by its verdict
// line; the turn that wakes does not wake another for it.
func TestAnOutcomeTheTurnDidNotCarryWakesOneTurnNotAChain(t *testing.T) {
	a, obs := promptCacheApp(t)
	capture := logsink.CaptureForTest(t)
	// Facts no turn at this ceiling carries at all: a crash disclosure
	// of one long line, ahead of the outcomes, keeps the folded block
	// too large, and the composer omits it.
	for i := range 4000 {
		a.bootInterrupted = append(a.bootInterrupted, fmt.Sprintf("fixture_tool_%04d", i))
	}
	deliveredChild(t, a, "ws_wide", "served: WIDE_OUTCOME a short outcome")
	foldAway(t, a, "Sub-agent outcomes")
	operatorTurn(t, a, "anything new")
	deadline := time.Now().Add(30 * time.Second)
	for len(obs.snapshot()) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the turn that did not carry the outcome woke no turn to tell it: %d request(s)\n%s", len(obs.snapshot()), capture.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The woken turn holds the gate until it has ended, its own sweep
	// included: once the gate is ours, every wake it fired is logged.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	wakes := strings.Count(capture.String(), "waking to harvest")
	a.releaseTurn()
	if wakes != 1 {
		t.Fatalf("an outcome no turn carried whole woke %d harvest turns, want 1", wakes)
	}
}

// The crash disclosure is consumed only by a turn that carried it: one
// whose facts were omitted for the budget succeeded without it.
func TestACrashNoteTheTurnDidNotCarryIsToldAgain(t *testing.T) {
	a, _ := promptCacheApp(t)
	noHarvestWake(a)
	a.bootInterrupted = []string{"write"}
	deliveredChild(t, a, "ws_huge", "served: a huge outcome\n\n"+strings.Repeat("HUGE_OUTCOME paragraph with ordinary words in it, long enough to matter.\n\n", 2000))
	foldAway(t, a, "Interrupted at last shutdown")
	operatorTurn(t, a, "anything new")
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(facts, "Interrupted at last shutdown") {
		t.Fatal("a turn that did not carry the crash disclosure consumed it")
	}
}

// The model notice rides last in the facts, the first thing a fold drops,
// and is told only by a turn that carried it.
func TestAModelNoticeTheTurnDidNotCarryIsToldAgain(t *testing.T) {
	a, _ := promptCacheApp(t)
	noHarvestWake(a)
	deliveredChild(t, a, "ws_long", "served: a long outcome\n\n"+strings.Repeat("LONG_OUTCOME paragraph with ordinary words in it.\n\n", 500))
	foldAway(t, a, "### Model")
	operatorTurn(t, a, "anything new")
	if a.modelNotice() == "" {
		t.Fatal("a turn that did not carry the model notice marked it told")
	}
}

// An event carried in round one stays acknowledged by the answered turn even
// when round two's request folds a large tool result to fit.
func TestAnEventCarriedBeforeARefitFoldIsAcknowledged(t *testing.T) {
	a, _ := promptCacheApp(t)
	if _, err := a.store.RecordInbound("in_mail_refit", "mail", "friend", "L12_REFIT_728d"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(a.configSnapshot().Tools.CWD, "large-read.txt")
	if err := os.WriteFile(file, []byte(strings.Repeat("fixture read line with ordinary text and no actions.\n", 6000)), 0o600); err != nil {
		t.Fatal(err)
	}
	a.conv.SetModelLimits(16000, 0)
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			fmt.Fprint(w, callReply("", toolCall("read1", "read", fmt.Sprintf(`{"file_path":%q}`, file))))
			return
		}
		fmt.Fprint(w, textReply("noted"))
	})
	operatorTurn(t, a, "Read the fixture once")
	reqs := obs.snapshot()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "tool result folded under context pressure") {
		t.Fatal("the fixture did not cause a real request refit fold")
	}
	for i, req := range reqs {
		if !strings.Contains(req, "L12_REFIT_728d") {
			t.Errorf("the event is absent from request %d", i+1)
		}
	}
	if len(pendingEvents(t, a)) != 0 {
		t.Fatal("an event carried in an answered round was not acknowledged")
	}
}

// A turn that sent a message in its tool round and then failed keeps every
// event it carried and the new outcome pending; the next turn is told, and
// the row is not sent again.
func TestAFailedTurnAfterASendKeepsItsEventsAndSendsOnce(t *testing.T) {
	a, _ := promptCacheApp(t)
	seedPendingEvents(t, a)
	var effects atomic.Int32
	a.toolReg.Register(&fixtureTool{name: "fixture_delivery", run: func(ctx context.Context) (tools.Result, error) {
		n, err := a.deliverOutbox(ctx)
		return tools.Result{Output: fmt.Sprintf("fixture delivery count=%d", n)}, err
	}})
	installReasonedAdapterCounting(t, a, &effects)
	contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "fixture@example.test"})
	if err := a.store.AddOutboxMessage("msg_l12_send", "peer", "friend", "fixture body", nil); err != nil {
		t.Fatal(err)
	}
	obs := fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			fmt.Fprint(w, callReply("", toolCall("send1", "fixture_delivery", `{}`)))
			return
		}
		http.Error(w, "fixture model failed after the send", http.StatusServiceUnavailable)
	})
	if _, err := a.handleMessage(t.Context(), "deliver the queued fixture row"); err == nil {
		t.Fatal("the fixture did not fail after its tool round")
	}
	if len(obs.snapshot()) != 2 || effects.Load() != 1 {
		t.Fatalf("the fixture failed before its send: requests=%d effects=%d", len(obs.snapshot()), effects.Load())
	}
	if rows := pendingEvents(t, a); len(rows) != 4 {
		t.Fatalf("the failed turn lost an input or the outcome: %d of 4 pending", len(rows))
	}
	obs = fixtureModel(t, a, func(_ int, w http.ResponseWriter) { fmt.Fprint(w, textReply("noted")) })
	operatorTurn(t, a, "review what happened before the failure")
	if _, err := a.deliverOutbox(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reqs := obs.snapshot(); len(reqs) != 1 || !strings.Contains(requestText(t, reqs[0]), "- to friend: delivered via org.example.primary") || len(pendingEvents(t, a)) != 0 || effects.Load() != 1 {
		t.Fatal("the recovery turn lost the outcome, or the row was sent again")
	}
}

// fixtureTool is a host tool whose run the test supplies.
type fixtureTool struct {
	name string
	run  func(context.Context) (tools.Result, error)
}

func (f *fixtureTool) Name() string        { return f.name }
func (f *fixtureTool) Description() string { return "isolated fixture tool" }
func (f *fixtureTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *fixtureTool) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	return f.run(ctx)
}

// installReasonedAdapterCounting installs a "primary" channel adapter whose
// every send is counted, safely, as one external effect.
func installReasonedAdapterCounting(t *testing.T, a *App, effects *atomic.Int32) {
	t.Helper()
	const id = "org.example.primary"
	send := &fixtureTool{name: "pl_" + id + "_send", run: func(context.Context) (tools.Result, error) {
		effects.Add(1)
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	}}
	for _, op := range []tools.Tool{send, &fakeOp{name: "pl_" + id + "_describe", out: `{"channel":"primary"}`}, &fakeOp{name: "pl_" + id + "_receive", out: "[]"}} {
		if err := a.toolReg.RegisterHostOp(op, id); err != nil {
			t.Fatal(err)
		}
	}
	addPlugin(a, activeChannel(id, send.name, "pl_"+id+"_receive", "pl_"+id+"_describe"))
}
