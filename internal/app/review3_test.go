package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// review3_test.go — a review's findings on the second freeze (2026-11-06): a
// resumed older session's turn acts on its own session in the primary
// seat; a queued leg stands down when its promise is gone; an arrival-made
// promise's alarm and an awaited delivery are checked against their owner;
// a stale promise alarm is cancelled at boot; the floor's citation.

func twoSessions(t *testing.T, app *App, olderOn, newerOn string) {
	t.Helper()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_two"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_older", "the older work"); err != nil {
		t.Fatal(err)
	}
	p1 := "in_1"
	for _, on := range []string{olderOn, newerOn} {
		if child, ok := strings.CutPrefix(on, "delivery:"); ok {
			subagentChild(t, app, child)
		}
	}
	if err := app.store.SetWorkPromise(ctx, "ws_older", &p1, &olderOn, "the older's condition"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.StartWorkSession(ctx, "ws_newer", "the newer work"); err != nil {
		t.Fatal(err)
	}
	p2 := "in_2"
	if err := app.store.SetWorkPromise(ctx, "ws_newer", &p2, &newerOn, "the newer's condition"); err != nil {
		t.Fatal(err)
	}
}

func TestAResumedOlderSessionsTurnActsOnItsOwnSessionInThePrimarySeat(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, _ = io.WriteString(w, callReply("",
				toolCall("u1", "work", `{"action":"update","next_move":"the older's next step"}`),
				toolCall("y1", "work", `{"action":"yield","resumes_on":"word"}`)))
			return
		}
		_, _ = io.WriteString(w, textReply("leg done"))
	})
	twoSessions(t, app, "now", "word")
	app.releaseHeldPromise(releasedAtTick)
	// The update and the yield are two calls of one reply; the record is
	// read once both have landed (the yield's condition is the later).
	waitUntil(t, func() bool {
		ws, _ := app.store.WorkSessionByID("ws_older")
		return ws != nil && ws.NextMove == "the older's next step" && ws.ResumesOn == "word"
	}, "the older session updated and yielded by its own fresh turn")
	newer, _ := app.store.WorkSessionByID("ws_newer")
	if newer.NextMove != "" || newer.ResumesOn != "word" {
		t.Fatalf("the newer session was touched by the older's turn: %+v", newer)
	}
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "yield is the resident's turn control and you are a sub-agent") {
			t.Fatal("the resumed turn ran in a worker seat")
		}
	}
}

