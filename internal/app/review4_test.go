package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// review4_test.go — a review's findings on the third freeze (2026-11-06): a
// queued promise firing is checked against the condition as it stands, and
// the exemption for the operator's own condition is read from the record;
// status, the seat and the plugin context of a resumed turn name its own
// session.

func fireQueued(t *testing.T, app *App, session string, at int64, setIn string) {
	t.Helper()
	firing := identity.TimerWake{AlarmID: identity.PromiseAlarmID(session), Deadline: at, Tag: identity.PromiseAlarmTag, Message: "the moment", SetIn: setIn}
	raw, _ := json.Marshal(firing)
	w := &store.WorkItem{ID: "wake_" + firing.DeliveryID(), Kind: identity.TimerWakeWorkKind, Payload: string(raw), DedupKey: firing.AlarmID + "@1"}
	if _, err := app.store.AddOutboxNoticeWithWork(firing.DeliveryID(), "[timer fired] the moment", w); err != nil {
		t.Fatal(err)
	}
	if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestAQueuedFiringIsCheckedAgainstTheConditionAsItStands(t *testing.T) {
	for _, c := range []struct{ name, then string }{{"cleared", ""}, {"replaced by the operator's word", "word"}, {"moved to a later moment", "later"}} {
		t.Run(c.name, func(t *testing.T) {
			app, _ := dispatchApp(t)
			defer stopApp(t, app)
			pressStage(t, app, store.StageTrusted)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
			app.executor.Stop()
			ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), turnSourceOperator)
			if err := app.store.StartWorkSession(ctx, "ws_q", "keep a promise"); err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Add(-time.Second).UnixMilli()
			promise, on := "in_1", "time:"+strconv.FormatInt(at, 10)
			if err := app.store.SetWorkPromise(ctx, "ws_q", &promise, &on, ""); err != nil {
				t.Fatal(err)
			}
			then := c.then
			if then == "later" {
				then = "time:" + strconv.FormatInt(at+1000, 10)
			}
			if err := app.store.SetWorkPromise(ctx, "ws_q", nil, &then, "the condition changed while the firing waited"); err != nil {
				t.Fatal(err)
			}
			// The firing was queued for the first moment, in the operator's turn.
			fireQueued(t, app, "ws_q", at, turnSourceOperator)
			if len(obs.snapshot()) != 0 {
				t.Fatal("a firing whose condition was " + c.name + " woke the identity")
			}
			if got := resumesOnOfSession(t, app, "ws_q"); got != then {
				t.Fatalf("the stale firing changed the condition: %q, want %q", got, then)
			}
		})
	}
	t.Run("the exemption is the record's, not the firing's payload", func(t *testing.T) {
		for _, c := range []struct {
			name, setIn, claims string
			wakes               bool
		}{
			{"the operator's condition fires under the hold whatever the payload says", turnSourceOperator, turnSourceContinuation, true},
			{"the identity's condition waits under the hold whatever the payload says", turnSourceContinuation, turnSourceOperator, false},
		} {
			t.Run(c.name, func(t *testing.T) {
				app, _ := dispatchApp(t)
				defer stopApp(t, app)
				pressStage(t, app, store.StageSupervised)
				obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
				app.executor.Stop()
				ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), c.setIn)
				if err := app.store.StartWorkSession(ctx, "ws_h", "keep a promise"); err != nil {
					t.Fatal(err)
				}
				at := time.Now().UTC().Add(-time.Second).UnixMilli()
				promise, on := "in_1", "time:"+strconv.FormatInt(at, 10)
				if err := app.store.SetWorkPromise(ctx, "ws_h", &promise, &on, ""); err != nil {
					t.Fatal(err)
				}
				fireQueued(t, app, "ws_h", at, c.claims)
				if woke := len(obs.snapshot()) > 0; woke != c.wakes {
					t.Fatalf("woke %v, want %v", woke, c.wakes)
				}
				if !c.wakes {
					if got := resumesOnOfSession(t, app, "ws_h"); got != "word" {
						t.Fatalf("the held condition is %q, want the operator's word", got)
					}
				}
			})
		}
	})
}

