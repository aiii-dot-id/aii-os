package app

import (
	"context"
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

// review2_test.go — a review's findings on the resident freeze (2026-11-05):
// a leg under a promise reads its condition again with the gate held; a
// `now` on a session that is not the active one is said, not starved; a
// promise an arrival's turn made never resumes unattended; the condition's
// alarm is made to agree with the record at boot.

func TestALegUnderAPromiseReadsAgainBeforeItBegins(t *testing.T) {
	t.Run("the operator's hold since it was queued", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
		ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
		if err := app.store.StartWorkSession(ctx, "ws_leg", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		promise, now := "in_1", "now"
		if err := app.store.SetWorkPromise(ctx, "ws_leg", &promise, &now, "The turn ended at a checkpoint under in_1; a fresh turn follows"); err != nil {
			t.Fatal(err)
		}
		pressStage(t, app, store.StageSupervised) // pressed after the leg was decided, before it took the gate
		app.addQueuedLegs(1)
		app.continueCappedTurn("ws_leg", 1, capDeclared, "turn_capped", "in_1" != "", "in_1")
		if len(obs.snapshot()) != 0 {
			t.Fatal("a leg ran under a hold pressed after it was queued")
		}
		if on := resumesOnOf(t, app); on != "word" {
			t.Fatalf("the held leg's condition is %q, want the operator's word", on)
		}
		if n := heldNotice(t, app); !strings.Contains(n, "It waits for your word.") {
			t.Fatalf("the operator was not told: %q", n)
		}
	})
	t.Run("a condition that changed since it was queued", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
		ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
		if err := app.store.StartWorkSession(ctx, "ws_leg", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		promise, word := "in_1", "word"
		if err := app.store.SetWorkPromise(ctx, "ws_leg", &promise, &word, ""); err != nil {
			t.Fatal(err)
		}
		app.addQueuedLegs(1)
		app.continueCappedTurn("ws_leg", 1, capDeclared, "turn_capped", "in_1" != "", "in_1")
		if len(obs.snapshot()) != 0 {
			t.Fatal("a leg ran on a condition that was no longer now")
		}
	})
}

func TestAnOlderSessionsNowRunsOnItsOwnSessionTheNewerWaits(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_older"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_older", "the older work"); err != nil {
		t.Fatal(err)
	}
	promise, now := "in_1", "now"
	if err := app.store.SetWorkPromise(ctx, "ws_older", &promise, &now, "The turn ended at a checkpoint under in_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.StartWorkSession(ctx, "ws_newer", "the newer work"); err != nil {
		t.Fatal(err)
	}
	newer, word := "in_2", "word"
	if err := app.store.SetWorkPromise(ctx, "ws_newer", &newer, &word, ""); err != nil {
		t.Fatal(err)
	}
	if active, _ := app.store.ActiveWorkSession(); active == nil || active.ID != "ws_newer" {
		t.Fatalf("the active session: %+v", active)
	}
	app.releaseHeldPromise(releasedAtTick)
	waitUntil(t, func() bool { return freshTurnsFor(t, app, "ws_older") == 1 }, "the older session's fresh turn")
	waitUntil(t, func() bool {
		ws, _ := app.store.WorkSessionByID("ws_older")
		return ws != nil && ws.ResumesOn == "word"
	}, "the older session's fresh turn ending by itself")
	if n := len(obs.snapshot()); n != 1 {
		t.Fatalf("the model was asked %d times, want 1", n)
	}
	if !strings.Contains(requestText(t, obs.snapshot()[0]), store.ContinuationFactNames("ws_older")) {
		t.Fatal("the fresh turn was not attributed to the older session")
	}
	if n := freshTurnsFor(t, app, "ws_newer"); n != 0 {
		t.Fatalf("the newer session was dispatched %d times, want 0", n)
	}
	if ws, _ := app.store.WorkSessionByID("ws_newer"); ws == nil || ws.ResumesOn != "word" {
		t.Fatalf("the newer session no longer waits for the word: %+v", ws)
	}
}

func TestAPromiseAnArrivalsTurnMadeNeverResumesUnattended(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("leg done")) })
	arrival := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_arrival"), turnSourceArrival)
	if err := app.store.StartWorkSession(arrival, "ws_arr", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	promise, now := "in_1", "now"
	if err := app.store.SetWorkPromise(arrival, "ws_arr", &promise, &now, "set in an arrival's turn"); err != nil {
		t.Fatal(err)
	}
	app.releaseHeldPromise(releasedAtRestart)
	time.Sleep(300 * time.Millisecond)
	if len(obs.snapshot()) != 0 {
		t.Fatal("a promise an arrival's turn made resumed unattended")
	}
	if on := resumesOnOf(t, app); on != "word" {
		t.Fatalf("the arrival-made promise's condition is %q, want the operator's word", on)
	}
	if n := promiseNotice(t, app); !strings.Contains(n, "was an arrival's") {
		t.Fatalf("the record does not say why: %q", n)
	}
	// A promise whose turn the record does not know is treated the same.
	unknown := interaction.WithTurn(context.Background(), "turn_nowhere")
	if err := app.store.StartWorkSession(unknown, "ws_unk", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetWorkPromise(unknown, "ws_unk", &promise, &now, "set in a turn the record does not hold"); err != nil {
		t.Fatal(err)
	}
	app.releaseHeldPromise(releasedAtRestart)
	time.Sleep(300 * time.Millisecond)
	if len(obs.snapshot()) != 0 || resumesOnOf(t, app) != "word" {
		t.Fatal("a promise from an unknown turn resumed unattended")
	}
}

func TestTheConditionsAlarmAgreesWithTheRecordAtBoot(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_set"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_time", "keep a promise"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour).UnixMilli()
	promise, on := "in_1", "time:"+strconv.FormatInt(at, 10)
	if err := app.store.SetWorkPromise(ctx, "ws_time", &promise, &on, ""); err != nil {
		t.Fatal(err)
	}
	// The cut between the condition's write and the alarm's: no alarm.
	_ = app.store.CancelAlarm("timers", identity.PromiseAlarmID("ws_time"))
	app.promiseConditionsAtBoot()
	alarm, found, err := app.store.AlarmByID(identity.PromiseAlarmID("ws_time"))
	if err != nil || !found || alarm.Deadline != at {
		t.Fatalf("the alarm was not made to agree with the record: %+v %v %v", alarm, found, err)
	}
	// The other way: a condition no longer a time, an alarm left behind.
	word := "word"
	if err := app.store.SetWorkPromise(ctx, "ws_time", nil, &word, ""); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetAlarm(identity.PromiseAlarmID("ws_time"), "timers", "wall", at, nil, identity.PromiseAlarmPayload("ws_time", "")); err != nil {
		t.Fatal(err)
	}
	app.promiseConditionsAtBoot()
	if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID("ws_time")); found {
		t.Fatal("an alarm outlived its time condition across the boot")
	}
	if operatorProvenance(turnSourceVoice) {
		t.Fatal("a voice turn reads as the operator's own provenance")
	}
}
