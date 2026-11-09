package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// VARIANT 3: a timer
// wake's own turn sets time:T; the identity stops with its own stop and
// boots again, re-arming the kept session's alarm; an operator message
// opens a turn before T; T comes during it; the only work call is an
// update with no resumes_on; the turn ends by itself. Then the queued
// wake must meet the condition, or the work must continue — never idle
// with a condition no alarm serves.
func TestARestartBetweenTheWakeThatSetTheMomentAndAnOperatorsTurnDoesNotIdleTheWork(t *testing.T) {
	var mu sync.Mutex
	var requests int
	var tAt int64
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests++
		k := requests
		prompts = append(prompts, string(body))
		if k == 1 {
			tAt = time.Now().UTC().Add(9 * time.Second).UnixMilli()
		}
		at := tAt
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch k {
		case 1: // A's wake: the identity names T, nine seconds ahead (room for the restart)
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","resumes_on":"time:`+strconv.FormatInt(at, 10)+`"}`)))
		case 3: // the operator's turn after the restart: an update of the focus, no resumes_on
			_, _ = io.WriteString(w, callReply("", toolCall("w2", "work", `{"action":"update","focus":"after the restart"}`)))
		case 4: // T comes while the operator's turn runs
			time.Sleep(time.Until(time.UnixMilli(at)) + 200*time.Millisecond)
			_, _ = io.WriteString(w, textReply("noted"))
		default:
			_, _ = io.WriteString(w, textReply("noted"))
		}
	}))
	defer srv.Close()
	app, _ := liveIdentity(t, t.TempDir(), srv.URL)
	pressStage(t, app, store.StageTrusted)
	a := time.Now().UTC().Add(400 * time.Millisecond).UnixMilli()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(a, 10))
	// A's own wake runs in the identity's own time and sets T.
	waitUntil(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return tAt > 0 && resumesOnOfSession(t, app, ws.ID) == "time:"+strconv.FormatInt(tAt, 10)
	}, "A's wake to set T")
	mu.Lock()
	want := "time:" + strconv.FormatInt(tAt, 10)
	mu.Unlock()
	// The restart: the kept session's alarm is re-armed.
	next := restartedIdentity(t, app)
	defer stopApp(t, next)
	afterBoot := resumesOnOfSession(t, next, ws.ID)
	_, armed, err := next.store.AlarmByID(identity.PromiseAlarmID(ws.ID))
	if err != nil {
		t.Fatal(err)
	}
	logs := logsink.CaptureForTest(t)
	operatorTurn2(t, next, "Status?")
	afterTurn := resumesOnOfSession(t, next, ws.ID)
	resumesNow := "Work session " + ws.ID + " resumes now: the moment you named with resumes_on has come."
	seen := func(marker string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range prompts {
			if strings.Contains(p, marker) {
				return true
			}
		}
		return false
	}
	waitUntil(t, func() bool {
		return strings.Contains(logs.String(), "wakes nothing") || seen(resumesNow) || strings.Contains(logs.String(), "continuation.start")
	}, "the queued wake's outcome, or a continuation")
	time.Sleep(2 * time.Second)
	refused := strings.Contains(logs.String(), "wakes nothing")
	resumed := seen(resumesNow)
	continued := strings.Contains(logs.String(), "continuation.start")
	afterWake := resumesOnOfSession(t, next, ws.ID)
	t.Logf("T %q; after the boot the condition is %q (alarm armed %v); after the operator's turn %q; the queued wake: refused=%v, resumed=%v; a continuation followed: %v; the condition now: %q", want, afterBoot, armed, afterTurn, refused, resumed, continued, afterWake)
	if !resumed && !continued {
		t.Fatalf("THE WORK IS IDLE after the restart: condition after boot %q (armed %v), after the operator's turn %q, wake refused=%v, now %q", afterBoot, armed, afterTurn, refused, afterWake)
	}
}