func TestAResumedTurnsStatusSeatAndPluginContextNameItsOwnSession(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	twoSessions(t, app, "word", "word")
	resumed := interaction.WithResumeSession(interaction.WithTurn(context.Background(), "turn_r"), "ws_older")
	state, err := app.readWorkState(resumed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "### Current work session: ws_older") || strings.Contains(state, "ws_newer") {
		t.Fatalf("the resumed turn's status describes the newer session:\n%s", state)
	}
	plain, err := app.readWorkState(context.Background())
	if err != nil || !strings.Contains(plain, "### Current work session: ws_newer") {
		t.Fatalf("a plain turn's status: %v\n%s", err, plain)
	}
	if seat := app.callSeat(resumed); seat.session != "ws_older" || seat.reason != "" {
		t.Fatalf("the resumed turn's seat: %+v", seat)
	}
	if seat := app.callSeat(context.Background()); seat.session != "ws_newer" {
		t.Fatalf("a plain turn's seat: %+v", seat)
	}
	p := addContextProbe(t, app, `{"text":"the older work's context"}`)
	app.pluginContext(resumed, "what is next")
	calls, _ := p.snapshot()
	if len(calls) != 1 || calls[0].ID != "ws_older" {
		t.Fatalf("the plugin context's acting session: %+v", calls)
	}
}

// THE HARVEST MEETS THE CONDITION UNDER THE PROMISE IT WAS KEPT UNDER
// (a review, 2026-11-06, round 5): at the real harvest boundary, with the
// promise changed alone after the condition was kept.
func TestAHarvestMeetsTheConditionUnderThePromiseItWasKeptUnder(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_two"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_older", "the older work"); err != nil {
		t.Fatal(err)
	}
	// The child is started first: a wait on a session that does not exist
	// is refused (store.DeliveryNeverComes).
	cctx := interaction.WithTurn(context.Background(), "turn_child")
	if _, err := app.store.EnqueueWork(&store.WorkItem{ID: "sub_ws_child", Kind: store.SubagentWorkKind, Payload: "{}", DedupKey: "ws_child"}); err != nil {
		t.Fatal(err)
	}
	if err := app.store.StartWorkSession(cctx, "ws_child", "find the figures"); err != nil {
		t.Fatal(err)
	}
	p1, on := "in_1", "delivery:ws_child"
	if err := app.store.SetWorkPromise(ctx, "ws_older", &p1, &on, ""); err != nil {
		t.Fatal(err)
	}
	p2 := "in_2"
	if err := app.store.SetWorkPromise(ctx, "ws_older", &p2, nil, "the promise changed; the condition stays"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.DeliverWorkSession(cctx, "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
	if !sawRequest(obs, "This delivery is what your work on ws_older waited for, under your promise in_1.")() {
		t.Fatal("the harvest did not name the promise the condition was kept under")
	}
	if on := resumesOnOfSession(t, app, "ws_older"); on != "" && on != "now" {
		t.Fatalf("the condition was not met: %q", on)
	}
	var under, met string
	if err := app.store.DB().QueryRow(`SELECT json_extract(details,'$.work.under'), json_extract(details,'$.work.met') FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.session')='ws_older' AND json_extract(details,'$.work.met') IS NOT NULL ORDER BY turn_seq DESC LIMIT 1`).Scan(&under, &met); err != nil {
		t.Fatal(err)
	}
	if under != "in_1" || met != "delivery:ws_child" {
		t.Fatalf("the harvest met the condition under %q (%s), want in_1", under, met)
	}
	if ok, _ := app.store.NewerFactCited("ws_older", "in_2", "in_2: ws_child delivered the figures.", time.Time{}); ok {
		t.Fatal("the floor cited a delivery awaited under in_1 as in_2's fact")
	}
}
