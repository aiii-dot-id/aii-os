package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// conditions_test.go — the host meets each condition a session's promise
// names (the resident design, step 3): the operator's word where their
// words reach a turn, a delivery at the harvest, the moment at the alarm
// the host keeps for it; a sub-agent that ends unfinished clears the
// condition with the reason; a boot keeps the session open and says so.

// recordPromise puts an open intention (in_…) or commitment (cm_…) with
// the given id into the record, so a work call may name it as its promise
// (a promise must name an open one). A commitment is to the operator's
// relationship, which pressStage put there.
func recordPromise(t *testing.T, a *App, id string) {
	t.Helper()
	ctx := interaction.WithTurn(context.Background(), "turn_record_"+id)
	args := map[string]any{"variant": "intention.create", "id": id, "statement": "keep a promise", "why": "a test's reason"}
	if strings.HasPrefix(id, "cm_") {
		rel, err := a.store.CurrentOperatorRelationship()
		if err != nil || rel == nil {
			t.Fatalf("no operator relationship to commit to (pressStage first): %v", err)
		}
		args = map[string]any{"variant": "commitment.promised", "id": id, "counterpart_id": rel.ID, "description": "keep a promise"}
	}
	if _, err := a.engine.ExecuteAction(ctx, "verb", "commit", args); err != nil {
		t.Fatalf("record the promise %s: %v", id, err)
	}
}

func sessionUnder(t *testing.T, a *App, resumesOn string) *store.WorkSession {
	t.Helper()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), turnSourceOperator)
	if _, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	recordPromise(t, a, "in_1")
	if child, ok := strings.CutPrefix(resumesOn, "delivery:"); ok {
		subagentChild(t, a, child)
	}
	if _, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "promise": "in_1", "resumes_on": resumesOn}); err != nil {
		t.Fatal(err)
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no session: %v", err)
	}
	return ws
}

