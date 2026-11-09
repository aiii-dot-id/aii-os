package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// continues_test.go — WORK CONTINUES: a turn that ends with
// its work still active is followed by a fresh turn, until the work is
// delivered, the identity sets what it waits for, the record shows no
// outcome or a stuck pattern, or the operator holds it.

func operatorTurn2(t *testing.T, app *App, words string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.observeChat(ctx, words, func(kind, name, args string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkContinuesUntilItIsDelivered(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	logs := logsink.CaptureForTest(t)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`), toolCall("c2", "work", `{"action":"update","next_move":"gather the figures","steps":2}`)))
		case 2:
			_, _ = io.WriteString(w, textReply("first step done"))
		case 3: // the fresh turn records an outcome
			_, _ = io.WriteString(w, callReply("", toolCall("c3", "work", `{"action":"update","state":"the figures are in"}`)))
		case 4:
			_, _ = io.WriteString(w, textReply("second step done"))
		case 5: // the next fresh turn delivers
			_, _ = io.WriteString(w, callReply("", toolCall("c5", "work", `{"action":"deliver","result":"served: the task is done"}`)))
		default:
			_, _ = io.WriteString(w, textReply("delivered"))
		}
	})
	operatorTurn2(t, app, "start the long task")
	waitUntil(t, sawRequest(obs, "[work continues — continuation 1]"), "a fresh turn after the first ended by itself")
	waitUntil(t, sawRequest(obs, "[work continues — continuation 2]"), "a second fresh turn after an outcome was recorded")
	waitUntil(t, func() bool {
		ws, _ := app.store.WorkSessionByID(activeOrLast(t, app))
		return ws != nil && ws.Status == "delivered"
	}, "the work delivered")
	time.Sleep(400 * time.Millisecond)
	if sawRequest(obs, "[work continues — continuation 3]")() {
		t.Fatal("a fresh turn followed the delivery")
	}
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "[work continues — continuation 1]") && !strings.Contains(b, "Another fresh turn follows this one only if it records what moved") {
			t.Fatal("the fresh turn's fact does not say what keeps the work going")
		}
	}
	// THE LOG NAMES THE FRESH TURN AND THE STATUS THE IDENTITY WAS TOLD:
	// one continuation.start line per leg, and the
	// turn summary carries the last status line.
	out := logs.String()
	if !strings.Contains(out, "continuation.start") || !strings.Contains(out, ": leg 1 begins; the turn before it ended by itself") || !strings.Contains(out, ": leg 2 begins; the turn before it ended by itself") {
		t.Fatalf("the fresh turns are not named in the log:\n%s", out)
	}
	if !strings.Contains(out, `TURN_SUMMARY calls=2 refused=0 read_only=0 spawned=0 harvested=0 predicted=2 declared_at=2 independent=0 rounds=2 status="[status · calls 2: 2 ok, 0 failed · declared 2, 0 spent since, 2 remain · tokens `) {
		t.Fatalf("the first turn's summary does not carry the status line:\n%s", out)
	}
}

func activeOrLast(t *testing.T, app *App) string {
	t.Helper()
	var id string
	if err := app.store.DB().QueryRow(`SELECT id FROM work_sessions ORDER BY created_seq DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAFreshTurnWithoutARecordedOutcomeIsTheLast(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`)))
		default:
			_, _ = io.WriteString(w, textReply("thinking about it"))
		}
	})
	operatorTurn2(t, app, "start the long task")
	waitUntil(t, sawRequest(obs, "[work continues — continuation 1]"), "a fresh turn after the first ended by itself")
	waitUntil(t, func() bool { return resumesOnOfSession(t, app, activeOrLast(t, app)) == "word" }, "the condition becoming the operator's word")
	time.Sleep(400 * time.Millisecond)
	if sawRequest(obs, "[work continues — continuation 2]")() {
		t.Fatal("a fresh turn followed one that recorded no outcome")
	}
	if !noticeSays(t, app, "No progress on this work was recorded by the fresh turn") {
		t.Fatal("the record does not say why the work stopped")
	}
}

