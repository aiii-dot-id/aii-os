package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/coder/websocket"
)

// leftover_steer_test.go — the steering queue's second exit.
//
// Words accepted while a turn runs leave the queue one of two ways: at a
// tool boundary (DrainSteering), or, when the turn makes no further tool
// call, at its end, where they open a turn of their own
// (runLeftoverSteerTurn). Which exit they take depends on whether the model
// happened to call a tool. A rule held at one exit and not at the other is
// held by that chance, and two were: the mode in force when spoken words are
// delivered, and that a turn does not run on words its record refused.

// leftoverApp is an identity whose model answers "Heard." and keeps every
// request it is sent.
func leftoverApp(t *testing.T) (*App, *contScript) {
	t.Helper()
	asked := &contScript{}
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("the fixture model could not read its request: %v", err)
		}
		asked.add(string(body))
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "Heard."}},
			"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 4, "output_tokens": 1}})
	})
	// The mode door reads the provider registry beside the configuration:
	// it is given a place of its own, or it is scaffolded in the package.
	a.cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	return a, asked
}

// operatorRows is every operator message the record holds, oldest first.
func operatorRows(t *testing.T, a *App) []string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT content FROM conversations WHERE role='operator' AND kind='message' ORDER BY turn_seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatal(err)
		}
		out = append(out, content)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// WORDS LEFT IN THE QUEUE KEEP THE TURN. The ending turn's token is not
// returned while its queue holds words: whoever already waits on the gate (a
// continuation leg, an alarm, a harvest) would take it first, and the words'
// own turn, started after the return, would wait a whole pass behind them. At
// the step where the token used to go back the gate is still held, and it is
// free again once the words' turn has ended. This identity cannot run a turn,
// so that turn is a refusal; it holds and returns the token all the same.
func TestWordsLeftInTheQueueKeepTheTurn(t *testing.T) {
	a := newSteerApp(t)
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	if steered, err := a.Steer("stop, that is the wrong repository"); err != nil || !steered {
		t.Fatalf("the words were not queued behind the running turn: steered=%v err=%v", steered, err)
	}
	// The step runs at every release, the words' own turn's included: only
	// the first, the ending turn's, is read.
	var steps atomic.Int32
	var held atomic.Bool
	a.turnAfterHandoff = func() {
		if steps.Add(1) == 1 {
			held.Store(a.TurnActive())
		}
	}
	a.EndTurn() // no tool boundary drained them
	a.bgWG.Wait()
	if !held.Load() {
		t.Fatal("the turn was returned while its queue held words: whoever waited on the gate takes it before the words are read")
	}
	if a.TurnActive() {
		t.Fatal("the turn was not given back after the words' own turn ended")
	}
	if n := len(a.PendingSteers()); n != 0 {
		t.Fatalf("%d message(s) are still queued with no turn running", n)
	}
}

// deadlineProbe is a tool that says whether the turn that called it runs
// under a deadline, and how far off it is.
type deadlineProbe struct {
	ran     atomic.Bool
	bounded atomic.Bool
	left    atomic.Int64 // nanoseconds, when bounded
}

func (p *deadlineProbe) Name() string               { return "deadline_probe" }
func (p *deadlineProbe) Description() string        { return "reads the calling turn's deadline" }
func (p *deadlineProbe) Parameters() map[string]any { return nil }
func (p *deadlineProbe) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	p.ran.Store(true)
	if d, ok := ctx.Deadline(); ok {
		p.bounded.Store(true)
		p.left.Store(int64(time.Until(d)))
	}
	return tools.Result{Output: "read"}, nil
}

