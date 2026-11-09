package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"strconv"
	"time"
)

// A DELIVERY TAKEN IN MEETS EVERY WAIT ON IT, whichever turn took it in
// (a review, 2026-11-08). Two children deliver before the first's harvest
// turn; that turn carries both and marks both harvested. Two sessions wait
// on the second child's delivery. Before, only the first child's waits
// were met, and the second child's wake found nothing to harvest and stood
// down: both sessions waited on a delivery already taken in.
func TestADeliveryTakenInMeetsEveryWaitOnItWhicheverTurnTookItIn(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_two"), turnSourceOperator)
	for _, child := range []string{"ws_a", "ws_b"} {
		cctx := interaction.WithTurn(context.Background(), "turn_"+child)
		if _, err := app.store.EnqueueWork(&store.WorkItem{ID: "sub_" + child, Kind: store.SubagentWorkKind, Payload: "{}", DedupKey: child}); err != nil {
			t.Fatal(err)
		}
		if err := app.store.StartWorkSession(cctx, child, "the task of "+child); err != nil {
			t.Fatal(err)
		}
	}
	for _, ws := range []string{"ws_one", "ws_two"} {
		if err := app.store.StartWorkSession(ctx, ws, "work waiting on b"); err != nil {
			t.Fatal(err)
		}
		on := "delivery:ws_b"
		if err := app.store.SetWorkPromise(ctx, ws, nil, &on, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range []string{"ws_a", "ws_b"} {
		if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_"+child), child, "DELIVERED: "+child+"'s figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
	}
	// a's wake: its turn carries both deliveries.
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_a", "the task of ws_a")
	if left, err := app.store.UnharvestedDeliveries(8); err != nil || len(left) != 0 {
		t.Fatalf("rig: the turn did not take in both deliveries: %d left (%v)", len(left), err)
	}
	// b's wake finds nothing left and stands down; the waits are met anyway.
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_b", "the task of ws_b")
	// Each wait is met in the record. What follows is the continuation's,
	// under the promise's provenance, the hold and the day's ceiling: here
	// the promise was made in no recorded operator's turn, so a session the
	// turn does not carry waits on the operator's word, as it should.
	for _, ws := range []string{"ws_one", "ws_two"} {
		if on := resumesOnOfSession(t, app, ws); on == "delivery:ws_b" {
			t.Errorf("%s still waits on ws_b's delivery after it was taken in", ws)
		}
		var met string
		if err := app.store.DB().QueryRow(`SELECT json_extract(details,'$.work.met') FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.session')=? AND json_extract(details,'$.work.met') IS NOT NULL ORDER BY turn_seq DESC LIMIT 1`, ws).Scan(&met); err != nil || met != "delivery:ws_b" {
			t.Errorf("%s: no record that its wait on ws_b was met: %q %v", ws, met, err)
		}
	}
}

// THE OPERATOR'S OWN TURN TAKES A DELIVERY IN BEFORE ITS WAKE (a review,
// 2026-11-08; the identity: the case where the operator's turn is what meets the wait). The
// turn carries the delivery whole and meets the wait on it; the wake that
// comes after finds nothing to harvest, and the wait is not left standing.
func TestTheOperatorsTurnTakingADeliveryInMeetsTheWaitOnIt(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	ws := sessionUnder(t, app, "delivery:ws_child")
	if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_child"), "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	ctx := goldenTurnCtx(t)
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.observeChat(ctx, "how is it going?", func(kind, name, args string) {}); err != nil {
		t.Fatal(err)
	}
	if left, err := app.store.UnharvestedDeliveries(8); err != nil || len(left) != 0 {
		t.Fatalf("rig: the operator's turn did not take the delivery in: %d left (%v)", len(left), err)
	}
	if on := resumesOnOfSession(t, app, ws.ID); on == "delivery:ws_child" {
		t.Fatalf("the wait still stands on a delivery the operator's turn took in")
	}
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
	if on := resumesOnOfSession(t, app, ws.ID); on == "delivery:ws_child" {
		t.Fatalf("after the wake the wait stands on a delivery already taken in")
	}
}

// A HARVEST THAT TAKES NOTHING IN MEETS NOTHING (a review, 2026-11-08): the
// wake named its waiting session before its turn and used to meet the wait
// there. Now, a harvest whose model call fails leaves the wait and the
// report as they were; one whose turn leaves the triggering child's report
// out (the ninth of nine, past the eight a turn composes) leaves its wait
// standing while the others it carried are met.
func TestAHarvestThatTakesNothingInMeetsNothing(t *testing.T) {
	t.Run("the model call fails", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		fixtureModel(t, app, func(n int, w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"refused"}}`)
		})
		app.executor.Stop()
		ws := sessionUnder(t, app, "delivery:ws_child")
		if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_child"), "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
		if on := resumesOnOfSession(t, app, ws.ID); on != "delivery:ws_child" {
			t.Fatalf("a harvest whose model call failed met the wait: %q", on)
		}
		if left, err := app.store.UnharvestedDeliveries(8); err != nil || len(left) != 1 {
			t.Fatalf("the report was not left to be read: %d (%v)", len(left), err)
		}
	})
	t.Run("the triggering report is left out", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		for i := 1; i <= 8; i++ {
			subagentChild(t, app, fmt.Sprintf("ws_c%d", i))
		}
		ws := sessionUnder(t, app, "delivery:ws_c9")
		for i := 1; i <= 9; i++ {
			id := fmt.Sprintf("ws_c%d", i)
			if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_"+id), id, "DELIVERED: "+id, store.EvidenceWorkerReportOnly, ""); err != nil {
				t.Fatal(err)
			}
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_c9", "the ninth")
		if on := resumesOnOfSession(t, app, ws.ID); on != "delivery:ws_c9" {
			t.Fatalf("a harvest that left the triggering report out met its wait: %q", on)
		}
		left, err := app.store.UnharvestedDeliveries(8)
		if err != nil || len(left) != 1 || left[0].ID != "ws_c9" {
			t.Fatalf("want ws_c9 alone left to be read: %+v (%v)", left, err)
		}
	})
}

// A FAILED WAIT WRITE STRANDS NOTHING (a review, 2026-11-08). Two sessions wait
// on b; a's harvest turn carries b too, and the write of ws_two's wait is
// made to fail. Before, b was marked taken in anyway, b's own wake then found
// nothing to harvest, and ws_two waited until a restart. Now nothing of b is
// applied: once the fault is gone, b's wake takes it in and both waits are
// met, with no restart.
func TestAFailedWaitWriteStrandsNothing(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_two"), turnSourceOperator)
	subagentChild(t, app, "ws_a")
	subagentChild(t, app, "ws_b")
	for _, ws := range []string{"ws_one", "ws_two"} {
		if err := app.store.StartWorkSession(ctx, ws, "work waiting on b"); err != nil {
			t.Fatal(err)
		}
		on := "delivery:ws_b"
		if err := app.store.SetWorkPromise(ctx, ws, nil, &on, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range []string{"ws_a", "ws_b"} {
		if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_"+child), child, "DELIVERED: "+child, store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.store.DB().Exec(`CREATE TRIGGER fail_two BEFORE UPDATE OF resumes_on ON work_sessions WHEN OLD.id = 'ws_two' BEGIN SELECT RAISE(ABORT, 'injected fault'); END`); err != nil {
		t.Fatal(err)
	}
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_a", "the task of ws_a")
	if _, err := app.store.DB().Exec(`DROP TRIGGER fail_two`); err != nil {
		t.Fatal(err)
	}
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_b", "the task of ws_b")
	for _, ws := range []string{"ws_one", "ws_two"} {
		if on := resumesOnOfSession(t, app, ws); on == "delivery:ws_b" {
			t.Errorf("%s is stranded on ws_b's delivery after the fault cleared", ws)
		}
	}
}

// AN UNREADABLE WAIT IS NOT "NO WAIT" (a review, 2026-11-08). Under the
// operator's hold, a delivered report has a session waiting on it, and the
// read of the waiting sessions fails at the wake. Before, the failed read
// was taken for "no promise waits", and the harvest ran past the hold. Now
// it asks the model nothing and leaves the report to be taken in.
func TestAnUnreadableWaitDoesNotRunPastTheHold(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	ws := sessionUnder(t, app, "delivery:ws_child")
	pressStage(t, app, store.StageSupervised)
	if err := app.store.DeliverWorkSession(interaction.WithTurn(context.Background(), "turn_child"), "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	// The read of the kept sessions asks which are sub-agents' by the work
	// queue; without it, that read fails. The hold's own decision says held:
	// before, it said "no promise waits" and let the harvest go on.
	if _, err := app.store.DB().Exec(`ALTER TABLE work_queue RENAME TO work_queue_away`); err != nil {
		t.Fatal(err)
	}
	if held, _ := app.deliveryHeld("ws_child"); !held {
		t.Errorf("with the wait unreadable, the hold check let the harvest go on")
	}
	app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
	if _, err := app.store.DB().Exec(`ALTER TABLE work_queue_away RENAME TO work_queue`); err != nil {
		t.Fatal(err)
	}
	if n := len(obs.snapshot()); n != 0 {
		t.Fatalf("with the wait unreadable, the harvest asked the model %d time(s) under the operator's hold", n)
	}
	if on := resumesOnOfSession(t, app, ws.ID); on != "delivery:ws_child" {
		t.Fatalf("the wait changed: %q", on)
	}
	if left, err := app.store.UnharvestedDeliveries(8); err != nil || len(left) != 1 {
		t.Fatalf("the report is not left to be taken in: %d (%v)", len(left), err)
	}
}

// A TIMER WAKE WHOSE SESSION CANNOT BE READ IS RETRIED, NOT FINISHED (a review,
// 2026-11-08). The moment comes, the alarm is retired and its wake queued;
// the first attempt cannot read the kept sessions. Before, that was taken
// for a stale firing: the wake returned no error, the queue finished it,
// and the session's moment never resumed it. Now the attempt fails, the
// model is not asked, and the retry, with the read back, resumes it.
func TestATimerWakeWhoseSessionCannotBeReadIsRetried(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
	app.executor.Stop()
	at := time.Now().UTC().Add(700 * time.Millisecond).UnixMilli()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(at, 10))
	time.Sleep(time.Until(time.UnixMilli(at)) + 100*time.Millisecond)
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
	w, err := app.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
	if err != nil || w == nil {
		t.Fatalf("no queued wake: %+v %v", w, err)
	}
	if _, err := app.store.DB().Exec(`ALTER TABLE work_queue RENAME TO work_queue_away`); err != nil {
		t.Fatal(err)
	}
	first := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w)
	if _, err := app.store.DB().Exec(`ALTER TABLE work_queue_away RENAME TO work_queue`); err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("a wake whose session could not be read was finished as done; the queue would not retry it")
	}
	if n := len(obs.snapshot()); n != 0 {
		t.Fatalf("the model was asked %d time(s) though the session could not be read", n)
	}
	if on := resumesOnOfSession(t, app, ws.ID); on != "time:"+strconv.FormatInt(at, 10) {
		t.Fatalf("the failed attempt changed the condition: %q", on)
	}
	if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
		t.Fatalf("the retry, with the read back: %v", err)
	}
	if !sawRequest(obs, "Work session "+ws.ID+" resumes now: the moment you named with resumes_on has come.")() {
		t.Fatal("the retry did not resume the session")
	}
}