func TestAYieldIsTheIdentitysOwnWait(t *testing.T) {
	// A yield with a condition, and a bare yield (a delivery or message
	// wakes it): both are the identity's own wait, and neither is continued.
	for _, c := range []struct{ name, yield, want string }{{"for the operator's word", `{"action":"yield","resumes_on":"word"}`, "word"}, {"bare", `{"action":"yield"}`, ""}} {
		t.Run(c.name, func(t *testing.T) {
			app, _ := dispatchApp(t)
			defer stopApp(t, app)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
				switch n {
				case 1:
					_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`), toolCall("c2", "work", c.yield)))
				default:
					_, _ = io.WriteString(w, textReply("waiting"))
				}
			})
			operatorTurn2(t, app, "start the long task")
			time.Sleep(600 * time.Millisecond)
			if sawRequest(obs, "[work continues")() {
				t.Fatal("a fresh turn followed the identity's own wait")
			}
			if on := resumesOnOfSession(t, app, activeOrLast(t, app)); on != c.want {
				t.Fatalf("the identity's wait was not kept: %q, want %q", on, c.want)
			}
		})
	}
}

func TestTheHoldIsTheBrake(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageSupervised)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`)))
		default:
			_, _ = io.WriteString(w, textReply("first step done"))
		}
	})
	operatorTurn2(t, app, "start the long task")
	time.Sleep(600 * time.Millisecond)
	if sawRequest(obs, "[work continues")() {
		t.Fatal("a fresh turn ran under the operator's hold")
	}
	if on := resumesOnOfSession(t, app, activeOrLast(t, app)); on != "word" {
		t.Fatalf("the held work's condition is %q, want the operator's word", on)
	}
	// Told from the turn's own end, before any leg was queued.
	if n := heldNotice(t, app); !strings.Contains(n, "the turn ended with this work still active. It waits for your word.") {
		t.Fatalf("the operator was not told at the turn's end: %q", n)
	}
}

func TestStuckWorkWaitsForTheOperatorsWord(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.executor.Stop()
	if err := app.store.AddConversationTurn("operator", "go on"); err != nil {
		t.Fatal(err)
	}
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_stuck", "a long task"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		turn := "turn_op"
		if err := app.store.RecordToolStart(turn, i, "main", "m", "", "ls", `{"path":"a"}`); err != nil {
			t.Fatal(err)
		}
		if err := app.store.RecordToolDoneMeasured(turn, i, "ls", `{"path":"a"}`, "x", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	ending := app.scheduleContinuation(capEnded, "turn_op", false)
	if ending.kind != nothingFollows || !strings.Contains(ending.why, "stuck (the same call gave the same result 4 times)") {
		t.Fatalf("the stuck work was not stopped: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_stuck"); on != "word" {
		t.Fatalf("the stuck work's condition is %q, want the operator's word", on)
	}
	var n int
	_ = app.store.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id LIKE 'stuck_%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("the operator was not told the work is stuck: %d notices", n)
	}
}

// startedSession starts a session in an operator's turn with the executor
// stopped, so the test drives the host's decision directly.
func startedSession(t *testing.T, app *App, id string) *store.WorkSession {
	t.Helper()
	if err := app.store.AddConversationTurn("operator", "go on"); err != nil {
		t.Fatal(err)
	}
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, id, "a long task"); err != nil {
		t.Fatal(err)
	}
	ws, err := app.store.WorkSessionByID(id)
	if err != nil || ws == nil {
		t.Fatal(err)
	}
	return ws
}

func legsQueued(app *App) int {
	app.turn.mu.Lock()
	defer app.turn.mu.Unlock()
	return app.turn.legsQueued
}

