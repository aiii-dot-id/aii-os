package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func replyRecordApp(t *testing.T) *App {
	t.Helper()
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	fixtureModel(t, a, func(_ int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(textReply("noted")))
	})
	if _, err := a.store.DB().Exec(`CREATE TRIGGER refuse_reply BEFORE INSERT ON conversations WHEN NEW.role='resident' AND NEW.kind='message' BEGIN SELECT RAISE(ABORT,'fixture: the record refuses the reply'); END`); err != nil {
		t.Fatal(err)
	}
	return a
}

// wantHostFailure says the error is the record's refusal.
func wantHostFailure(t *testing.T, path string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "fixture: the record refuses the reply") {
		t.Errorf("%s: error = %v, want the record's refusal reported", path, err)
	}
}

// outboxReply is the operator's undelivered outbox row with id prefix.
func outboxReply(t *testing.T, a *App, prefix string) string {
	t.Helper()
	msgs, err := a.store.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, prefix) {
			return m.Content
		}
	}
	return ""
}

func TestAReplyTheRecordRefusedIsReturnedWithTheFailure(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		a := replyRecordApp(t)
		ctx := goldenTurnCtx(t)
		if err := a.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		reply, err := a.observeChat(ctx, "are you there?", func(kind, name, args string) {})
		if reply != "noted" {
			t.Errorf("chat: reply = %q, want the identity's words beside the failure", reply)
		}
		wantHostFailure(t, "chat", err)
	})
	t.Run("wake", func(t *testing.T) {
		a := replyRecordApp(t)
		ctx := goldenTurnCtx(t)
		if err := a.acquireTurn(ctx); err != nil {
			t.Fatal(err)
		}
		defer a.releaseTurn()
		reply, err := a.wake(withTurnSource(ctx, turnSourceArrival), string(interaction.Participant), "[messages] a contact wrote")
		if reply != "noted" {
			t.Errorf("wake: reply = %q, want the identity's words beside the failure", reply)
		}
		wantHostFailure(t, "wake", err)
	})
	t.Run("timer", func(t *testing.T) {
		a := replyRecordApp(t)
		err := a.wakeTimerAlarm(goldenTurnCtx(t), "d2", "[timer d2 fired] check the oven")
		if got := outboxReply(t, a, "wake_d2_"); got != "noted" {
			t.Errorf("timer: the operator's outbox holds %q, want the alarm's answer", got)
		}
		wantHostFailure(t, "timer", err)
	})
	t.Run("continuation", func(t *testing.T) {
		a := replyRecordApp(t)
		logs := logsink.CaptureForTest(t)
		a.continueCappedTurn("ws_d2", 1, capDeclared, "")
		if got := outboxReply(t, a, "wake_cont_"); got != "noted" {
			t.Errorf("continuation: the operator's outbox holds %q, want the leg's answer", got)
		}
		reported := false
		for _, line := range strings.Split(logs.String(), "\n") {
			reported = reported || strings.Contains(line, "continuation.error") && strings.Contains(line, "fixture: the record refuses the reply")
		}
		if !reported {
			t.Errorf("continuation: the record's failure was not reported as the continuation's:\n%s", logs.String())
		}
	})
}

// The operator's spoken words are answered aloud when the record refuses
// the reply: the words are spoken to the session that heard them, and the
// heard path is told the answer's record failed.
func TestASpokenAnswerTheRecordRefusedIsStillSpoken(t *testing.T) {
	a := replyRecordApp(t)
	h := &voiceHandle{id: "vs-d2", drained: make(chan struct{})}
	a.voiceSessions.Store("vs-d2", h)
	a.voiceModes.Store("vs-d2", true)
	var mu sync.Mutex
	var spoken []string
	prev := voiceSynthesize
	voiceSynthesize = func(_ *App, _ context.Context, session string, _ uint64, reply string) replyVerdict {
		mu.Lock()
		spoken = append(spoken, session+"/"+reply)
		mu.Unlock()
		return replyAdmitted
	}
	t.Cleanup(func() { voiceSynthesize = prev })
	err := a.observeVoice(goldenTurnCtx(t), heardUtterance{Source: "speech engine", Text: "are you there?", Answer: true, SessionID: "vs-d2", Gen: 1, Operator: true})
	mu.Lock()
	got := strings.Join(spoken, ",")
	mu.Unlock()
	if got != "vs-d2/noted" {
		t.Errorf("spoken = %q, want the answer spoken to the session that heard the question", got)
	}
	wantHostFailure(t, "spoken", err)
}

func TestTheDrainReasonSaysASpokenReplyWasNotRecorded(t *testing.T) {
	heardBy := func(t *testing.T, a *App, operator bool) (spoken, outcome, logs string) {
		t.Helper()
		h := &voiceHandle{id: "vs-c", drained: make(chan struct{})}
		a.voiceSessions.Store("vs-c", h)
		a.voiceModes.Store("vs-c", true)
		var mu sync.Mutex
		var said []string
		prev := voiceSynthesize
		voiceSynthesize = func(_ *App, _ context.Context, session string, _ uint64, reply string) replyVerdict {
			mu.Lock()
			said = append(said, session+"/"+reply)
			mu.Unlock()
			return replyAdmitted
		}
		t.Cleanup(func() { voiceSynthesize = prev })
		capture := logsink.CaptureForTest(t)
		a.handleHeard(goldenTurnCtx(t), heardUtterance{Source: "speech engine", Text: "are you there?", Answer: true, SessionID: "vs-c", Gen: 1, Operator: operator})
		mu.Lock()
		defer mu.Unlock()
		outcome, _ = h.replyOutcome.Load().(string)
		return strings.Join(said, ","), outcome, capture.String()
	}
	for _, operator := range []bool{true, false} {
		name := "participant"
		if operator {
			name = "operator"
		}
		t.Run(name, func(t *testing.T) {
			spoken, outcome, logs := heardBy(t, replyRecordApp(t), operator)
			if spoken != "vs-c/noted" {
				t.Errorf("spoken = %q, want the reply spoken to the session that heard the words", spoken)
			}
			if !strings.HasPrefix(outcome, "replied, but the reply was not recorded: ") || !strings.Contains(outcome, "fixture: the record refuses the reply") {
				t.Errorf("drain reason = %q, want a spoken reply that was not recorded", outcome)
			}
			if !strings.Contains(logs, "the engine's transcript was recorded; the reply was spoken but not recorded: ") {
				t.Errorf("the log does not say the reply was spoken but not recorded:\n%s", logs)
			}
		})
		t.Run(name+"/no words", func(t *testing.T) {
			// Control: a turn that failed before any words keeps "no reply".
			a := replyRecordApp(t)
			fixtureModel(t, a, func(_ int, w http.ResponseWriter) { http.Error(w, "fixture failure", http.StatusServiceUnavailable) })
			spoken, outcome, _ := heardBy(t, a, operator)
			if spoken != "" || !strings.HasPrefix(outcome, "no reply: ") {
				t.Errorf("a turn that said nothing: spoken %q, drain reason %q; want nothing spoken and \"no reply: …\"", spoken, outcome)
			}
		})
	}
}
