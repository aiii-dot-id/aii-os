package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// screen is one live dashboard connection, collecting what it is pushed.
type screen struct {
	items chan dashboard.OutboxItem
}

func watchScreen(t *testing.T, a *App) *screen {
	t.Helper()
	conn := dialWSS(t, a.dashboard.Origin())
	s := &screen{items: make(chan dashboard.OutboxItem, 256)}
	ready, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		announced := false
		for {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			_, data, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			var msg struct {
				Type      string                 `json:"type"`
				RequestID string                 `json:"request_id"`
				Outbox    []dashboard.OutboxItem `json:"outbox"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Type == "probe" && msg.RequestID == "timer-screen-ready" && !announced {
				announced = true
				close(ready)
			}
			if msg.Type == "outbox" {
				for _, o := range msg.Outbox {
					select {
					case s.items <- o:
					case <-t.Context().Done():
						return
					}
				}
			}
		}
	}()
	t.Cleanup(func() {
		conn.CloseNow()
		<-done
	})
	// Accept completes the handshake before handleWS registers this page.
	// Its correlated reply proves admission; the same reader keeps any outbox
	// pushed before that reply, so readiness cannot consume a timer notice.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, map[string]any{"type": "probe", "request_id": "timer-screen-ready"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-done:
		t.Fatal("timer screen closed before its readiness reply")
	case <-ctx.Done():
		t.Fatal("timer screen readiness:", ctx.Err())
	}
	return s
}

func TestTimerScreenWaitsForItsProbeAndKeepsStartupOutbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		// A handshake alone releases nothing. No timing assumption is needed:
		// the old helper never sends the request that permits these messages.
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // the failing helper's cleanup can close before sending
		}
		var request dashboard.ClientMessage
		if err := json.Unmarshal(data, &request); err != nil || request.Type != "probe" || request.RequestID != "timer-screen-ready" {
			t.Errorf("readiness request: %s (%v)", data, err)
			return
		}
		for _, msg := range []dashboard.ServerMessage{
			{Type: "outbox", Outbox: []dashboard.OutboxItem{{ID: "startup-notice", Content: "keep me"}}},
			{Type: "probe", RequestID: request.RequestID},
		} {
			if err := wsjsonWrite(ctx, conn, msg); err != nil {
				t.Error(err)
				return
			}
		}
		_, _, _ = conn.Read(ctx) // the helper's cleanup owns connection close
	}))
	t.Cleanup(srv.Close)
	a := &App{dashboard: dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})}
	a.dashboard.SetOrigin(srv.URL)
	scr := watchScreen(t, a)
	select {
	case got := <-scr.items:
		if got.ID != "startup-notice" || got.Content != "keep me" {
			t.Fatalf("startup outbox changed: %+v", got)
		}
	default:
		t.Fatal("screen returned before collecting the outbox preceding its readiness reply")
	}
}

// expect waits for the first pushed item whose id matches.
func (s *screen) expect(t *testing.T, within time.Duration, id string) dashboard.OutboxItem {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case o := <-s.items:
			if o.ID == id {
				return o
			}
		case <-deadline:
			t.Fatalf("no transient push with id %s within %v", id, within)
		}
	}
}

// expectPrefix waits for the first pushed item whose id starts with prefix.
func (s *screen) expectPrefix(t *testing.T, within time.Duration, prefix string) dashboard.OutboxItem {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case o := <-s.items:
			if strings.HasPrefix(o.ID, prefix) {
				return o
			}
		case <-deadline:
			t.Fatalf("no transient push with id prefix %s within %v", prefix, within)
		}
	}
}

// expectAll waits until every id in want has been pushed, in any order.
func (s *screen) expectAll(t *testing.T, within time.Duration, want map[string]bool) {
	t.Helper()
	deadline := time.After(within)
	for len(want) > 0 {
		select {
		case o := <-s.items:
			delete(want, o.ID)
		case <-deadline:
			t.Fatalf("no transient push for %d id(s) within %v: %v", len(want), within, want)
		}
	}
}

// quiet asserts nothing more is pushed for a while.
func (s *screen) quiet(t *testing.T, for_ time.Duration) {
	t.Helper()
	select {
	case o := <-s.items:
		t.Fatalf("an unexpected push: %s: %q", o.ID, o.Content)
	case <-time.After(for_):
	}
}

// armTimer sets a resident timer through the verb and returns the durable
// deadline the row carries.
func armTimer(t *testing.T, a *App, args map[string]any) int64 {
	t.Helper()
	if _, err := a.engine.ExecuteAction(context.Background(), "verb", "timer", args); err != nil {
		t.Fatal(err)
	}
	return alarmDeadline(t, a, args["id"].(string))
}

func alarmDeadline(t *testing.T, a *App, id string) int64 {
	t.Helper()
	rows, err := a.store.DueAlarms("wall", int64(1)<<62, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.AlarmID == id {
			return r.Deadline
		}
	}
	t.Fatalf("alarm %s is not in the table", id)
	return 0
}

func alarmPresent(t *testing.T, a *App, id string) bool {
	t.Helper()
	rows, err := a.store.DueAlarms("wall", int64(1)<<62, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.AlarmID == id {
			return true
		}
	}
	return false
}

// frozenSnapshot is the projection and the ledger as SAFE found them.
type frozenSnapshot struct {
	tables map[string]string
	ledger string
}

func snapshotUnderSafe(t *testing.T, a *App, ledgerPath string) frozenSnapshot {
	t.Helper()
	ledger, _ := fileDigest(t, ledgerPath)
	return frozenSnapshot{tables: projectionSnapshot(t, a.store), ledger: ledger}
}

func (s frozenSnapshot) unchanged(t *testing.T, a *App, ledgerPath string) {
	t.Helper()
	after := projectionSnapshot(t, a.store)
	for name, digest := range s.tables {
		if after[name] != digest {
			t.Errorf("table %s changed under the freeze: %s -> %s", name, digest, after[name])
		}
	}
	if len(after) != len(s.tables) {
		t.Errorf("the set of tables changed under the freeze: %d -> %d", len(s.tables), len(after))
	}
	if got, _ := fileDigest(t, ledgerPath); got != s.ledger {
		t.Error("the ledger's bytes changed under SAFE")
	}
}

// A due resident timer, with the identity in SAFE, reaches the operator
// as a transient notice and a transient wake reply; the wake's read-only
// tool runs, its mutation is refused, and nothing durable is written —
// the alarm row included. It fires once.
func TestSafeTimerFiresTransientlyAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	forbidden := filepath.Join(dir, "must-not-exist.txt")
	llm, srv := newScriptedToolLLM(t, "SAFEWAKE", []toolStep{
		{"ls", map[string]any{"path": "."}},
		{"write", map[string]any{"file_path": forbidden, "content": "x"}},
	}, "VERDICT: awake", -1)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)

	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "wakeup", "tag": "safe", "duration": "1500ms", "message": "SAFEWAKE look around",
	})
	app.enterSafe("test: integrity lost at runtime")
	if _, frozen := app.store.Frozen(); !frozen {
		t.Fatal("fixture: the transition must freeze the store")
	}
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)

	notice := scr.expect(t, 15*time.Second, fmt.Sprintf("timer_wakeup_%d", deadline))
	if !strings.HasPrefix(notice.Content, "[timer wakeup #safe fired ") || !strings.HasSuffix(notice.Content, "] SAFEWAKE look around") {
		t.Fatalf("the notice reads like the floor would: %q", notice.Content)
	}
	reply := scr.expect(t, 30*time.Second, fmt.Sprintf("wake_wakeup_%d_safe", deadline))
	if reply.Content != "VERDICT: awake" {
		t.Fatalf("the wake's reply reaches the screen: %q", reply.Content)
	}

	seen := llm.results()
	if len(seen) != 2 {
		t.Fatalf("two tool results must reach the model, got %d: %q", len(seen), seen)
	}
	// The home, listed: ls names what read would open and counts what
	// the floor keeps (ledger.jsonl among them) without naming it (T71).
	if !strings.Contains(seen[0], "  log"+string(filepath.Separator)+"\n") || !strings.Contains(seen[0], "protected by the substrate floor were not listed") {
		t.Errorf("ls must run and list the directory: %q", seen[0])
	}
	if !strings.Contains(seen[1], "safe mode") {
		t.Errorf("a mutation must still be refused in SAFE, naming it: %q", seen[1])
	}
	if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
		t.Errorf("the refused write must not have happened: %v", err)
	}
	events := app.safeTools.Events()
	if len(events) != 2 || events[0].Tool != "ls" || !events[0].Done || events[0].Outcome != "succeeded" {
		t.Fatalf("the wake's calls are in the transient record only: %+v", events)
	}

	scr.quiet(t, 1500*time.Millisecond) // once per process: no second firing
	before.unchanged(t, app, ledgerPath)
	if got := alarmDeadline(t, app, "wakeup"); got != deadline {
		t.Fatalf("the alarm row must be untouched: deadline %d, want %d", got, deadline)
	}
	if msgs, _ := app.engine.UndeliveredMessages(); len(msgs) != 0 {
		t.Fatalf("nothing durable was delivered: %+v", msgs)
	}
}

// With more due rows in front of it than one scan returns — held alarms
// of another owner, all at the same moment — the resident timer still
// fires, because a SAFE pass reads the whole due set.
func TestSafeTimerFiresBehindAPageOfHeldRows(t *testing.T) {
	dir := t.TempDir()
	_, srv := newScriptedToolLLM(t, "SAFEWAKE", nil, "VERDICT: awake", -1)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)
	// Own the scheduling step, as TestSafeTimerBacklogIsOneWaiterAndOneTurn
	// does. With a live 1.2 s deadline, a loaded host was still inserting the
	// holds when they came due: arming the timer re-armed TIME, its NORMAL
	// pass handed hold_000 to maintenance before SAFE, and that maintenance
	// pass wrote its continuity posture under the freeze. Stop and join the
	// automatic driver; TIME's real pass runs only after SAFE, below.
	app.timeFac.Stop()

	at := time.Now().Add(-time.Minute).UnixMilli()
	const holds = 105
	for i := 0; i < holds; i++ {
		if err := app.store.SetAlarm(fmt.Sprintf("hold_%03d", i), app.continuity.AlarmOwner().Name(), "wall", at, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Armed through the verb; due, like the holds, and behind all of them
	// in scan order.
	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "zz_wakeup", "when": time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339), "message": "SAFEWAKE behind the crowd",
	})
	app.enterSafe("test: integrity lost at runtime")
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)
	passDone := make(chan error, 1)
	go func() { passDone <- app.timeFac.EvaluateAll(app.bgCtx) }()

	notice := scr.expect(t, 15*time.Second, fmt.Sprintf("timer_zz_wakeup_%d", deadline))
	if !strings.Contains(notice.Content, "SAFEWAKE behind the crowd") {
		t.Fatalf("notice: %q", notice.Content)
	}
	scr.expect(t, 30*time.Second, fmt.Sprintf("wake_zz_wakeup_%d_safe", deadline))
	select {
	case err := <-passDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("TIME's pass did not finish")
	}
	before.unchanged(t, app, ledgerPath)
	for i := 0; i < holds; i++ {
		if got := alarmDeadline(t, app, fmt.Sprintf("hold_%03d", i)); got != at {
			t.Fatalf("a held row moved: hold_%03d at %d, want %d", i, got, at)
		}
	}
}

// A recurring timer under SAFE fires for its durable deadline, then again
// at the next period boundary in memory, each once; the durable deadline
// never moves.
func TestSafeRecurringTimerFiresAgainAtTheNextBoundaryInMemory(t *testing.T) {
	dir := t.TempDir()
	_, srv := newScriptedToolLLM(t, "SAFEWAKE", nil, "VERDICT: tick", -1)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)

	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "tick", "duration": "1s", "every": "1s", "message": "SAFEWAKE tick",
	})
	app.enterSafe("test: integrity lost at runtime")
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)

	scr.expect(t, 15*time.Second, fmt.Sprintf("timer_tick_%d", deadline))
	scr.expect(t, 30*time.Second, fmt.Sprintf("wake_tick_%d_safe", deadline))
	next := deadline + 1000
	scr.expect(t, 15*time.Second, fmt.Sprintf("timer_tick_%d", next))
	scr.expect(t, 30*time.Second, fmt.Sprintf("wake_tick_%d_safe", next))

	before.unchanged(t, app, ledgerPath)
	if got := alarmDeadline(t, app, "tick"); got != deadline {
		t.Fatalf("the durable deadline moved: %d, want %d", got, deadline)
	}
}

// When the wake's inference fails under SAFE, the operator is told so,
// transiently, and SAFE does not retry.
func TestSafeTimerWakeFailureIsToldOnceAndNotRetried(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "SAFEWAKE") {
			w.WriteHeader(http.StatusBadRequest) // not retryable: the client returns it at once
			_, _ = io.WriteString(w, `{"error":{"message":"the model is gone"}}`)
			return
		}
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	t.Cleanup(srv.Close)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)

	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "wakeup", "duration": "1s", "message": "SAFEWAKE think",
	})
	app.enterSafe("test: integrity lost at runtime")
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)

	scr.expect(t, 15*time.Second, fmt.Sprintf("timer_wakeup_%d", deadline))
	told := scr.expect(t, 30*time.Second, fmt.Sprintf("wake_wakeup_%d_safe", deadline))
	if !strings.Contains(told.Content, "did not finish") || !strings.Contains(told.Content, "the model is gone") {
		t.Fatalf("the operator is told what happened, in the substrate's voice: %q", told.Content)
	}
	scr.quiet(t, 2*time.Second)
	before.unchanged(t, app, ledgerPath)
	if got := alarmDeadline(t, app, "wakeup"); got != deadline {
		t.Fatalf("the row must be untouched for recovery: %d", got)
	}
}

// A SAFE wake turn ends when the operator cancels it and when the identity
// stops with it in flight; the cancelled turn is reported transiently.
func TestSafeTimerWakeEndsOnCancelAndOnShutdown(t *testing.T) {
	for _, how := range []string{"cancel", "stop"} {
		t.Run(how, func(t *testing.T) {
			dir := t.TempDir()
			llm, srv := newScriptedToolLLM(t, "SAFEWAKE", nil, "never", 0)
			app, ledgerPath := liveIdentity(t, dir, srv.URL)
			stopped := false
			defer func() {
				if !stopped {
					stopApp(t, app)
				}
			}()
			// Own the firing step, not a race against setup's duration. The
			// old 800 ms timer could fire before SAFE or screen admission;
			// a transient notice sent with nobody connected has no replay.
			// As in the backlog test, stop and join only TIME's automatic
			// driver, then run its real pass once the fixture is ready.
			app.timeFac.Stop()

			deadline := armTimer(t, app, map[string]any{
				"action": "set", "id": "wakeup", "when": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "message": "SAFEWAKE hold on",
			})
			app.enterSafe("test: integrity lost at runtime")
			before := snapshotUnderSafe(t, app, ledgerPath)
			scr := watchScreen(t, app)
			if err := app.timeFac.EvaluateAll(app.bgCtx); err != nil {
				t.Fatal(err)
			}
			scr.expect(t, 15*time.Second, fmt.Sprintf("timer_wakeup_%d", deadline))
			select {
			case <-llm.blocked:
			case <-time.After(20 * time.Second):
				t.Fatal("the wake never reached the provider")
			}

			if how == "cancel" {
				if !app.CancelTurn() {
					t.Fatal("the wake turn must be cancellable")
				}
				told := scr.expect(t, 15*time.Second, fmt.Sprintf("wake_wakeup_%d_safe", deadline))
				if !strings.Contains(told.Content, "did not finish") {
					t.Fatalf("the cancelled wake is reported: %q", told.Content)
				}
				before.unchanged(t, app, ledgerPath)
				return
			}
			halted := stopAppAside(app)
			stopped = true
			select {
			case <-halted.done:
				halted.check(t)
			case <-time.After(30 * time.Second):
				t.Fatal("Stop did not return with a SAFE wake turn in flight")
			}
		})
	}
}

// SAFE beginning while a NORMAL wake is thinking: the floor landed
// durably before, the reply cannot, and it reaches the screen instead.
func TestSafeBeginningDuringAWakeDeliversTheReplyTransiently(t *testing.T) {
	dir := t.TempDir()
	held := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "SAFEWAKE") {
			once.Do(func() { close(held) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, textReply("I am awake."))
			return
		}
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	t.Cleanup(srv.Close)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)

	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "wakeup", "duration": "300ms", "message": "SAFEWAKE while normal",
	})
	select {
	case <-held:
	case <-time.After(20 * time.Second):
		t.Fatal("the NORMAL wake never reached the provider")
	}
	if !alarmPresent(t, app, "wakeup") {
		// The durable path accepted the firing: the floor is written and
		// the one-shot row is gone before the wake turn runs.
		t.Log("the row was already settled by the durable path")
	}
	app.enterSafe("test: integrity lost while the identity was thinking")
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)
	close(release)

	reply := scr.expectPrefix(t, 30*time.Second, "wake_wakeup_")
	if reply.Content != "I am awake." {
		t.Fatalf("the reply reaches the screen when the outbox cannot take it: %q", reply.Content)
	}
	before.unchanged(t, app, ledgerPath)
	msgs, _ := app.engine.UndeliveredMessages()
	floor := false
	for _, m := range msgs {
		if m.ID == fmt.Sprintf("timer_wakeup_%d", deadline) {
			floor = true
		}
		if strings.HasPrefix(m.ID, "wake_wakeup_") {
			t.Fatalf("the reply must not have been written under the freeze: %+v", m)
		}
	}
	if !floor {
		t.Fatal("the floor landed durably before SAFE and must still be there")
	}
}

// After a transient SAFE firing the row is untouched, so the next NORMAL
// boot fires it durably: floor and wake, through the ordinary path.
func TestSafeFiredTimerFiresDurablyAfterRestart(t *testing.T) {
	dir := t.TempDir()
	_, srv := newScriptedToolLLM(t, "SAFEWAKE", nil, "VERDICT: awake", -1)
	app, _ := liveIdentity(t, dir, srv.URL)
	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "wakeup", "tag": "again", "duration": "800ms", "message": "SAFEWAKE restart me",
	})
	app.enterSafe("test: integrity lost at runtime")
	scr := watchScreen(t, app)
	scr.expect(t, 15*time.Second, fmt.Sprintf("timer_wakeup_%d", deadline))
	scr.expect(t, 30*time.Second, fmt.Sprintf("wake_wakeup_%d_safe", deadline))
	if got := alarmDeadline(t, app, "wakeup"); got != deadline {
		t.Fatalf("the row must survive the SAFE firing: %d", got)
	}
	stopApp(t, app)

	// The same identity, verified again (the ledger was never touched).
	cfg := safebootConfig(t, dir, "RuntimeSafe",
		filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
	cfg.LLM = withTestProvider(t, dir, "test", srv.URL, "m", "sk-x")
	again := New(cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	defer stopApp(t, again)
	if _, safe := again.SafeMode(); safe {
		t.Fatal("fixture: the second boot was meant to be NORMAL")
	}
	wait := time.Now().Add(15 * time.Second)
	floor, wake := false, false
	for time.Now().Before(wait) {
		msgs, _ := again.engine.UndeliveredMessages()
		for _, m := range msgs {
			if m.ID == fmt.Sprintf("timer_wakeup_%d", deadline) && strings.Contains(m.Content, "SAFEWAKE restart me") {
				floor = true
			}
			if strings.HasPrefix(m.ID, "wake_wakeup_") && m.Content == "VERDICT: awake" {
				wake = true
			}
		}
		// The one-shot is deleted by the alarm's worker and the reply is
		// written by the wake's: neither waits for the other, so the
		// deletion is waited for with them.
		if floor && wake && !alarmPresent(t, again, "wakeup") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !floor || !wake {
		t.Fatalf("the durable firing after recovery: floor=%v wake=%v", floor, wake)
	}
	if alarmPresent(t, again, "wakeup") {
		t.Fatal("the durable path deletes the accepted one-shot")
	}
}

// A backlog is one waiter and one turn, not N — with more timers due
// together under SAFE than one scan returns (150 > safePageSize) and no
// screen connected: every firing is queued without a goroutine of its
// own, the notices are logged in a handful of lines naming every timer,
// TIME logs a summary rather than a line per row, and one turn answers
// all of them. The operator is never queued behind a turn per timer.
func TestSafeTimerBacklogIsOneWaiterAndOneTurn(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var wakes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "SAFEBACKLOG") {
			mu.Lock()
			wakes = append(wakes, string(body))
			mu.Unlock()
		}
		_, _ = io.WriteString(w, textReply("VERDICT: all of them"))
	}))
	t.Cleanup(srv.Close)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)
	// Own the scheduling step. A live three-second deadline could fire in
	// NORMAL while a loaded host was still inserting the 150-row fixture.
	// Stop and join the automatic driver, then use TIME's real catch-up pass
	// only after SAFE and the operator's turn gate are established below.
	app.timeFac.Stop()

	const n = 150
	// The first through the verb; all are already due, as a restored backlog
	// would be. Setup speed cannot decide which posture fires them.
	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "bk_000", "when": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), "message": "SAFEBACKLOG item",
	})
	for i := 1; i < n; i++ {
		if err := app.store.SetAlarm(fmt.Sprintf("bk_%03d", i), "timers", "wall", deadline, nil, `{"message":"SAFEBACKLOG item"}`); err != nil {
			t.Fatal(err)
		}
	}
	logs := logsink.CaptureForTest(t)
	app.enterSafe("test: integrity lost at runtime")
	before := snapshotUnderSafe(t, app, ledgerPath)

	// The operator's turn holds the gate throughout the controlled pass.
	if err := app.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			app.releaseTurn()
		}
	}()
	q := app.timerOwner.SafeWakes()
	base, peak := runtime.NumGoroutine(), 0
	passDone := make(chan error, 1)
	go func() { passDone <- app.timeFac.EvaluateAll(app.bgCtx) }()
	wait := time.Now().Add(20 * time.Second)
	for q.Len() < n && time.Now().Before(wait) {
		if g := runtime.NumGoroutine(); g > peak {
			peak = g
		}
		time.Sleep(time.Millisecond)
	}
	if got := q.Len(); got != n {
		t.Fatalf("all %d firings wait in the queue: %d", n, got)
	}
	select {
	case err := <-passDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("TIME's backlog pass did not finish")
	}
	if extra := peak - base; extra > 10 {
		t.Fatalf("a backlog of %d spawned %d goroutines: one drainer is the budget", n, extra)
	}
	app.releaseTurn()
	held = false
	// No more passes can start; join the existing delivery owner instead of
	// guessing from a log line plus a half-second sleep that it has finished.
	app.timerOwner.Stop()
	mu.Lock()
	got := append([]string(nil), wakes...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("one wake turn answers the backlog, got %d provider calls", len(got))
	}
	var fired, announced int
	var announcedText strings.Builder
	for _, line := range logs.Lines() {
		if strings.Contains(line, "fired transiently under SAFE") {
			fired++
		}
		if strings.Contains(line, "were transient-only") {
			announced++
			announcedText.WriteString(line)
		}
	}
	if fired == 0 || fired > 3 {
		t.Errorf("TIME logs the SAFE firings as a summary per pass, got %d lines", fired)
	}
	if announced == 0 || announced > 10 {
		t.Errorf("with nobody connected the notices are logged in a few lines, got %d", announced)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bk_%03d", i)
		if !strings.Contains(got[0], id) {
			t.Errorf("the turn was not told of %s", id)
		}
		if !strings.Contains(announcedText.String(), id) {
			t.Errorf("the log does not record %s's notice", id)
		}
	}
	before.unchanged(t, app, ledgerPath)
}

// With a screen connected, a batch of firings reaches it — every notice,
// keyed like the floor — and one turn answers them.
func TestSafeTimerBatchReachesAConnectedScreen(t *testing.T) {
	dir := t.TempDir()
	_, srv := newScriptedToolLLM(t, "SAFEBATCH", nil, "VERDICT: three", -1)
	app, ledgerPath := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)
	deadline := armTimer(t, app, map[string]any{
		"action": "set", "id": "b_0", "duration": "1500ms", "message": "SAFEBATCH one",
	})
	for _, id := range []string{"b_1", "b_2"} {
		if err := app.store.SetAlarm(id, "timers", "wall", deadline, nil, `{"message":"SAFEBATCH more"}`); err != nil {
			t.Fatal(err)
		}
	}
	app.enterSafe("test: integrity lost at runtime")
	before := snapshotUnderSafe(t, app, ledgerPath)
	scr := watchScreen(t, app)
	scr.expectAll(t, 15*time.Second, map[string]bool{
		fmt.Sprintf("timer_b_0_%d", deadline): true,
		fmt.Sprintf("timer_b_1_%d", deadline): true,
		fmt.Sprintf("timer_b_2_%d", deadline): true,
	})
	if reply := scr.expectPrefix(t, 30*time.Second, "wake_b_"); reply.Content != "VERDICT: three" {
		t.Fatalf("one turn's reply: %q", reply.Content)
	}
	scr.quiet(t, time.Second)
	before.unchanged(t, app, ledgerPath)
}