// EVERY ENDING OF AN ACTIVE SESSION IS ONE DECISION (a review, 2026-11-06,
// finding 1): a cap with no promise continues under the record's brakes
// as an ending by itself does, its leg told what keeps the work going and
// its words passing the floor; the identity's own wait stands at a cap.
func TestACapIsContinuedByTheRecordsBrakes(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		_, _ = io.WriteString(w, textReply("carrying on"))
	})
	startedSession(t, app, "ws_cap")
	ending := app.scheduleContinuation(capTokenFence, "turn_op", false)
	if ending.kind != continued {
		t.Fatalf("a capped unpromised session was not continued: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_cap"); on != "now" {
		t.Fatalf("the capped work's condition is %q, want now", on)
	}
	waitUntil(t, sawRequest(obs, "[budget checkpoint — continuation 1]"), "the cap's fresh turn")
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "[budget checkpoint — continuation 1]") {
			if !strings.Contains(b, whatMovedSentence) {
				t.Fatal("the cap's leg was not told what keeps the work going")
			}
			if !strings.Contains(b, "You may speak to your operator now") {
				t.Fatal("the cap's leg was not told its choice: its words do not pass the floor")
			}
		}
	}
	// THE CAP'S FRESH TURN ENDS FIRST (the host, 2026-11-07: the gate at
	// f0fe94a5 — the wait below was set while that turn still ran, and the
	// turn's own ending settled the session after it): its reply recorded,
	// the turn gate free, no leg queued.
	waitUntil(t, func() bool {
		return replyRecorded(app, "carrying on")() && len(app.turnGate) == 1 && legsQueued(app) == 0
	}, "the cap's fresh turn ending")
	// A wait the identity set stands at a cap.
	word := "word"
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), turnSourceOperator)
	if err := app.store.SetWorkPromise(ctx, "ws_cap", nil, &word, "the identity's own wait"); err != nil {
		t.Fatal(err)
	}
	ending = app.scheduleContinuation(capDeclared, "turn_op", false)
	if ending.kind != nothingFollows || ending.why != "this work waits for your operator's next message, as you set" {
		t.Fatalf("the identity's wait was not honoured at a cap: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_cap"); on != "word" {
		t.Fatalf("the wait was changed by the cap: %q", on)
	}
}

// THE HOLD IS THE BRAKE AT A CAP TOO, said from the turn's own end.
func TestTheHoldBrakesACap(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.executor.Stop()
	pressStage(t, app, store.StageSupervised)
	startedSession(t, app, "ws_held")
	ending := app.scheduleContinuation(capRounds, "turn_op", false)
	if ending.kind != nothingFollows || !strings.Contains(ending.why, "your operator holds this work") {
		t.Fatalf("the hold did not brake the cap: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_held"); on != "word" {
		t.Fatalf("the held work's condition is %q, want the operator's word", on)
	}
	if n := heldNotice(t, app); !strings.Contains(n, "the turn ended with this work still active. It waits for your word.") {
		t.Fatalf("the operator was not told: %q", n)
	}
}

// A STAGE THE HOST CANNOT READ STOPS THE FRESH TURN WITHOUT A HOLD (a review,
// 2026-11-06, finding 2): the work is left under its own `now`, no leg is
// queued, the operator is not told of a hold they did not press, and the
// next tick reads again.
func TestAStageReadErrorStopsSchedulingWithoutAHold(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		_, _ = io.WriteString(w, textReply("carrying on"))
	})
	app.stageReader = func() (string, error) { return "", errors.New("the relationship row could not be read") }
	ws := startedSession(t, app, "ws_unread")
	ending := app.scheduleContinuation(capEnded, "turn_op", false)
	if ending.kind != nothingFollows || ending.why != stageUnreadWhy {
		t.Fatalf("the unread stage did not stand the fresh turn down: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_unread"); on != "now" {
		t.Fatalf("the condition under an unread stage is %q, want now", on)
	}
	if n := heldNotice(t, app); n != "" {
		t.Fatalf("a hold the operator did not press was told: %q", n)
	}
	if n := legsQueued(app); n != 0 {
		t.Fatalf("%d legs queued under an unread stage", n)
	}
	if !noticeSays(t, app, "The operator's stage could not be read; the fresh turn waits for the next tick") {
		t.Fatal("the record does not say why the fresh turn waits")
	}
	// The tick reads again: still unreadable, the `now` is left as it is.
	ws, _ = app.store.WorkSessionByID(ws.ID)
	if app.releaseNow(ws, releasedAtTick) {
		t.Fatal("a fresh turn was queued under an unread stage")
	}
	if on := resumesOnOfSession(t, app, "ws_unread"); on != "now" {
		t.Fatalf("the tick changed the condition: %q", on)
	}
	// Readable when the tick queues the leg, unreadable again when the leg
	// holds the gate: the leg stands down, the `now` is left as it is, and
	// the model is asked nothing.
	if err := app.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.stageReader = nil
	if !app.releaseNow(ws, releasedAtTick) {
		app.releaseTurn()
		t.Fatal("the fresh turn was not queued once the stage could be read")
	}
	app.stageReader = func() (string, error) { return "", errors.New("unreadable again") }
	app.releaseTurn()
	var notice string
	waitUntil(t, func() bool {
		notice = outboxReply(t, app, "continuation_")
		return notice != ""
	}, "the operator's notice that the leg stood down")
	if !strings.HasSuffix(notice, " did not run: the host could not read the operator's stage before it began; it is tried again at the next tick.") {
		t.Fatalf("the operator is told %q", notice)
	}
	if on := resumesOnOfSession(t, app, "ws_unread"); on != "now" {
		t.Fatalf("the leg's stand-down changed the condition: %q", on)
	}
	if n := len(obs.snapshot()); n != 0 {
		t.Fatalf("the leg asked the model %d time(s) under an unread stage", n)
	}
	// Readable again: the tick takes the `now` up, and the leg runs.
	app.stageReader = nil
	ws, _ = app.store.WorkSessionByID(ws.ID)
	if !app.releaseNow(ws, releasedAtTick) {
		t.Fatal("the fresh turn was not queued once the stage could be read")
	}
	waitUntil(t, sawRequest(obs, "[work continues — continuation "), "the fresh turn after the stage could be read")
	if !sawRequest(obs, "ended with nothing holding the work, so the host started this one")() {
		t.Fatal("the tick's fresh turn does not say the host started it")
	}
}

