package app

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// VARIANT 4 AS SEV'S LOG HAS IT (997d47d9, 02:35Z, 2026-11-07; the setup
// as a review read it): the condition is set in a timer
// wake's own turn (the wake for moment A names moment B); an operator's
// message opens a turn before B; B comes while it runs, so the firing is
// queued and the alarm row is gone; THEN, in that same turn, work update
// with state= and standing= and no resumes_on; the turn ends by itself.
// An update that names no resumes_on leaves the condition and its firing
// as they are: B's queued wake resumes the work.
func TestAnUpdateAfterTheFiringIsQueuedOfAMomentSetByAWakeLeavesTheCondition(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	logs := logsink.CaptureForTest(t)
	app.executor.Stop()
	fireCtx := goldenTurnCtx(t)
	var mu sync.Mutex
	var sessionID string
	var b int64
	errs := make(chan error, 2)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		mu.Lock()
		defer mu.Unlock()
		switch n {
		case 1: // A's wake: the identity names B, two seconds ahead
			b = time.Now().UTC().Add(2000 * time.Millisecond).UnixMilli()
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","resumes_on":"time:`+strconv.FormatInt(b, 10)+`"}`)))
		case 2:
			_, _ = io.WriteString(w, textReply("noted"))
		case 3: // the operator's turn: B comes, the firing is queued, the row goes; THEN the update
			time.Sleep(time.Until(time.UnixMilli(b)) + 100*time.Millisecond)
			if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
				if err := fireAlarm(app, fireCtx, sessionID); err != nil {
					errs <- err
				}
			}
			if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
				errs <- errors.New("the alarm row still stands after its firing: the wake was not queued")
			}
			_, _ = io.WriteString(w, callReply("", toolCall("w2", "work", `{"action":"update","state":"997d47d9 deployed; live-checked","standing":"Running the whole build; a78f8695 landed, not yet deployed here"}`)))
		default: // the turn ends by itself
			_, _ = io.WriteString(w, textReply("noted"))
		}
	})
	// A is far enough ahead to stand when it is set: 300 ms was not, in the
	// public tree's gate under the race detector (cut h, 2026-11-08), where
	// setting it took longer and it was refused as already past.
	a := time.Now().UTC().Add(2 * time.Second).UnixMilli()
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
		if err := app.store.CompleteWork(w.ID); err != nil {
			t.Fatal(err)
		}
	}
	wake("A") // sets B in the wake's own turn
	mu.Lock()
	want := "time:" + strconv.FormatInt(b, 10)
	mu.Unlock()
	if on := resumesOnOf(t, app); on != want {
		t.Fatalf("A's wake did not leave B standing: %q", on)
	}
	operatorTurn2(t, app, "Status?")
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	afterTurn := resumesOnOf(t, app)
	if afterTurn != want {
		t.Fatalf("THE UPDATE MOVED THE CONDITION: after an update with no resumes_on, queued firing standing, the condition was %q (B was %q)", afterTurn, want)
	}
	wake("B")
	resumesNow := "Work session " + ws.ID + " resumes now: the moment you named with resumes_on has come."
	refused := strings.Contains(logs.String(), "wakes nothing")
	resumed := sawRequest(obs, resumesNow)()
	afterWake := resumesOnOf(t, app)
	t.Logf("after the operator's turn the condition is %q (set %q); B's queued wake: refused=%v, resumed=%v; the condition now: %q", afterTurn, want, refused, resumed, afterWake)
	if !resumed {
		t.Fatalf("THE WORK IS IDLE: after the operator's turn the condition was %q (B was %q), B's queued wake was refused=%v, and the condition is %q", afterTurn, want, refused, afterWake)
	}
}