// THE WORDS' TURN CARRIES NO DEADLINE OF ITS OWN. Words left in the queue
// open a turn as a typed message does, and no clock ends a typed message's
// turn. This one ran under fifteen minutes: the same sentence was cut at a
// quarter of an hour when it was typed a second before the turn ended and
// never when typed a second after. A tool called inside the words' turn
// reads the turn's own context: no deadline rides it.
func TestTheWordsTurnCarriesNoDeadlineOfItsOwn(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	probe := &deadlineProbe{}
	a.toolReg.Register(probe)
	fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, _ = io.WriteString(w, callReply("", toolCall("d1", probe.Name(), `{}`)))
			return
		}
		_, _ = io.WriteString(w, textReply("noted"))
	})
	// The words' turn is given the token by the release that kept it for
	// them (releaseTurn): held here as that release leaves it.
	<-a.turnGate
	a.runLeftoverSteerTurn([]steerEntry{{role: roleOperator, content: "and one more thing, said as you finished", recordCtx: context.Background()}})
	if !probe.ran.Load() {
		t.Fatal("rig: the words' turn never called the tool that reads its deadline")
	}
	if probe.bounded.Load() {
		t.Fatalf("the words' turn runs under a deadline of its own, %s away: no other turn a person opens has one", time.Duration(probe.left.Load()).Round(time.Second))
	}
}

// SPOKEN WORDS LEFT IN THE QUEUE ARE ADMITTED UNDER THE MODE IN FORCE WHEN
// THEIR TURN IS TAKEN. They were heard as a question while a turn ran; a
// meeting began while they waited; the turn ended without a tool call, so no
// boundary delivered them. They are the room's — recorded with the note,
// answered by nobody, their binding released, the drain told why — exactly
// as the tool boundary and the wait for a pass decide it. Words that were
// typed are not the room's in any mode: they still get their turn.
func TestSpokenWordsLeftInTheQueueAreAdmittedUnderTheModeInForceWhenTheirTurnIsTaken(t *testing.T) {
	const spoken, typed = "asked while the turn ran", "typed while the turn ran"
	// leave queues spoken words (and typed ones, where asked) behind a
	// running turn, sets the mode, and ends the turn with no tool boundary.
	leave := func(t *testing.T, listen, speak string, withTyped bool) (*App, *contScript, *voiceHandle, *atomic.Int32) {
		t.Helper()
		a, asked := leftoverApp(t)
		voiceModeDoor(t, a, listenInteractive, speakOn)
		h, _ := newTrackedSession(a, "vs-left", true)
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		released := new(atomic.Int32)
		b := &voiceBinding{session: h.id, seq: 1, text: spoken, done: func() { released.Add(1) }}
		if steered, err := a.admit(roleOperator, voiceMarker+spoken, b, nil); err != nil || !steered {
			t.Fatalf("the spoken words were not queued behind the running turn: steered=%v err=%v", steered, err)
		}
		if withTyped {
			if steered, err := a.admit(roleOperator, typed, nil, nil); err != nil || !steered {
				t.Fatalf("the typed words were not queued behind the running turn: steered=%v err=%v", steered, err)
			}
		}
		voiceModeDoor(t, a, listen, speak)
		a.EndTurn() // no tool boundary drained them: the queue's second exit
		a.bgWG.Wait()
		if n := released.Load(); n != 1 {
			t.Errorf("the spoken words' binding was released %d time(s), want exactly once", n)
		}
		if !a.TryBeginTurn() {
			t.Fatal("the turn was not given back after the queue emptied")
		}
		a.EndTurn()
		a.bgWG.Wait()
		return a, asked, h, released
	}

	t.Run("under interactive they are answered", func(t *testing.T) {
		// The control: the same words, the same exit, no meeting.
		a, asked, _, _ := leave(t, listenInteractive, speakOn, false)
		reqs := asked.snapshot()
		if len(reqs) != 1 || !strings.Contains(reqs[0], spoken) {
			t.Fatalf("under interactive the words left in the queue open their turn and reach the model: %d request(s)", len(reqs))
		}
		if rows := operatorRows(t, a); len(rows) != 1 || rows[0] != voiceMarker+spoken {
			t.Fatalf("the question must be recorded as the operator's, without the room note: %q", rows)
		}
	})
	t.Run("a meeting began and they are the room's", func(t *testing.T) {
		a, asked, h, _ := leave(t, listenMeeting, speakOff, false)
		if n := len(asked.snapshot()); n != 0 {
			t.Fatalf("a meeting began while the words waited for the turn's end, and they opened an answering turn: the model was asked %d time(s)", n)
		}
		if rows := operatorRows(t, a); len(rows) != 1 || rows[0] != voiceMarker+voiceRoomNote+spoken {
			t.Fatalf("the words must be recorded as the room's, once: %q", rows)
		}
		if outcome, _ := h.replyOutcome.Load().(string); outcome != "recorded (meeting), no reply" {
			t.Fatalf("the drain would not say why nothing was answered: %q", outcome)
		}
	})
	t.Run("typed words in the same queue still get their turn", func(t *testing.T) {
		a, asked, _, _ := leave(t, listenMeeting, speakOff, true)
		reqs := asked.snapshot()
		if len(reqs) != 1 || !strings.Contains(reqs[0], typed) {
			t.Fatalf("typed words are the operator's in every mode and open their turn: %d request(s)", len(reqs))
		}
		// The session's drain reason is the typed turn's by now (its reply
		// was shown as text), so the record is what tells the two apart.
		rows := operatorRows(t, a)
		if len(rows) != 2 || !slices.Contains(rows, typed) || !slices.Contains(rows, voiceMarker+voiceRoomNote+spoken) {
			t.Fatalf("the record must hold the typed words as typed and the spoken ones as the room's: %q", rows)
		}
	})
}