// THE REFLECTION CADENCE IS THE RESUMED SESSION'S (a review, 2026-11-06,
// finding 5): a turn carrying an older session on reads that session's
// reflect_every, not the newest active session's.
func TestTheReflectionCadenceIsTheResumedSessions(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.executor.Stop()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_old", "the older work"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetWorkReflectEvery(ctx, "ws_old", 2); err != nil {
		t.Fatal(err)
	}
	if err := app.store.StartWorkSession(ctx, "ws_new", "the newer work"); err != nil {
		t.Fatal(err)
	}
	if n := app.reflectEvery(context.Background()); n != 0 {
		t.Fatalf("a turn of the active session reads a cadence of %d, want none", n)
	}
	if n := app.reflectEvery(interaction.WithResumeSession(context.Background(), "ws_old")); n != 2 {
		t.Fatalf("a turn carrying ws_old on reads a cadence of %d, want 2", n)
	}
}

// THE FLOOR FOR AN UNPROMISED LEG: a continuing leg's words reach the
// operator when the record
// holds an outcome that changed since they last spoke; a checkpoint that
// moved nothing stays on the page, with the row beside it.
func TestAnUnpromisedLegsWordsReachTheOperatorOnlyWhenSomethingMoved(t *testing.T) {
	// THE LEG THAT DELIVERS, THEN SPEAKS, IS SENT: the delivered session is
	// no longer active when the floor
	// reads, and the delivery is what the operator asked for.
	t.Run("the leg delivers the work, then speaks", func(t *testing.T) {
		app, _ := dispatchApp(t)
		defer stopApp(t, app)
		obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
			switch n {
			case 1:
				_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`)))
			case 2:
				_, _ = io.WriteString(w, textReply("first step done"))
			case 3: // the fresh turn delivers and chooses to speak
				_, _ = io.WriteString(w, callReply("",
					toolCall("c3d", "work", `{"action":"deliver","result":"served: all three done in sequence"}`),
					toolCall("c3s", "work", `{"action":"say","choice":"speak"}`)))
			default:
				_, _ = io.WriteString(w, textReply("report: all three done in sequence"))
			}
		})
		operatorTurn2(t, app, "do all three, then report")
		waitUntil(t, sawRequest(obs, "[work continues — continuation 1]"), "the fresh turn")
		waitUntil(t, replyRecorded(app, "report: all three done in sequence"), "the report")
		waitUntil(t, func() bool { return outboxHolds(t, app, "wake_cont_") }, "the report on the outbox")
		if r := rowBeside(t, app); r != "" {
			t.Fatalf("a row beside the delivered report: %q", r)
		}
	})
	for _, moved := range []bool{true, false} {
		t.Run(fmt.Sprintf("moved=%t", moved), func(t *testing.T) {
			app, _ := dispatchApp(t)
			defer stopApp(t, app)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
				switch n {
				case 1:
					_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`)))
				case 2:
					_, _ = io.WriteString(w, textReply("first step done"))
				case 3: // the fresh turn: it speaks, and sets its own wait
					calls := []string{toolCall("c3s", "work", `{"action":"say","choice":"speak"}`)}
					if moved {
						calls = append(calls, toolCall("c3m", "work", `{"action":"update","state":"the figures are in"}`))
					}
					calls = append(calls, toolCall("c3w", "work", `{"action":"update","resumes_on":"word"}`))
					_, _ = io.WriteString(w, callReply("", calls...))
				default:
					_, _ = io.WriteString(w, textReply("checkpoint: the figures are in"))
				}
			})
			operatorTurn2(t, app, "start the long task")
			waitUntil(t, sawRequest(obs, "[work continues — continuation 1]"), "the fresh turn")
			waitUntil(t, replyRecorded(app, "checkpoint: the figures are in"), "the fresh turn's words")
			waitUntil(t, func() bool { return resumesOnOfSession(t, app, activeOrLast(t, app)) == "word" }, "the leg's own wait")
			if moved {
				waitUntil(t, func() bool { return outboxHolds(t, app, "wake_cont_") }, "the words on the outbox")
				if r := rowBeside(t, app); r != "" {
					t.Fatalf("a row beside words that were sent: %q", r)
				}
				return
			}
			if r := waitRowBeside(t, app); r != wordsHeldUnpromisedRow {
				t.Fatalf("the row beside the unsent words of an unpromised turn: %q", r)
			}
			if outboxHolds(t, app, "wake_cont_") {
				t.Fatal("a checkpoint that moved nothing was sent to the operator")
			}
		})
	}
}

