package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// The child dies inside a real wake turn, with its queue claim uncompleted.
// Advance the lease clock through the real store sweep (not a sleep), then
// boot the identity normally. The actual composed retry must say it resumes
// interrupted work, carry an earlier send's outcome and never resend that row.
func TestTimerWakeSurvivesProcessDeathAndKeepsDeliveryOutcomes(t *testing.T) {
	if dir := os.Getenv("AII_L28_CRASH_DIR"); dir != "" {
		timerWakeCrashChild(t, dir)
		return
	}
	for _, sent := range []bool{false, true} {
		t.Run(fmt.Sprintf("sent=%t", sent), func(t *testing.T) {
			dir := t.TempDir()
			requests := make(chan string, 4)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				body, _ := io.ReadAll(r.Body)
				calls.Add(1)
				requests <- string(body)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, textReply("Recovered the timer promise."))
			}))
			defer srv.Close()
			a, _ := liveIdentity(t, dir, srv.URL)
			contact(t, a, Contact{Name: "friend", Channel: "primary", Address: "friend@example.test"})
			cfg := a.configSnapshot()
			stopApp(t, a)
			raw, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "child-config.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, exe, "-test.run=^TestTimerWakeSurvivesProcessDeathAndKeepsDeliveryOutcomes$", "-test.timeout=50s")
			child.Env = append(os.Environ(), "AII_L28_CRASH_DIR="+dir, fmt.Sprintf("AII_L28_SENT=%t", sent))
			out, err := child.CombinedOutput()
			if strings.Contains(string(out), "WARNING: DATA RACE") {
				t.Fatalf("crash child reported a data race before its deliberate exit:\n%s", out)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 91 {
				t.Fatalf("child did not die in wake: %v\n%s", err, out)
			}
			st, err := store.NewForTest(cfg.Identity.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			var state string
			if err := st.DB().QueryRow(`SELECT state FROM work_queue WHERE id='wake_timer_durable_42'`).Scan(&state); err != nil || state != "CLAIMED" {
				st.Close()
				t.Fatalf("crash lost owned work: %s %v", state, err)
			}
			// The child sets claimed_at=1 at its model-call step. Passing a
			// clock two seconds behind now makes the normal retry backoff due
			// at boot, without replacing either sweep or claim with test SQL.
			if n, err := st.SweepExpiredLeases(time.Now().Add(-2 * time.Second).UnixMilli()); err != nil || n != 1 {
				st.Close()
				t.Fatalf("crash recovery: %d %v", n, err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			next := New(&cfg)
			if err := startLiveForTest(next); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, next)
			var body string
			select {
			case body = <-requests:
			case <-ctx.Done():
				t.Fatal("boot never retried the durable wake")
			}
			text := requestText(t, body)
			messages := wireMessages(t, body)
			if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "resuming an interrupted timer wake") {
				t.Fatal("resume fact was put only in foldable background, not the current wake message")
			}
			if !strings.Contains(text, "resuming an interrupted timer wake") || !strings.Contains(text, "DURABLE-WAKE") {
				t.Fatalf("retry lost its truthful context: %s", text)
			}
			if sent && !strings.Contains(text, "to friend: delivered via org.example.primary") {
				t.Fatalf("retry lost its earlier send outcome: %s", text)
			}
			// The request proves this turn owns the gate. Taking it after the
			// response waits for that turn's completion, not an arbitrary delay.
			if err := next.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			result := next.timerOwner.OnAlarm(ctx, "durable", "wall", 42, `{"tag":"proof","message":"DURABLE-WAKE"}`)
			next.releaseTurn()
			if !result.Accepted {
				t.Fatalf("duplicate firing refused: %+v", result)
			}
			var repeated atomic.Int32
			installReasonedAdapterCounting(t, next, &repeated)
			if _, err := next.deliverOutbox(ctx); err != nil {
				t.Fatal(err)
			}
			if repeated.Load() != 0 {
				t.Fatal("host repeated an earlier send")
			}
			stopApp(t, next) // joins the executor before final counts/readback
			if calls.Load() != 1 {
				t.Fatalf("one recovered wake ran %d turns", calls.Load())
			}
			st, err = store.NewForTest(cfg.Identity.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var retries int
			if err := st.DB().QueryRow(`SELECT state,retry_count FROM work_queue WHERE id='wake_timer_durable_42'`).Scan(&state, &retries); err != nil || state != "DONE" || retries != 1 {
				t.Fatalf("wake not completed exactly once after recovery: %s retry=%d %v", state, retries, err)
			}
		})
	}
}