// subagentChild makes id a sub-agent's session, as a spawn does (its queue
// row, then its session), so a wait on its delivery can be kept
// (store.DeliveryNeverComes); nothing runs it.
func subagentChild(t *testing.T, a *App, id string) {
	t.Helper()
	if ws, err := a.store.WorkSessionByID(id); err != nil || ws != nil {
		return
	}
	if _, err := a.store.EnqueueWork(&store.WorkItem{ID: "sub_" + id, Kind: store.SubagentWorkKind, Payload: "{}", DedupKey: id}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(interaction.WithTurn(context.Background(), "turn_child"), id, "find the figures"); err != nil {
		t.Fatal(err)
	}
}

func TestTheHostMeetsEachCondition(t *testing.T) {
	t.Run("word", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		sessionUnder(t, app, "word")
		ctx := goldenTurnCtx(t)
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := app.observeChat(ctx, "go on", func(kind, name, args string) {}); err != nil {
			t.Fatal(err)
		}
		if on := resumesOnOf(t, app); on != "" && on != "now" {
			t.Fatalf("the operator's word did not meet the condition: %q", on)
		}
		if !noticeSays(t, app, "The operator's word came") {
			t.Fatal("the record does not say the operator's word met the condition")
		}
	})
	t.Run("time", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		// A moment just ahead: the verb refuses one already past
		// (85842ca0), and the firing below is driven by hand.
		at := time.Now().UTC().Add(700 * time.Millisecond).UnixMilli()
		ws := sessionUnder(t, app, "time:"+strconv.FormatInt(at, 10))
		time.Sleep(time.Until(time.UnixMilli(at)) + 100*time.Millisecond) // the moment comes
		alarm, found, err := app.store.AlarmByID(identity.PromiseAlarmID(ws.ID))
		if err != nil || !found {
			t.Fatalf("no alarm keeps the moment: %v %v", found, err)
		}
		if r := app.timerOwner.OnAlarm(goldenTurnCtx(t), alarm.AlarmID, "wall", alarm.Deadline, alarm.Payload); !r.Accepted {
			t.Fatalf("the firing was not accepted: %+v", r)
		}
		if _, err := app.store.DeleteAlarmCAS(alarm.AlarmID, alarm.Deadline); err != nil { // as TIME retires a fired alarm
			t.Fatal(err)
		}
		w, err := app.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
		if err != nil || w == nil {
			t.Fatalf("no queued wake: %+v %v", w, err)
		}
		if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
			t.Fatal(err)
		}
		if !sawRequest(obs, "Work session "+ws.ID+" resumes now: the moment you named with resumes_on has come.")() {
			t.Fatal("the alarm's wake does not say the moment came")
		}
		if on := resumesOnOf(t, app); on != "" {
			t.Fatalf("the moment did not meet the condition: %q", on)
		}
		if n := promiseNotice(t, app); !strings.Contains(n, "The moment named with resumes_on came") {
			t.Fatalf("the record does not say why: %q", n)
		}
	})
	t.Run("delivery", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop() // the child is a sub-agent's session by its queue item; nothing runs it here
		ws := sessionUnder(t, app, "delivery:ws_child")
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.DeliverWorkSession(ctx, "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
		// in_1 is in the record (recordPromise), so the words name its kind.
		if !sawRequest(obs, "This delivery is what your work on "+ws.ID+" waited for, under your intention in_1.")() {
			t.Fatal("the harvest does not name the promise the delivery met")
		}
		if on := resumesOnOf(t, app); on != "" && on != "now" {
			t.Fatalf("the delivery did not meet the condition: %q", on)
		}
	})
	// A FAILED RUN THAT DELIVERED ITS REPORT KEEPS THE WAIT (a review,
	// 2026-11-08): the report is a delivery still to be taken in, so the
	// harvest meets the wait under the operator's hold. Under supervision
	// the harvest asks the model nothing, and the report stays to be read.
	// Only a delivery that can never come clears the wait, with the reason.
	t.Run("a child that ended unfinished", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		pressStage(t, app, store.StageSupervised)
		sessionUnder(t, app, "delivery:ws_child")
		// As the adapter ends a run that failed: its report delivered, then closed.
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.DeliverWorkSession(ctx, "ws_child", "FAILED: its first request to the model provider failed", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		(&subagentHandler{app}).closeChild(identity.SubagentRequest{SessionID: "ws_child", Goal: "find the figures"}, 1, 0, 0, 0, "",
			errors.New("its first request to the model provider failed"), "FAILED: its first request to the model provider failed", "failed")
		// closeChild's own harvest wake runs in the background: under the
		// hold it holds the report for the operator's word and asks the model
		// nothing. Before, the wait was cleared first, and that wake ran the
		// harvest past the hold.
		waitUntil(t, func() bool { return resumesOnOf(t, app) == "word" }, "the delivered failure held for the operator's word")
		if n := len(obs.snapshot()); n != 0 {
			t.Fatalf("under the operator's hold the harvest asked the model %d time(s)", n)
		}
		if left, err := app.store.UnharvestedDeliveries(8); err != nil || len(left) != 1 {
			t.Fatalf("the failure's report is no longer there to be read: %d (%v)", len(left), err)
		}

		// A child that can never deliver: its wait is cleared, saying why.
		subagentChild(t, app, "ws_lost")
		main, err := app.store.ActiveWorkSession()
		if err != nil || main == nil {
			t.Fatalf("no session: %v", err)
		}
		lost := "delivery:ws_lost"
		if err := app.store.SetWorkPromise(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), turnSourceOperator), main.ID, nil, &lost, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.DB().Exec(`UPDATE work_sessions SET status = 'abandoned' WHERE id = 'ws_lost'`); err != nil {
			t.Fatal(err)
		}
		app.childEndedUnfinished("ws_lost", "its runtime stopped")
		if on := resumesOnOf(t, app); on != "" {
			t.Fatalf("a child that can never deliver left the wait: %q", on)
		}
		if n := promiseNotice(t, app); !strings.Contains(n, "Sub-agent ws_lost ended unfinished (its runtime stopped)") {
			t.Fatalf("the record does not say why: %q", n)
		}
		app.childEndedUnfinished("ws_other", "x") // no condition on it: nothing written
	})
	// A DELIVERY MADE BEFORE A RESTART AND NOT YET TAKEN IN SURVIVES IT
	// (a review, 2026-11-08): the boot clears a wait only when its delivery can
	// never come, the rule a new wait is kept by. Once taken in, it is gone.
	t.Run("boot keeps a wait whose delivery is still to be taken in", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		app.executor.Stop()
		sessionUnder(t, app, "delivery:ws_kept")
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.DeliverWorkSession(ctx, "ws_kept", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := app.store.SweepOrphanWorkSessions(); err != nil {
			t.Fatal(err)
		}
		app.promiseConditionsAtBoot()
		if on := resumesOnOf(t, app); on != "delivery:ws_kept" {
			t.Fatalf("the boot cleared a wait whose delivery is still to be taken in: %q", on)
		}
		// Marked taken in with its wait left standing, as a release before
		// store.TakeInDelivery could leave a record (it meets the waits in
		// the same transaction now).
		if _, err := app.store.DB().Exec(`UPDATE work_sessions SET harvested_ms = 1762600000000 WHERE id = 'ws_kept'`); err != nil {
			t.Fatal(err)
		}
		app.promiseConditionsAtBoot()
		if on := resumesOnOf(t, app); on != "" {
			t.Fatalf("the boot kept a wait whose delivery was already taken in: %q", on)
		}
		if n := promiseNotice(t, app); !strings.Contains(n, "already taken in") {
			t.Fatalf("the record does not say why: %q", n)
		}
	})
	t.Run("boot keeps the session and says so", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		ws := sessionUnder(t, app, "delivery:ws_gone")
		// The child did not survive the restart: its session is no longer
		// working when the boot reads the conditions.
		if _, err := app.store.DB().Exec(`UPDATE work_sessions SET status = 'abandoned' WHERE id = 'ws_gone'`); err != nil {
			t.Fatal(err)
		}
		// As a boot does: the sweep, then the conditions.
		swept, err := app.store.SweepOrphanWorkSessions()
		if err != nil || len(swept) != 0 {
			t.Fatalf("the sweep closed the session under a condition: %+v %v", swept, err)
		}
		app.promiseConditionsAtBoot()
		if on := resumesOnOf(t, app); on != "" {
			t.Fatalf("a child that did not survive the restart left the condition: %q", on)
		}
		ctx := goldenTurnCtx(t)
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := app.observeChat(ctx, "hello", func(kind, name, args string) {}); err != nil {
			t.Fatal(err)
		}
		want := "Work session " + ws.ID + " (\"keep a promise\") was active at the last shutdown and stays open; its sub-agent did not survive the restart, so nothing resumes it until you say what does. Nothing was repeated. Verify what the record shows before continuing."
		if !strings.Contains(requestText(t, obs.snapshot()[0]), want) {
			t.Fatalf("the boot line was not said:\n%s", requestText(t, obs.snapshot()[0]))
		}
		if app.bootKept != nil {
			t.Fatal("the boot line was not consumed by the turn that carried it")
		}
	})
}