func TestAQueuedLegStandsDownWhenItsPromiseIsGone(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_leg", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	promise, now := "in_1", "now"
	if err := app.store.SetWorkPromise(ctx, "ws_leg", &promise, &now, "a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	// The promise cleared while the leg waited for the gate.
	none := ""
	if err := app.store.SetWorkPromise(ctx, "ws_leg", &none, &none, "the promise ended"); err != nil {
		t.Fatal(err)
	}
	app.addQueuedLegs(1)
	app.continueCappedTurn("ws_leg", 1, capDeclared, "turn_capped", "in_1" != "", "in_1")
	if len(obs.snapshot()) != 0 {
		t.Fatal("a leg ran as ordinary work after its promise was gone")
	}
}

func TestAnArrivalMadePromisesAlarmAndAnAwaitedDeliveryAreCheckedAgainstTheirOwner(t *testing.T) {
	t.Run("the alarm of a promise an arrival's turn made wakes nothing", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		arrival := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_arrival"), turnSourceArrival)
		if err := app.store.StartWorkSession(arrival, "ws_arr", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Add(-time.Second).UnixMilli()
		promise, on := "in_1", "time:"+strconv.FormatInt(at, 10)
		if err := app.store.SetWorkPromise(arrival, "ws_arr", &promise, &on, "set in an arrival's turn"); err != nil {
			t.Fatal(err)
		}
		firing := identity.TimerWake{AlarmID: identity.PromiseAlarmID("ws_arr"), Deadline: at, Tag: identity.PromiseAlarmTag, Message: "the moment", SetIn: turnSourceArrival}
		raw, _ := json.Marshal(firing)
		w := &store.WorkItem{ID: "wake_" + firing.DeliveryID(), Kind: identity.TimerWakeWorkKind, Payload: string(raw), DedupKey: firing.AlarmID + "@1"}
		if _, err := app.store.AddOutboxNoticeWithWork(firing.DeliveryID(), "[timer fired] the moment", w); err != nil {
			t.Fatal(err)
		}
		if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
			t.Fatal(err)
		}
		if len(obs.snapshot()) != 0 {
			t.Fatal("the alarm of an arrival-made promise woke the identity")
		}
		if on := resumesOnOfSession(t, app, "ws_arr"); on != "word" {
			t.Fatalf("the condition after the arrival-made alarm is %q, want the operator's word", on)
		}
	})
	t.Run("a delivery an older session awaits meets its condition under a newer word waiter", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageTrusted)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		twoSessions(t, app, "delivery:ws_child", "word")
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.DeliverWorkSession(ctx, "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
		if !sawRequest(obs, "This delivery is what your work on ws_older waited for")() {
			t.Fatal("the harvest did not name the older session's promise")
		}
		if on := resumesOnOfSession(t, app, "ws_older"); on != "" && on != "now" {
			t.Fatalf("the older session's condition was not met: %q", on)
		}
		if on := resumesOnOfSession(t, app, "ws_newer"); on != "word" {
			t.Fatalf("the newer session was touched: %q", on)
		}
	})
	t.Run("a delivery an older session awaits is held under supervision", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		pressStage(t, app, store.StageSupervised)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("noted")) })
		app.executor.Stop()
		twoSessions(t, app, "delivery:ws_child", "word")
		ctx := interaction.WithTurn(context.Background(), "turn_child")
		if err := app.store.DeliverWorkSession(ctx, "ws_child", "DELIVERED: the figures", store.EvidenceWorkerReportOnly, ""); err != nil {
			t.Fatal(err)
		}
		app.wakeSubagentDelivery(goldenTurnCtx(t), "ws_child", "find the figures")
		if len(obs.snapshot()) != 0 {
			t.Fatal("an awaited delivery woke the identity under the hold")
		}
		if on := resumesOnOfSession(t, app, "ws_older"); on != "word" {
			t.Fatalf("the held delivery's condition is %q, want the operator's word", on)
		}
	})
}

func TestAStalePromiseAlarmIsCancelledAtBoot(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	at := time.Now().Add(time.Hour).UnixMilli()
	if err := app.store.SetAlarm(identity.PromiseAlarmID("ws_gone"), "timers", "wall", at, nil, identity.PromiseAlarmPayload("ws_gone", "")); err != nil {
		t.Fatal(err)
	}
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_kept", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	promise, on := "in_1", "time:"+strconv.FormatInt(at, 10)
	if err := app.store.SetWorkPromise(ctx, "ws_kept", &promise, &on, ""); err != nil {
		t.Fatal(err)
	}
	_ = app.store.CancelAlarm("timers", identity.PromiseAlarmID("ws_kept"))
	app.promiseConditionsAtBoot()
	if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID("ws_gone")); found {
		t.Fatal("a promise alarm no session's condition owns survived the boot")
	}
	alarm, found, _ := app.store.AlarmByID(identity.PromiseAlarmID("ws_kept"))
	if !found || alarm.Deadline != at {
		t.Fatalf("the kept condition's alarm was not rebuilt: %+v %v", alarm, found)
	}
	if _, msg := identity.DecodeTimerPayload(alarm.Payload); !strings.Contains(msg, "ws_kept") {
		t.Fatalf("the rebuilt alarm's payload: %q", alarm.Payload)
	}
	var setIn string
	_ = json.Unmarshal([]byte(alarm.Payload), &struct {
		SetIn *string `json:"set_in"`
	}{&setIn})
	if setIn != turnSourceOperator {
		t.Fatalf("the rebuilt alarm lost the setting turn's provenance: %q", setIn)
	}
}

// probeArgs is a read of the probe file whose arguments differ per call
// (limit n+1): the record's stuck detector counts identical calls with
// identical results, and a fixture that re-reads one file is not stuck.
func probeArgs(dir string, n int) string {
	b, _ := json.Marshal(map[string]any{"file_path": filepath.Join(dir, "probe.txt"), "limit": n + 1})
	return string(b)
}