// QUEUED WORDS THE RECORD REFUSED ARE NOT ANSWERED WITHOUT THEM. What a turn
// answers reaches the model through the record and no other way: the words
// are priced, never appended (runTurnLockedInner). An ordinary message whose
// record fails does not run (handleMessageLocked). A queued one ran: the
// failure was logged and the model was asked on a history that did not hold
// the words, so an operator's correction could be accepted on the page,
// refused by the record, and answered as if it had never been said.
//
// The record here refuses one operator message and nothing else: reads work,
// and so would the record of a reply.
func TestALeftoverTurnDoesNotRunOnWordsTheRecordRefused(t *testing.T) {
	const first, correction, last = "start the long job", "the correction: do not start it", "and tell me when it is done"
	// queued sends the three messages from a page while a turn runs, ends
	// the turn with no tool boundary, and returns what the page was then
	// told about the second message's record and the next thing said to it.
	queued := func(t *testing.T, refuse bool) (a *App, asked *contScript, receipt *dashboard.ChatRecording, said *dashboard.ServerMessage, logs string) {
		t.Helper()
		a, asked = leftoverApp(t)
		var err error
		if a.keyPair, err = crypto.GenerateKeyPair(); err != nil {
			t.Fatal(err)
		}
		h := &dashboard.WSHandler{Speaker: "identity", AdmitChat: a.admitOperatorChat, ReleaseTurn: a.EndTurn, AcquireTurn: a.acquireTurn, PendingSteers: a.PendingSteers,
			HandleMessage: func(context.Context, string) (string, error) { return "", nil }}
		s := dashboard.New("127.0.0.1", 0, h)
		if _, err := s.Start(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Shutdown(context.Background()) })
		a.dashboard = s
		conn := dialWSS(t, s.Origin())
		if refuse {
			if _, err := a.store.DB().Exec(`CREATE TRIGGER refuse_queued BEFORE INSERT ON conversations WHEN NEW.role='operator' AND NEW.content LIKE '%the correction%' BEGIN SELECT RAISE(ABORT,'fixture: the record refuses the queued words'); END`); err != nil {
				t.Fatal(err)
			}
		}
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for i, words := range []string{first, correction, last} {
			id := []string{"input-1", "input-2", "input-3"}[i]
			raw, _ := json.Marshal(dashboard.ClientMessage{Type: "chat", RequestID: id, Message: words})
			if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
				t.Fatal(err)
			}
			if ack := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "chat_ack" && m.RequestID == id }); ack.Message != "accepted" {
				t.Fatalf("%q was not accepted behind the running turn: %+v", words, ack)
			}
		}
		capture := logsink.CaptureForTest(t)
		a.EndTurn() // no tool boundary drained them
		msg := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "chat_receipt" && m.RequestID == "input-2" })
		said = readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "response" })
		a.bgWG.Wait()
		if !a.TryBeginTurn() {
			t.Fatal("the turn was not given back")
		}
		a.EndTurn()
		a.bgWG.Wait()
		return a, asked, msg.ChatRecording, said, capture.String()
	}

	t.Run("recorded: all are answered", func(t *testing.T) {
		// The control: the same messages, a record that takes them all.
		a, asked, receipt, said, _ := queued(t, false)
		reqs := asked.snapshot()
		if len(reqs) != 1 || !strings.Contains(reqs[0], first) || !strings.Contains(reqs[0], "the correction") || !strings.Contains(reqs[0], last) {
			t.Fatalf("recorded words open one turn that holds them all: %d request(s)", len(reqs))
		}
		if receipt == nil || receipt.Error != nil || receipt.Ref == nil || receipt.Ref.Source != "recorded" {
			t.Fatalf("the second message was recorded and its receipt must say so: %+v", receipt)
		}
		if said.Role != "identity" || said.Message != "Heard." {
			t.Fatalf("the page must be shown the answer: %+v", said)
		}
		if rows := operatorRows(t, a); !slices.Equal(rows, []string{first, correction, last}) {
			t.Fatalf("the record must hold the messages in order: %q", rows)
		}
	})
	t.Run("refused: nothing is answered", func(t *testing.T) {
		a, asked, receipt, said, logs := queued(t, true)
		if n := len(asked.snapshot()); n != 0 {
			t.Fatalf("the record refused a queued message and its turn ran anyway: the model was asked %d time(s) on a history that does not hold it", n)
		}
		if receipt == nil || receipt.Error == nil || receipt.Error.Code != dashboard.ChatRecordingFailedCode || !strings.Contains(receipt.Error.Message, "fixture: the record refuses the queued words") {
			t.Fatalf("the refused message's receipt must carry the record's refusal: %+v", receipt)
		}
		if receipt.Ref != nil && receipt.Ref.Source == "recorded" {
			t.Fatalf("the receipt claims a durable record of words the record refused: %+v", receipt.Ref)
		}
		if said.Role != "system" || !strings.Contains(said.Message, "1 of 3 queued messages were not recorded, so none was run") ||
			!strings.Contains(said.Message, "fixture: the record refuses the queued words") || !strings.Contains(said.Message, "The others are recorded") {
			t.Fatalf("every screen must be told that nothing ran, how many were refused and why; it was told %q as %q", said.Message, said.Role)
		}
		// Every message is offered to the record, whatever became of the
		// one before it: what it takes stays, to be read by the turn that
		// comes next, and the refused words are in no record.
		if rows := operatorRows(t, a); !slices.Equal(rows, []string{first, last}) {
			t.Fatalf("the record must hold the messages it took and not the refused one: %q", rows)
		}
		if !strings.Contains(logs, "steering.error") || !strings.Contains(logs, "fixture: the record refuses the queued words") {
			t.Fatalf("the log does not say why the queued words did not run:\n%s", logs)
		}
	})
}