func timerWakeCrashChild(t *testing.T, dir string) {
	raw, err := os.ReadFile(filepath.Join(dir, "child-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	t.Chdir(dir)
	a := New(&cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	sent := os.Getenv("AII_L28_SENT") == "true"
	var effects atomic.Int32
	installReasonedAdapterCounting(t, a, &effects)
	a.toolReg.Register(&fixtureTool{name: "fixture_send", run: func(ctx context.Context) (tools.Result, error) {
		if err := a.store.AddOutboxMessage("msg_l28", "peer", "friend", "The earlier attempt sent this.", nil); err != nil {
			return tools.Result{}, err
		}
		n, err := a.deliverOutbox(ctx)
		return tools.Result{Output: fmt.Sprintf("delivered %d", n)}, err
	}})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if _, err := a.store.DB().Exec(`UPDATE work_queue SET claimed_at=1 WHERE id='wake_timer_durable_42' AND state='CLAIMED'`); err != nil {
			os.Exit(92)
		}
		if calls.Add(1) == 1 && sent {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, callReply("", toolCall("send1", "fixture_send", `{}`)))
			return
		}
		if sent && effects.Load() != 1 {
			os.Exit(93)
		}
		os.Exit(91)
	}))
	defer srv.Close()
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	result := a.timerOwner.OnAlarm(t.Context(), "durable", "wall", 42, `{"tag":"proof","message":"DURABLE-WAKE"}`)
	if !result.Accepted {
		t.Fatalf("enqueue failed: %+v", result)
	}
	<-t.Context().Done()
}

func TestDurableTimerWakeWaitsThroughSafeAndShutdown(t *testing.T) {
	requests := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		requests <- string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, textReply("awake"))
	}))
	defer srv.Close()
	a, _ := liveIdentity(t, t.TempDir(), srv.URL)
	defer stopApp(t, a)
	a.executor.Stop() // leave the queued item pending until the SAFE transition
	firing := identity.TimerWake{AlarmID: "held", Deadline: 42, Message: "HELD-WAKE"}
	raw, _ := json.Marshal(firing)
	w := &store.WorkItem{ID: "wake_" + firing.DeliveryID(), Kind: identity.TimerWakeWorkKind, Payload: string(raw), DedupKey: "held@42"}
	const original = "original firing, not the retry time — HELD-WAKE"
	if _, err := a.store.AddOutboxNoticeWithWork(firing.DeliveryID(), original, w); err != nil {
		t.Fatal(err)
	}
	a.enterSafe("test: hold durable work")
	before := projectionSnapshot(t, a.store)
	if got, err := a.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli()); err != nil || got != nil {
		t.Fatalf("SAFE claimed wake: %+v %v", got, err)
	}
	if err := (&timerWakeHandler{a}).RunWork(t.Context(), w); err == nil {
		t.Fatal("SAFE ran durable wake")
	}
	after := projectionSnapshot(t, a.store)
	for table, hash := range before {
		if after[table] != hash {
			t.Fatalf("SAFE changed %s", table)
		}
	}
	select {
	case <-requests:
		t.Fatal("SAFE durable item reached inference")
	default:
	}
	// Actual recovery is a new normal boot; SAFE itself is not weakened.
	cfg := a.configSnapshot()
	stopApp(t, a)

	restored, err := store.NewForTest(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.SettleRestore(store.Restore{At: time.Now(), Source: "snapshot"}, "fixture restore"); err != nil {
		restored.Close()
		t.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	next := New(&cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, next)
	ctx, done := context.WithTimeout(t.Context(), 15*time.Second)
	defer done()
	select {
	case body := <-requests:
		if !strings.Contains(requestText(t, body), original) {
			t.Fatal("recovery replaced the original firing notice")
		}
		if !strings.Contains(requestText(t, body), "it was due before the restore") {
			t.Fatal("queued wake lost the restore disclosure")
		}
	case <-ctx.Done():
		t.Fatal("recovery did not execute pending wake")
	}
	if err := next.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	next.releaseTurn()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (&timerWakeHandler{next}).RunWork(cancelled, w); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown lost refusal: %v", err)
	}
}

