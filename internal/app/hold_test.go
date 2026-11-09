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

// hold_test.go — the operator's hold (the resident design §2): the stage
// pressed to supervised, bound by their digest. Under it nothing of the
// identity's resumes unattended: a checkpoint under a promise, a promise's
// alarm (unless the operator's own turn set it), a child's delivery. What
// comes due is said and waits for their word; their stop holds the promise
// the stopped turn was resuming.

// pressStage records the operator's press for a level and the identity's
// upsert carrying its digest, through the identity's own commit.
func pressStage(t *testing.T, a *App, level string) {
	t.Helper()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_press_"+level), turnSourceOperator)
	// The press is on the identity's operator relationship: rel_quinn_01,
	// which supersedes a birth fixture's row where one exists (the verb
	// mints only ids of its own form), by the identity's own commit.
	rel, name := "rel_quinn_01", "Quinn"
	args := map[string]any{"variant": "relationship.upsert", "id": rel, "counterpart_name": name, "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "CHARTER-TEXT Quinn and I build carefully.", "autonomy_level": level}
	if cur, err := a.store.CurrentOperatorRelationship(); err == nil && cur != nil && cur.ID != rel {
		args["supersedes"] = cur.ID
	}
	digest := store.AutonomyApprovalDigest(rel, level)
	if err := a.store.AddConversationTurn("operator", "I approve the charter."); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RecordOperatorPress(context.Background(), "Quinn's identity", level, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := a.engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
		t.Fatal(err)
	}
	if got := a.operatorStage(); got != level {
		t.Fatalf("the press did not bind the level: %q", got)
	}
}

func heldNotice(t *testing.T, a *App) string {
	t.Helper()
	var content string
	_ = a.store.DB().QueryRow(`SELECT content FROM outbox WHERE id LIKE 'held_%' ORDER BY rowid DESC LIMIT 1`).Scan(&content)
	return content
}

func TestUnderTheOperatorsHoldNothingResumesUnattended(t *testing.T) {
	t.Run("a checkpoint under a promise", func(t *testing.T) {
		app, dir := dispatchApp(t)
		defer stopApp(t, app)
		script := cappedUnderPromise(t, dir, false, false)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, script(n)) })
		pressStage(t, app, store.StageSupervised)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		reply, err := app.observeChat(ctx, "go on", func(kind, name, args string) {})
		if err != nil {
			t.Fatal(err)
		}
		if got := recordedReply(t, app, reply); !strings.Contains(got, "Nothing continues by itself: your operator holds this work; it resumes on their word.") {
			t.Fatalf("the reply does not say the operator holds the work: %q", got)
		}
		if on := resumesOnOf(t, app); on != "word" {
			t.Fatalf("the held checkpoint's condition is %q, want the operator's word", on)
		}
		if n := heldNotice(t, app); !strings.Contains(n, "It waits for your word.") {
			t.Fatalf("the operator was not told: %q", n)
		}
		time.Sleep(300 * time.Millisecond)
		if sawRequest(obs, "budget checkpoint")() {
			t.Fatal("a fresh turn followed under the hold")
		}
	})
	t.Run("a promise's alarm, by who set it", func(t *testing.T) {
		for _, setIn := range []string{turnSourceContinuation, turnSourceOperator} {
			t.Run(setIn, func(t *testing.T) {
				app, _ := dispatchApp(t)
				defer stopApp(t, app)
				obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
				app.executor.Stop()
				pressStage(t, app, store.StageSupervised)
				ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), setIn)
				if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
					t.Fatal(err)
				}
				ws, _ := app.store.ActiveWorkSession()
				// The condition is written by the host (the identity's own write under the hold becomes word).
				at := time.Now().UTC().Add(-time.Second).UnixMilli()
				firing := identity.TimerWake{AlarmID: identity.PromiseAlarmID(ws.ID), Deadline: at, Tag: identity.PromiseAlarmTag, Message: "the moment", SetIn: setIn}
				raw, _ := json.Marshal(firing)
				w := &store.WorkItem{ID: "wake_" + firing.DeliveryID(), Kind: identity.TimerWakeWorkKind, Payload: string(raw), DedupKey: firing.AlarmID + "@1"}
				if _, err := app.store.AddOutboxNoticeWithWork(firing.DeliveryID(), "[timer fired] the moment", w); err != nil {
					t.Fatal(err)
				}
				on := "time:" + itoa64(at)
				promise := "in_1"
				if err := app.store.SetWorkPromise(ctx, ws.ID, &promise, &on, ""); err != nil {
					t.Fatal(err)
				}
				if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
					t.Fatal(err)
				}
				time.Sleep(200 * time.Millisecond)
				woke := len(obs.snapshot()) > 0
				if setIn == turnSourceOperator && !woke {
					t.Fatal("an alarm the operator's own turn set did not fire under the hold")
				}
				if setIn == turnSourceContinuation {
					if woke {
						t.Fatal("a promise's alarm woke the identity under the hold")
					}
					if got := resumesOnOf(t, app); got != "word" {
						t.Fatalf("the held alarm's condition is %q, want the operator's word", got)
					}
					if n := heldNotice(t, app); !strings.Contains(n, "the moment named with resumes_on came. It waits for your word.") {
						t.Fatalf("the operator was not told: %q", n)
					}
				}
			})
		}
	})
	t.Run("a delivery", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		pressStage(t, app, store.StageSupervised)
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.StartWorkSession(ctx, "ws_parent", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		subagentChild(t, app, "ws_child")
		promise, on := "in_1", "delivery:ws_child"
		if err := app.store.SetWorkPromise(ctx, "ws_parent", &promise, &on, ""); err != nil {
			t.Fatal(err)
		}
		if err := app.store.DeliverWorkSession(ctx, "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
		if len(obs.snapshot()) != 0 {
			t.Fatal("a delivery woke the identity under the hold")
		}
		if got := resumesOnOf(t, app); got != "word" {
			t.Fatalf("the held delivery's condition is %q, want the operator's word", got)
		}
		unh, _ := app.store.UnharvestedDeliveries(8)
		if len(unh) != 1 {
			t.Fatalf("the delivery was not kept for the operator's word: %d", len(unh))
		}
	})
	t.Run("the operator's stop", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		leg := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_leg"), turnSourceContinuation)
		if err := app.store.StartWorkSession(leg, "ws_stop", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		promise, on := "in_1", "now"
		if err := app.store.SetWorkPromise(leg, "ws_stop", &promise, &on, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 1] Work session ws_stop is still active.", "turn_leg", "", interaction.Details{}); err != nil {
			t.Fatal(err)
		}
		app.turnMu.Lock()
		app.turnCancel = func(error) {}
		app.interactionTurn = "turn_leg"
		app.turnMu.Unlock()
		if !app.CancelTurn() {
			t.Fatal("no turn to cancel")
		}
		if got := resumesOnOf(t, app); got != "word" {
			t.Fatalf("the stopped promise's condition is %q, want the operator's word", got)
		}
		if n := promiseNotice(t, app); !strings.Contains(n, "they stopped the turn that was resuming this work") {
			t.Fatalf("the record does not say why: %q", n)
		}
	})
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