// Spoken words the record refused are let go as words that will never run:
// their binding is released, so the session's Finish is not held for a reply
// that cannot come, and what an observation held for them is retired.
func TestRefusedLeftoverSpokenWordsReleaseTheirSession(t *testing.T) {
	a, asked := leftoverApp(t)
	h, _ := newTrackedSession(a, "vs-refused", true)
	if _, err := a.store.DB().Exec(`CREATE TRIGGER refuse_spoken BEFORE INSERT ON conversations WHEN NEW.role='operator' BEGIN SELECT RAISE(ABORT,'fixture: the record refuses the queued words'); END`); err != nil {
		t.Fatal(err)
	}
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
	released := new(atomic.Int32)
	b := &voiceBinding{session: h.id, seq: 7, text: "words the record refuses", done: func() { released.Add(1) }}
	if steered, err := a.admit(roleOperator, voiceMarker+"words the record refuses", b, nil); err != nil || !steered {
		t.Fatalf("the spoken words were not queued behind the running turn: steered=%v err=%v", steered, err)
	}
	a.EndTurn()
	a.bgWG.Wait()
	if n := len(asked.snapshot()); n != 0 {
		t.Fatalf("the record refused the spoken words and their turn ran anyway: the model was asked %d time(s)", n)
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("the binding of words that will never run was released %d time(s), want exactly once", n)
	}
	if _, held := a.speakerPending.Load(voiceRefKey(h.id, 7)); held {
		t.Fatal("refused spoken words left their observation held")
	}
	if outcome, _ := h.replyOutcome.Load().(string); !strings.HasPrefix(outcome, "no reply: the words were not recorded: ") || !strings.Contains(outcome, "fixture: the record refuses the queued words") {
		t.Fatalf("the drain would not say why nothing was answered: %q", outcome)
	}
}