func TestTimerWakeBacklogLeavesOtherWorkClaimable(t *testing.T) {
	a, _ := promptCacheApp(t)
	a.executor.Stop()
	for i := 0; i < 3; i++ {
		result := a.timerOwner.OnAlarm(t.Context(), fmt.Sprintf("backlog%d", i), "wall", 42, "owed")
		if !result.Accepted {
			t.Fatalf("backlog enqueue: %+v", result)
		}
	}
	if _, err := a.store.EnqueueWork(&store.WorkItem{ID: "other-work", Kind: "fixture.other", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	kinds := []string{identity.TimerWakeWorkKind, "fixture.other"}
	first, err := a.store.ClaimWork(kinds, time.Now().UnixMilli())
	if err != nil || first == nil || first.Kind != identity.TimerWakeWorkKind {
		t.Fatalf("first wake: %+v %v", first, err)
	}
	second, err := a.store.ClaimWork(kinds, time.Now().UnixMilli())
	if err != nil || second == nil || second.Kind != "fixture.other" {
		t.Fatalf("timer waiters consumed the other worker: %+v %v", second, err)
	}
	if third, err := a.store.ClaimWork(kinds, time.Now().UnixMilli()); err != nil || third != nil {
		t.Fatalf("second timer claimed beside the first: %+v %v", third, err)
	}
}

func TestTimerWakeRejectsMalformedWorkBeforeTakingTurn(t *testing.T) {
	for _, w := range []*store.WorkItem{
		{ID: "bad", Payload: "{"},
		{ID: "wake_timer__0", Payload: "{}"},
		{ID: "other-firing", Payload: `{"alarm_id":"a","deadline":42}`},
	} {
		// No App is supplied: malformed work must never reach a turn owner.
		if err := (&timerWakeHandler{}).RunWork(t.Context(), w); err == nil {
			t.Fatalf("invalid work accepted: %+v", w)
		}
	}
}

func TestCancellingTimerWakeDoesNotRestartTheCancelledAttempt(t *testing.T) {
	model, srv := newScriptedToolLLM(t, "CANCELNORMAL", nil, "never", 0)
	a, _ := liveIdentity(t, t.TempDir(), srv.URL)
	defer stopApp(t, a)
	result := a.timerOwner.OnAlarm(t.Context(), "cancel-normal", "wall", 42, `{"message":"CANCELNORMAL"}`)
	if !result.Accepted {
		t.Fatalf("timer handoff: %+v", result)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	select {
	case <-model.blocked:
	case <-ctx.Done():
		t.Fatal("wake never reached the provider")
	}
	if !a.CancelTurn() {
		t.Fatal("wake was not cancellable")
	}
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	a.releaseTurn()
	a.executor.Stop() // join completion before reading final queue state
	var state string
	var retries int
	if err := a.store.DB().QueryRow(`SELECT state,retry_count FROM work_queue WHERE id='wake_timer_cancel-normal_42'`).Scan(&state, &retries); err != nil {
		t.Fatal(err)
	}
	if state != "DONE" || retries != 0 {
		t.Fatalf("Cancel silently queued another attempt: %s retries=%d", state, retries)
	}
}

func TestATimerWakeCutByAShutdownIsLeftForTheNextStart(t *testing.T) {
	model, srv := newScriptedToolLLM(t, "CUTBYSHUTDOWN", nil, "never", 0)
	a, _ := liveIdentity(t, t.TempDir(), srv.URL)
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, a)
		}
	}()
	if result := a.timerOwner.OnAlarm(t.Context(), "cut-by-shutdown", "wall", 42, `{"message":"CUTBYSHUTDOWN"}`); !result.Accepted {
		t.Fatalf("timer handoff: %+v", result)
	}
	select {
	case <-model.blocked:
	case <-time.After(20 * time.Second):
		t.Fatal("wake never reached the provider")
	}
	cfg := a.configSnapshot()
	stopApp(t, a)
	stopped = true
	after, err := store.NewForTest(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	var state string
	if err := after.DB().QueryRow(`SELECT state FROM work_queue WHERE id='wake_timer_cut-by-shutdown_42'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "DONE" {
		t.Fatal("A WAKE CUT BY A SHUTDOWN WAS FINISHED FOR GOOD, AS ONE THE OPERATOR STOPPED IS")
	}
}