// noticeSays reports whether any work notice in the record contains the
// words: the latest notice may be the host's own continuation (work
// continues), so a met condition is read from the whole record.
func noticeSays(t *testing.T, a *App, words string) bool {
	t.Helper()
	var n int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='notice' AND content LIKE ?`, "%"+words+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// A WAIT THAT COMES DUE DURING ITS SETTING TURN IS MET BY ITS WAKE (a review,
// 2026-11-06): the moment comes while the turn that set it still holds the
// gate, the firing is queued and the alarm row gone; that turn's end leaves
// the condition standing, and the wake's turn meets it.
func TestAWaitThatComesDueDuringItsSettingTurnIsMetByItsWake(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	at := time.Now().UTC().Add(700 * time.Millisecond).UnixMilli()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(at, 10))     // set in the operator's turn, turn_c
	time.Sleep(time.Until(time.UnixMilli(at)) + 100*time.Millisecond) // the moment comes while that turn runs
	alarm, found, err := app.store.AlarmByID(identity.PromiseAlarmID(ws.ID))
	if err != nil || !found {
		t.Fatalf("no alarm keeps the moment: %v %v", found, err)
	}
	if r := app.timerOwner.OnAlarm(goldenTurnCtx(t), alarm.AlarmID, "wall", alarm.Deadline, alarm.Payload); !r.Accepted {
		t.Fatalf("the firing was not accepted: %+v", r)
	}
	if _, err := app.store.DeleteAlarmCAS(alarm.AlarmID, alarm.Deadline); err != nil {
		t.Fatal(err)
	}
	// The setting turn ends: its settle leaves the condition for the wake.
	app.settleMetConditions("turn_set")
	if on := resumesOnOf(t, app); on != "time:"+strconv.FormatInt(at, 10) {
		t.Fatalf("the setting turn's end cleared the condition: %q", on)
	}
	w, err := app.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
	if err != nil || w == nil {
		t.Fatalf("no queued wake: %+v %v", w, err)
	}
	if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
		t.Fatal(err)
	}
	if !sawRequest(obs, "Work session "+ws.ID+" resumes now: the moment you named with resumes_on has come.")() {
		t.Fatal("the queued wake did not run on the condition")
	}
	if on := resumesOnOf(t, app); on != "" {
		t.Fatalf("the wake did not meet the condition: %q", on)
	}
}

// fireAlarm is the clock reaching a session's moment: the alarm fires,
// its wake is queued, and its row goes, as the timer does.
func fireAlarm(app *App, ctx context.Context, sessionID string) error {
	alarm, found, err := app.store.AlarmByID(identity.PromiseAlarmID(sessionID))
	if err != nil || !found {
		return fmt.Errorf("no alarm keeps the moment: %v %v", found, err)
	}
	if r := app.timerOwner.OnAlarm(ctx, alarm.AlarmID, "wall", alarm.Deadline, alarm.Payload); !r.Accepted {
		return fmt.Errorf("the firing was not accepted: %+v", r)
	}
	_, err = app.store.DeleteAlarmCAS(alarm.AlarmID, alarm.Deadline)
	return err
}

// A WAKE LEAVES THE WAIT IT SET FOR THAT WAIT'S OWN WAKE (a review,
// 2026-11-06, on e6f4907d): the wake for moment A names moment B, and B
// comes while A's turn still holds the gate, its wake queued and its alarm
// row gone; A's turn end used to read "timer turn, due, no alarm" as "met
// here" and clear B, and B's queued wake then woke nothing. The opening
// carries the moment its firing consumed; the turn's end meets only that.
func TestAWakeLeavesTheWaitItSetForThatWaitsOwnWake(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	app.executor.Stop()
	fireCtx := goldenTurnCtx(t)
	var mu sync.Mutex
	var sessionID string
	var b int64
	errs := make(chan error, 2)
	fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		mu.Lock()
		defer mu.Unlock()
		switch n {
		case 1: // A's wake names B, due while this turn runs
			b = time.Now().UTC().Add(700 * time.Millisecond).UnixMilli()
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","resumes_on":"time:`+strconv.FormatInt(b, 10)+`"}`)))
		case 2: // B comes: its alarm fires and queues B's wake, before A's turn ends
			time.Sleep(time.Until(time.UnixMilli(b)) + 100*time.Millisecond)
			if err := fireAlarm(app, fireCtx, sessionID); err != nil {
				errs <- err
			}
			_, _ = io.WriteString(w, textReply("noted"))
		default:
			_, _ = io.WriteString(w, textReply("noted"))
		}
	})
	a := time.Now().UTC().Add(300 * time.Millisecond).UnixMilli()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(a, 10))
	mu.Lock()
	sessionID = ws.ID
	mu.Unlock()
	time.Sleep(time.Until(time.UnixMilli(a)) + 100*time.Millisecond)
	if err := fireAlarm(app, fireCtx, ws.ID); err != nil {
		t.Fatal(err)
	}
	wake := func(which string) {
		t.Helper()
		w, err := app.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
		if err != nil || w == nil {
			t.Fatalf("no queued wake for %s: %+v %v", which, w, err)
		}
		if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
			t.Fatalf("the wake for %s: %v", which, err)
		}
		if err := app.store.CompleteWork(w.ID); err != nil { // as the executor does
			t.Fatal(err)
		}
	}
	wake("A")
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	mu.Lock()
	want := "time:" + strconv.FormatInt(b, 10)
	mu.Unlock()
	if on := resumesOnOf(t, app); on != want {
		t.Fatalf("A's turn end did not leave the wait it set for B's wake: %q, want %q", on, want)
	}
	wake("B")
	if on := resumesOnOf(t, app); on != "" {
		t.Fatalf("B's own wake did not meet the wait: %q", on)
	}
}

// A SESSION WITH NO PROMISE IS NAMED WITHOUT ONE. The interrupted-at-shutdown
// line once read "it resumes on … under your promise ." with no promise; the
// clause is omitted where there is no promise, on the boot's line and on a
// delivery's.
func TestASessionWithNoPromiseIsNamedWithoutOne(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_np"), turnSourceOperator)
	if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a task with no promise"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "resumes_on": "word"}); err != nil {
		t.Fatal(err)
	}
	ws, _ := app.store.ActiveWorkSession()
	on, under := app.conditionWords(ws)
	if on != "your operator's next message" || under != "" {
		t.Fatalf("the condition's words: on %q under %q", on, under)
	}
	if line := app.resumesOnLine(ctx, ws); line != "This work resumes on your operator's next message. Deliver, or change resumes_on, to change that." {
		t.Fatalf("the card's line: %q", line)
	}
	app.bootKept = []store.WorkSession{*ws}
	words := app.keptSessionNote()
	if !strings.Contains(words, "it resumes on your operator's next message. Nothing was repeated.") || strings.Contains(words, "under ") {
		t.Fatalf("the shutdown line names a promise that is not there: %q", words)
	}
}