// THE CARD LINE SAYS WHAT THE HOST WILL DO (a review, 2026-11-06): the
// switch, an arrival's turn, an unreadable stage and the hold each stop
// the fresh turn, and the line under the card says which.
func TestTheCardLineSaysWhatTheHostWillDo(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.executor.Stop()
	startedSession(t, app, "ws_card")
	ctx := interaction.WithTurnSource(context.Background(), turnSourceOperator)
	card := func(ctx context.Context) string {
		t.Helper()
		s, err := app.buildWorkState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if c := card(ctx); !strings.Contains(c, workContinuesLine) {
		t.Fatalf("the default line is missing: %q", c)
	}
	if c := card(interaction.WithTurnSource(context.Background(), turnSourceArrival)); !strings.Contains(c, "This turn was opened by an arrival, so nothing continues this work by itself when it ends.") {
		t.Fatalf("an arrival's turn is not said: %q", c)
	}
	app.stageReader = func() (string, error) { return "", errors.New("unreadable") }
	if c := card(ctx); !strings.Contains(c, "Your operator's stage could not be read: when this turn ends this work waits for the next tick, which reads it again.") {
		t.Fatalf("an unreadable stage is not said: %q", c)
	}
	app.stageReader = func() (string, error) { return store.StageSupervised, nil }
	if c := card(ctx); !strings.Contains(c, "Your operator holds this work (supervised): when this turn ends it waits for their word.") {
		t.Fatalf("the hold is not said: %q", c)
	}
	app.stageReader = nil
	off := false
	app.cfgMu.Lock()
	app.cfg.Agency.TurnContinuation = &off
	app.cfgMu.Unlock()
	if c := card(ctx); !strings.Contains(c, "Nothing continues this work by itself: continuation is switched off (agency.turn_continuation).") {
		t.Fatalf("the switch is not said: %q", c)
	}
}

// THE OPERATOR'S OWN TURN IS NOT HELD TO THE FRESH TURN'S PROGRESS: a
// `now` a stood-down leg left behind, then the operator's turn ending
// with the work still active, continues the work; only a fresh turn of
// the session must have recorded an outcome (freshTurnOf).
func TestTheOperatorsOwnTurnIsNotHeldToTheFreshTurnsProgress(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	fixtureModel(t, app, func(n int, w http.ResponseWriter) { _, _ = io.WriteString(w, textReply("carrying on")) })
	if err := app.acquireTurn(context.Background()); err != nil { // the gate held: no leg runs
		t.Fatal(err)
	}
	defer app.releaseTurn()
	startedSession(t, app, "ws_op")
	app.stageReader = func() (string, error) { return "", errors.New("unreadable") }
	if ending := app.scheduleContinuation(capEnded, "turn_op", false); ending.kind != nothingFollows {
		t.Fatalf("rig: %+v", ending)
	}
	app.stageReader = nil
	// The operator speaks again; their turn ends with the work active and
	// nothing recorded.
	op := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op2"), turnSourceOperator)
	if _, err := app.store.RecordConversation(op, "operator", "go on", "turn_op2", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if ending := app.scheduleContinuation(capEnded, "turn_op2", false); ending.kind != continued {
		t.Fatalf("the operator's own turn was held to the fresh turn's progress: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_op"); on != "now" {
		t.Fatalf("the condition is %q, want now", on)
	}
}

// AN UNCHANGED REWRITE DOES NOT HIDE A FAILURE PATTERN (a review,
// 2026-11-06): real progress, then the same call failing four times, then
// the same state written again: the ending is held as stuck, not continued
// on the strength of the earlier progress.
func TestAnUnchangedRewriteDoesNotHideStuckWork(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.executor.Stop()
	if err := app.store.AddConversationTurn("operator", "go on"); err != nil {
		t.Fatal(err)
	}
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), turnSourceOperator)
	if err := app.store.StartWorkSession(ctx, "ws_rewrite", "a long task"); err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateWorkState(ctx, "ws_rewrite", "half the figures are in"); err != nil { // real progress
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		if err := app.store.RecordToolStart("turn_op", i, "main", "m", "", "sh", `{"cmd":"x"}`); err != nil {
			t.Fatal(err)
		}
		if err := app.store.RecordToolDoneMeasured("turn_op", i, "sh", `{"cmd":"x"}`, "Error: no", interaction.Failed, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.UpdateWorkState(ctx, "ws_rewrite", "half the figures are in"); err != nil { // the same again
		t.Fatal(err)
	}
	ending := app.scheduleContinuation(capEnded, "turn_op", false)
	if ending.kind != nothingFollows || !strings.Contains(ending.why, "stuck (the same call failed 4 times)") {
		t.Fatalf("the unchanged rewrite hid the failures: %+v", ending)
	}
	if on := resumesOnOfSession(t, app, "ws_rewrite"); on != "word" {
		t.Fatalf("the stuck work's condition is %q, want the operator's word", on)
	}
}

// A YIELD THAT LEAVES NOTHING HOLDING THE WORK GOES ON AT THE TURN'S END:
// resumes_on now on a yield is taken up as an update's
// now is, by the turn's own end, with no tick (the tick's fresh turn says
// the host started it; this one, that the turn before it ended by itself).
func TestAYieldToNowGoesOnAtTheTurnsEnd(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`), toolCall("c2", "work", `{"action":"yield","resumes_on":"now"}`)))
		case 2: // the yield's closing answer, tools withdrawn
			_, _ = io.WriteString(w, textReply("yielded; it goes on"))
		case 3: // the fresh turn delivers, so the chain ends
			_, _ = io.WriteString(w, callReply("", toolCall("c3", "work", `{"action":"deliver","result":"served: the task is done"}`)))
		default:
			_, _ = io.WriteString(w, textReply("delivered"))
		}
	})
	operatorTurn2(t, app, "start the long task")
	waitUntil(t, sawRequest(obs, "[work continues — continuation 1] Your previous turn ended by itself."), "a fresh turn at the end of the turn that yielded to now")
	if sawRequest(obs, "so the host started this one")() {
		t.Fatal("the fresh turn came from the tick, not from the turn's end")
	}
}

// AND A YIELD'S OTHER WAITS ARE UNCHANGED (the control): a yield naming a
// time, as one naming the operator's word, is the identity's own wait, and
// no fresh turn follows it.
func TestAYieldToATimeIsStillTheIdentitysOwnWait(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	at := fmt.Sprintf("time:%d", time.Now().Add(time.Hour).UnixMilli())
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch n {
		case 1:
			_, _ = io.WriteString(w, callReply("", toolCall("c1", "work", `{"action":"start","description":"a long task"}`), toolCall("c2", "work", `{"action":"yield","resumes_on":"`+at+`"}`)))
		default:
			_, _ = io.WriteString(w, textReply("waiting"))
		}
	})
	operatorTurn2(t, app, "start the long task")
	time.Sleep(600 * time.Millisecond)
	if sawRequest(obs, "[work continues")() {
		t.Fatal("a fresh turn followed a yield to a time")
	}
	if on := resumesOnOfSession(t, app, activeOrLast(t, app)); on != at {
		t.Fatalf("the identity's wait was not kept: %q, want %q", on, at)
	}
}
