package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
type evLog struct {
	mu    sync.Mutex
	types []string
}

func (l *evLog) sink(ev dashboard.VoiceEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.types = append(l.types, ev.Type)
}
func (l *evLog) has(t string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return hasVoiceEvent(l.types, t)
}

func hasVoiceEvent(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func dropEvent(dashboard.VoiceEvent) {}

func finalEvent(id, text string) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": "transcript_final", "session_id": id, "text": text, "speaker": "james"})
	return pluginhost.Event{Type: "transcript_final", SessionID: id, Raw: raw}
}

func finishedEvent(id string, end int64) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": "input_finished", "session_id": id, "stream_id": "in-" + id, "end_sample": end, "processed_end_sample": end})
	return pluginhost.Event{Type: "input_finished", SessionID: id, Raw: raw}
}

func awaitDrained(t *testing.T, h *voiceHandle) {
	t.Helper()
	select {
	case <-h.drained:
	case <-time.After(5 * time.Second):
		t.Fatal("the input's completion never resolved")
	}
}

func newTrackedSession(a *App, id string, answer bool) (*voiceHandle, *fakeEngineSession) {
	f := &fakeEngineSession{}
	h := &voiceHandle{id: id, v: f, b: &audio.Binding{InputHandle: "in-" + id, Contained: true}, done: make(chan struct{}), drained: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	a.voiceModes.Store(id, answer)
	return h, f
}

func stubWake(t *testing.T, fn func() (string, error)) {
	t.Helper()
	saved := voiceWake
	voiceWake = func(*App, context.Context, string, string) (string, error) { return fn() }
	t.Cleanup(func() { voiceWake = saved })
}

// .
// .
// .
// .
// .
// .
// .
func TestInputCompletionOrdersTheFinalReplyBeforeDrainClose(t *testing.T) {
	ctx := context.Background()

	t.Run("a delayed final reply settles, then ONE drain", func(t *testing.T) {
		a := newVoiceApp(t)
		var events evLog
		a.voiceEventSink = events.sink
		gate := make(chan struct{})
		stubWake(t, func() (string, error) { <-gate; return "the final answer", nil })
		h, f := newTrackedSession(a, "vs-1", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent("vs-1", "goodbye"), dropEvent)
		a.voiceEngineEvent(nil, finishedEvent("vs-1", 320), dropEvent)
		select {
		case <-h.drained:
			t.Fatal("the drain must wait for the final reply to settle")
		case <-time.After(50 * time.Millisecond):
		}
		if len(f.closes()) != 0 {
			t.Fatalf("no close before the reply settled: %v", f.closes())
		}
		close(gate)
		awaitDrained(t, h)
		if got := strings.Join(f.opsSeen(), ","); got != "finish:vs-1,synthesize:vs-1,close:vs-1" {
			t.Fatalf("order must be finish, synthesize, drain-close: %s", got)
		}
		if c := f.closes(); len(c) != 1 || c[0] != "drain: reply admitted" {
			t.Fatalf("the drain names the admitted reply: %v", c)
		}
		if !events.has("drain_requested") {
			t.Fatal("the drain is evidence")
		}
		a.voiceEngineEvent(nil, finishedEvent("vs-1", 320), dropEvent)
		if len(f.closes()) != 1 {
			t.Fatalf("exactly one drain: %v", f.closes())
		}
	})
	t.Run("silence: completion without a transcript drains, inventing nothing", func(t *testing.T) {
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "never asked", nil })
		h, f := newTrackedSession(a, "vs-2", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finishedEvent("vs-2", 320), dropEvent)
		awaitDrained(t, h)
		if got := strings.Join(f.opsSeen(), ","); got != "finish:vs-2,close:vs-2" {
			t.Fatalf("silence: no synthesis, then drain: %s", got)
		}
		if c := f.closes(); len(c) != 1 || !strings.Contains(c[0], "no utterance after Finish") {
			t.Fatalf("the outcome is explicit: %v", c)
		}
	})
	t.Run("two queued utterances: the second steers into the running turn; both settle before the one drain", func(t *testing.T) {
		a := newVoiceApp(t)
		entered, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		stubWake(t, func() (string, error) { once.Do(func() { close(entered) }); <-gate; return "an answer", nil })
		h, f := newTrackedSession(a, "vs-3", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent("vs-3", "first"), dropEvent)
		<-entered
		a.voiceEngineEvent(nil, finalEvent("vs-3", "second"), dropEvent)
		a.voiceEngineEvent(nil, finishedEvent("vs-3", 320), dropEvent)
		select {
		case <-h.drained:
			t.Fatal("the drain must wait for the running turn to settle")
		case <-time.After(50 * time.Millisecond):
		}
		close(gate)
		awaitDrained(t, h)
		if n := len(f.synthed()); n != 1 {
			t.Fatalf("one turn answers both utterances (the second steered into it): %d syntheses", n)
		}
		if got := strings.Join(f.opsSeen(), ","); got != "finish:vs-3,synthesize:vs-3,close:vs-3" {
			t.Fatalf("one drain, after the settled turn: %s", got)
		}
	})
	t.Run("the completion may precede the Finish acknowledgement", func(t *testing.T) {
		a := newVoiceApp(t)
		h, f := newTrackedSession(a, "vs-4", true)
		a.voiceEngineEvent(nil, finishedEvent("vs-4", 320), dropEvent)
		awaitDrained(t, h)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		if c := f.closes(); len(c) != 1 || !strings.HasPrefix(c[0], "drain:") {
			t.Fatalf("the drain follows the engine's word: %v", c)
		}
	})
	t.Run("an abort during the Finish skips the drain", func(t *testing.T) {
		a := newVoiceApp(t)
		var events evLog
		a.voiceEventSink = events.sink
		stubWake(t, func() (string, error) { return "the final answer", nil })
		h, f := newTrackedSession(a, "vs-5", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent("vs-5", "goodbye"), dropEvent)
		if err := h.Close(ctx, "abort"); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finishedEvent("vs-5", 320), dropEvent)
		awaitDrained(t, h)
		if c := f.closes(); len(c) != 1 || !strings.HasPrefix(c[0], "abort:") {
			t.Fatalf("only the abort closes; no drain after it: %v", c)
		}
		if !events.has("drain_skipped") {
			t.Fatal("the skipped drain is evidence")
		}
	})
	t.Run("meeting mode records, then drains", func(t *testing.T) {
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "never asked", nil })
		h, f := newTrackedSession(a, "vs-6", false)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent("vs-6", "noted"), dropEvent)
		a.voiceEngineEvent(nil, finishedEvent("vs-6", 320), dropEvent)
		awaitDrained(t, h)
		if got := strings.Join(f.opsSeen(), ","); got != "finish:vs-6,close:vs-6" {
			t.Fatalf("meeting: recorded, no synthesis, then drain: %s", got)
		}
		if c := f.closes(); len(c) != 1 || !strings.Contains(c[0], "recorded (meeting)") {
			t.Fatalf("%v", c)
		}
	})
	t.Run("a failed wake still drains, naming the failure", func(t *testing.T) {
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "", errors.New("the model timed out") })
		h, f := newTrackedSession(a, "vs-7", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent("vs-7", "goodbye"), dropEvent)
		a.voiceEngineEvent(nil, finishedEvent("vs-7", 320), dropEvent)
		awaitDrained(t, h)
		if len(f.synthed()) != 0 {
			t.Fatal("no synthesis for a failed wake")
		}
		if c := f.closes(); len(c) != 1 || !strings.Contains(c[0], "no reply: the model timed out") {
			t.Fatalf("the failure is the drain's explicit reason: %v", c)
		}
	})
	t.Run("a transcript withheld under SAFE still completes", func(t *testing.T) {
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "never asked", nil })
		h, f := newTrackedSession(a, "vs-8", true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.enterSafe("the test's SAFE")
		a.voiceEngineEvent(nil, finalEvent("vs-8", "secret"), dropEvent)
		if a.voiceSafeDropped.Load() == 0 {
			t.Fatal("under SAFE the transcript reaches no record")
		}
		a.voiceEngineEvent(nil, finishedEvent("vs-8", 320), dropEvent)
		awaitDrained(t, h)
		if c := f.closes(); len(c) != 1 || !strings.Contains(c[0], "withheld under SAFE") {
			t.Fatalf("the SAFE-withheld outcome still orders the drain: %v", c)
		}
		if len(f.synthed()) != 0 {
			t.Fatal("nothing withheld is answered")
		}
	})
}
