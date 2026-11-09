package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// restart_test.go — the two restart receipts a review asked for (2026-11-05):
// a promise's pending condition survives a restart on the same session,
// and a due `now` runs exactly once across it, counted in the record and
// at the model, with a second chance to pick it up taking nothing up.

// restartedIdentity stops app and boots the same identity again, from the
// same paths, as a process restart does.
func restartedIdentity(t *testing.T, app *App) *App {
	t.Helper()
	cfg := app.configSnapshot()
	stopApp(t, app)
	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatalf("the second boot: %v", err)
	}
	return next
}

// freshTurnsFor counts the fresh turns the record holds for the session:
// the facts of continuation legs, by the words that opened them.
func freshTurnsFor(t *testing.T, a *App, session string) int {
	t.Helper()
	var n int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations c JOIN turn_annotations a ON a.turn_seq = c.turn_seq AND a.kind = 'turn.source' AND a.key = 'continuation'
		WHERE json_extract(a.payload, '$.session') = ?`, session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAPromiseAndItsNowSurviveARestartAndTheNowRunsOnce(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("leg done"))
	}))
	defer srv.Close()
	t.Run("a pending condition survives the restart on the same session", func(t *testing.T) {
		app, _ := liveIdentity(t, t.TempDir(), srv.URL)
		pressStage(t, app, store.StageTrusted)
		ctx := interaction.WithTurn(context.Background(), "turn_before")
		if err := app.store.StartWorkSession(ctx, "ws_kept", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		promise, on := "in_1", "word"
		if err := app.store.SetWorkPromise(ctx, "ws_kept", &promise, &on, ""); err != nil {
			t.Fatal(err)
		}
		next := restartedIdentity(t, app)
		defer stopApp(t, next)
		ws, err := next.store.ActiveWorkSession()
		if err != nil || ws == nil || ws.ID != "ws_kept" || ws.Status != "active" {
			t.Fatalf("after the restart the session is not the same open session: %+v %v", ws, err)
		}
		if ws.Promise != "in_1" || ws.ResumesOn != "word" || ws.PromisedTurn != "turn_before" {
			t.Fatalf("after the restart the condition is not the same pending one: %+v", ws)
		}
		if len(next.bootSwept) != 0 {
			t.Fatalf("the boot sweep closed something: %+v", next.bootSwept)
		}
		if len(next.bootKept) != 1 || next.bootKept[0].ID != "ws_kept" {
			t.Fatalf("the boot did not keep the session for its line: %+v", next.bootKept)
		}
	})
	t.Run("a due now runs once, and a second chance takes nothing up", func(t *testing.T) {
		requests.Store(0)
		app, _ := liveIdentity(t, t.TempDir(), srv.URL)
		pressStage(t, app, store.StageTrusted)
		ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
		if err := app.store.StartWorkSession(ctx, "ws_now", "keep a promise"); err != nil {
			t.Fatal(err)
		}
		promise, on := "in_1", "now"
		if err := app.store.SetWorkPromise(ctx, "ws_now", &promise, &on, "The turn ended at a checkpoint under in_1; a fresh turn follows"); err != nil {
			t.Fatal(err)
		}
		// Killed before the fresh turn: nothing of it ran.
		if n := freshTurnsFor(t, app, "ws_now"); n != 0 {
			t.Fatalf("a fresh turn before the restart: %d", n)
		}
		next := restartedIdentity(t, app)
		defer stopApp(t, next)
		// Recovery: the one fresh turn, in the record and at the model.
		waitUntil(t, func() bool { return freshTurnsFor(t, next, "ws_now") == 1 }, "the fresh turn after the restart")
		waitUntil(t, func() bool { return resumesOnOf(t, next) == "word" }, "the fresh turn ending by itself")
		if n := requests.Load(); n != 1 {
			t.Fatalf("the model was asked %d times for the one now, want 1", n)
		}
		var fact string
		_ = next.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='system' AND content LIKE '%Work session ws_now%' ORDER BY turn_seq DESC LIMIT 1`).Scan(&fact)
		if !strings.Contains(fact, "the identity was restarted before this fresh turn ran") {
			t.Fatalf("the fresh turn's fact does not say it comes after a restart: %q", fact)
		}
		// A second chance to pick the same now up: at the tick, and at a
		// second restart. Nothing runs twice.
		next.releaseHeldPromise(releasedAtTick)
		time.Sleep(300 * time.Millisecond)
		again := restartedIdentity(t, next)
		defer stopApp(t, again)
		time.Sleep(500 * time.Millisecond)
		if n := freshTurnsFor(t, again, "ws_now"); n != 1 {
			t.Fatalf("the now ran %d times after a second chance, want 1", n)
		}
		if n := requests.Load(); n != 1 {
			t.Fatalf("the model was asked %d times after a second chance, want 1", n)
		}
		if on := resumesOnOf(t, again); on != "word" {
			t.Fatalf("after the second restart the condition is %q, want the operator's word", on)
		}
		// Killed mid-now: the fresh turn had opened (its fact is in the
		// record) and the process died before it ended. A restart repeats
		// nothing: the condition becomes the operator's word with the reason.
		ctx2 := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped_2"), turnSourceOperator)
		if err := again.store.SetWorkPromise(ctx2, "ws_now", nil, &on, "The turn ended at a checkpoint under in_1; a fresh turn follows"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_leg_cut"), turnSourceContinuation), "ws_now")
		if _, err := again.store.RecordRelatedConversation(leg, string(interaction.System), "[budget checkpoint — continuation 2] Work session ws_now is still active.", "turn_leg_cut", "", interaction.Details{}); err != nil {
			t.Fatal(err)
		}
		before := freshTurnsFor(t, again, "ws_now")
		third := restartedIdentity(t, again)
		defer stopApp(t, third)
		time.Sleep(500 * time.Millisecond)
		if n := freshTurnsFor(t, third, "ws_now"); n != before {
			t.Fatalf("a now cut mid-turn ran again after the restart: %d fresh turns, had %d", n, before)
		}
		if n := requests.Load(); n != 1 {
			t.Fatalf("the model was asked %d times after the cut now's restart, want 1", n)
		}
		if on := resumesOnOf(t, third); on != "word" {
			t.Fatalf("the cut now's condition after the restart is %q, want the operator's word", on)
		}
		if r := promiseNotice(t, third); !strings.Contains(r, "interrupted (the identity restarted); nothing was repeated") {
			t.Fatalf("the record does not say why: %q", r)
		}
	})
}