func TestRecordedSpokenWordsBesideARefusedMessageAreNotToldTheyWereRefused(t *testing.T) {
	a, asked := leftoverApp(t)
	h, _ := newTrackedSession(a, "vs-mixed", true)
	if _, err := a.store.DB().Exec(`CREATE TRIGGER refuse_typed BEFORE INSERT ON conversations WHEN NEW.role='operator' AND NEW.content LIKE '%the record refuses%' BEGIN SELECT RAISE(ABORT,'fixture: the record refuses these words'); END`); err != nil {
		t.Fatal(err)
	}
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
	released := new(atomic.Int32)
	const taken = "spoken words the record takes"
	b := &voiceBinding{session: h.id, seq: 7, text: taken, done: func() { released.Add(1) }}
	if steered, err := a.admit(roleOperator, voiceMarker+taken, b, nil); err != nil || !steered {
		t.Fatalf("the spoken words were not queued: steered=%v err=%v", steered, err)
	}
	if steered, err := a.admit(roleOperator, "a typed correction the record refuses", nil, nil); err != nil || !steered {
		t.Fatalf("the typed words were not queued: steered=%v err=%v", steered, err)
	}
	a.EndTurn()
	a.bgWG.Wait()
	if n := len(asked.snapshot()); n != 0 {
		t.Fatalf("the record refused a queued message and the turn ran anyway: the model was asked %d time(s)", n)
	}
	if rows := operatorRows(t, a); !slices.Equal(rows, []string{voiceMarker + taken}) {
		t.Fatalf("the record must hold the spoken words and not the refused ones: %q", rows)
	}
	if n := released.Load(); n != 1 {
		t.Fatalf("the binding was released %d time(s), want exactly once", n)
	}
	outcome, _ := h.replyOutcome.Load().(string)
	if !strings.HasPrefix(outcome, "no reply: the words are recorded; their turn was not run because another queued message was not recorded: ") || !strings.Contains(outcome, "fixture: the record refuses these words") {
		t.Fatalf("the drain must say the words are recorded and why nothing answered them: %q", outcome)
	}
	// The observation held for the recorded words went with their record.
	if got, found := spokenAttribution(t, a, voiceRefKey(h.id, 7)); !found || !strings.Contains(got, `"speaker_id":"quinn"`) {
		t.Fatalf("the recorded words lost their observation: found=%v %q", found, got)
	}
}

// One session, two utterances, one refused: the session is told how many of
// how many, once, whichever was queued last.
func TestASessionWithRefusedAndRecordedWordsIsToldHowMany(t *testing.T) {
	for _, order := range [][2]string{
		{"words the record takes", "words the record refuses"},
		{"words the record refuses", "words the record takes"},
	} {
		a, asked := leftoverApp(t)
		h, _ := newTrackedSession(a, "vs-two", true)
		if _, err := a.store.DB().Exec(`CREATE TRIGGER refuse_one BEFORE INSERT ON conversations WHEN NEW.role='operator' AND NEW.content LIKE '%the record refuses%' BEGIN SELECT RAISE(ABORT,'fixture: the record refuses these words'); END`); err != nil {
			t.Fatal(err)
		}
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		released := new(atomic.Int32)
		for i, text := range order {
			b := &voiceBinding{session: h.id, seq: int64(i + 1), text: text, done: func() { released.Add(1) }}
			if steered, err := a.admit(roleOperator, voiceMarker+text, b, nil); err != nil || !steered {
				t.Fatalf("%q was not queued: steered=%v err=%v", text, steered, err)
			}
		}
		a.EndTurn()
		a.bgWG.Wait()
		if n := len(asked.snapshot()); n != 0 {
			t.Fatalf("%q: the model was asked %d time(s)", order, n)
		}
		if n := released.Load(); n != 2 {
			t.Fatalf("%q: %d binding(s) released, want both", order, n)
		}
		if outcome, _ := h.replyOutcome.Load().(string); !strings.HasPrefix(outcome, "no reply: 1 of 2 utterances were not recorded: ") {
			t.Fatalf("%q: the drain must say how many of the session's words were refused: %q", order, outcome)
		}
	}
}