// TWO SESSIONS ACROSS A RESTART: the older has a due now, the newer waits
// for the operator's word, and no word comes. The older now runs once,
// attributed to its session, and stays at one across a second restart; the
// newer is dispatched never. Counts before the restart, after recovery and
// after the second restart, each recovery allowed to finish.
func TestTwoSessionsAcrossARestartTheOlderNowRunsOnceTheNewerWaits(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("leg done"))
	}))
	defer srv.Close()
	app, _ := liveIdentity(t, t.TempDir(), srv.URL)
	pressStage(t, app, store.StageTrusted)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped"), turnSourceOperator)
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
	counts := func(a *App, when string) {
		t.Helper()
		t.Logf("%s: ws_older fresh turns %d, ws_newer fresh turns %d, model requests %d", when, freshTurnsFor(t, a, "ws_older"), freshTurnsFor(t, a, "ws_newer"), requests.Load())
	}
	counts(app, "before the restart")
	if freshTurnsFor(t, app, "ws_older") != 0 || freshTurnsFor(t, app, "ws_newer") != 0 || requests.Load() != 0 {
		t.Fatal("something ran before the restart")
	}
	next := restartedIdentity(t, app)
	defer stopApp(t, next)
	waitUntil(t, func() bool { return freshTurnsFor(t, next, "ws_older") == 1 }, "the older now's fresh turn after the restart")
	waitUntil(t, func() bool {
		ws, _ := next.store.WorkSessionByID("ws_older")
		return ws != nil && ws.ResumesOn == "word"
	}, "that fresh turn ending by itself")
	time.Sleep(500 * time.Millisecond) // a late replay would land here
	counts(next, "after recovery")
	if freshTurnsFor(t, next, "ws_older") != 1 || requests.Load() != 1 || freshTurnsFor(t, next, "ws_newer") != 0 {
		t.Fatalf("after recovery: older %d, newer %d, requests %d; want 1, 0, 1", freshTurnsFor(t, next, "ws_older"), freshTurnsFor(t, next, "ws_newer"), requests.Load())
	}
	var fact string
	_ = next.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='system' AND instr(content, ?) > 0 ORDER BY turn_seq DESC LIMIT 1`, store.ContinuationFactNames("ws_older")).Scan(&fact)
	if !strings.Contains(fact, "restarted before this fresh turn ran") {
		t.Fatalf("the fresh turn is not attributed to the older session as a restart's: %q", fact)
	}
	if ws, _ := next.store.WorkSessionByID("ws_newer"); ws == nil || ws.ResumesOn != "word" || ws.Status != "active" {
		t.Fatalf("the newer session no longer waits for the word: %+v", ws)
	}
	again := restartedIdentity(t, next)
	defer stopApp(t, again)
	time.Sleep(700 * time.Millisecond) // recovery, and a late replay, would land here
	counts(again, "after the second restart")
	if freshTurnsFor(t, again, "ws_older") != 1 || requests.Load() != 1 || freshTurnsFor(t, again, "ws_newer") != 0 {
		t.Fatalf("after the second restart: older %d, newer %d, requests %d; want 1, 0, 1", freshTurnsFor(t, again, "ws_older"), freshTurnsFor(t, again, "ws_newer"), requests.Load())
	}
	if ws, _ := again.store.WorkSessionByID("ws_newer"); ws == nil || ws.ResumesOn != "word" {
		t.Fatalf("the newer session after the second restart: %+v", ws)
	}
	// The replay case: the older session's now set again and its fresh turn
	// opened (its fact in the record) when the process is cut; a third
	// restart repeats nothing, and the newer still waits.
	ctx3 := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_capped_3"), turnSourceOperator)
	if err := again.store.SetWorkPromise(ctx3, "ws_older", nil, &now, "The turn ended at a checkpoint under in_1; a fresh turn follows"); err != nil {
		t.Fatal(err)
	}
	cut := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_leg_cut"), turnSourceContinuation), "ws_older")
	if _, err := again.store.RecordRelatedConversation(cut, string(interaction.System), "[budget checkpoint — continuation 2] "+store.ContinuationFactNames("ws_older")+"is still active.", "turn_leg_cut", "", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	before := freshTurnsFor(t, again, "ws_older")
	third := restartedIdentity(t, again)
	defer stopApp(t, third)
	time.Sleep(700 * time.Millisecond)
	counts(third, "after the third restart, the now cut mid-turn")
	if freshTurnsFor(t, third, "ws_older") != before || requests.Load() != 1 || freshTurnsFor(t, third, "ws_newer") != 0 {
		t.Fatalf("a now cut mid-turn was replayed: older %d (had %d), newer %d, requests %d", freshTurnsFor(t, third, "ws_older"), before, freshTurnsFor(t, third, "ws_newer"), requests.Load())
	}
	if on := resumesOnOfSession(t, third, "ws_older"); on != "word" {
		t.Fatalf("the cut now's condition after the restart is %q, want the operator's word", on)
	}
}

func resumesOnOfSession(t *testing.T, a *App, id string) string {
	t.Helper()
	ws, err := a.store.WorkSessionByID(id)
	if err != nil || ws == nil {
		t.Fatalf("session %s: %v", id, err)
	}
	return ws.ResumesOn
}
