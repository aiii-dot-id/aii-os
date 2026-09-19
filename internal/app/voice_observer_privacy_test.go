package app

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
func observerWire(t *testing.T, a *App) (func(string, int64), <-chan struct{}) {
	t.Helper()
	frames := make(chan []byte, 16)
	c := supervisor.NewSessionClientFrames(frames, io.Discard, nil, 32)
	v := pluginhost.NewVoiceSession(c)
	ctx, cancel := context.WithCancel(context.Background())
	runDone, observed := make(chan struct{}), make(chan struct{})
	go func() { defer close(runDone); _ = c.Run(ctx) }()
	go func() { defer close(observed); a.observeEngine(nil, v) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Error("the wire reader did not retire")
		}
		select {
		case <-observed:
		case <-time.After(2 * time.Second):
			t.Error("the application observer did not retire")
		}
	})
	return func(typ string, seq int64) {
		if typ == "EOF" {
			close(frames)
			return
		}
		raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{
			"type": typ, "session_id": "vs-wire", "sequence": seq, "text": "excluded private words",
		}})
		if err != nil {
			t.Fatal(err)
		}
		frames <- raw
	}, observed
}

func TestRestrictedPartialsAreWithheldOnTheRealTelemetryRoute(t *testing.T) {
	for _, mode := range []string{"all", "only", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: mode, UIDs: []string{"james"}}
			if mode == "all" {
				a.cfg.Speech.Speakers.UIDs = nil
			}
			page := &pageLog{}
			a.voiceEventSink = page.enqueue
			send, _ := observerWire(t, a)
			send("transcript_partial", 1)
			// .
			// .
			send("vad_probability", 2)
			holdWait(t, "the telemetry barrier", func() bool { return page.find("vad_probability", 2) != nil })
			got := page.find("transcript_partial", 1)
			if mode == "all" {
				if got == nil || got.Text != "excluded private words" {
					t.Fatalf("unrestricted speech disappeared: %+v", got)
				}
			} else {
				if got != nil {
					t.Errorf("restricted telemetry leaked to the page: %q", got.Text)
				}
				page.mu.Lock()
				for _, event := range page.evs {
					if event.Text != "" || event.Speaker != "" {
						t.Errorf("non-transcript telemetry leaked private fields: %+v", event)
					}
				}
				page.mu.Unlock()
				if n := a.speakerWithheldPartials.Load(); n != 1 {
					t.Errorf("withheld partials = %d, want 1", n)
				}
			}
			send("EOF", 0)
		})
	}
}

func TestVoiceObserverRetiresAndReportsADeadTransport(t *testing.T) {
	a := newVoiceApp(t)
	page := &pageLog{}
	a.voiceEventSink = page.enqueue
	send, observed := observerWire(t, a)
	send("transcript_partial", 1)
	send("vad_probability", 2)
	holdWait(t, "telemetry before transport loss", func() bool { return page.find("vad_probability", 2) != nil })
	send("EOF", 0)
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("transport ended but the application's voice observer never retired or reported failure")
	}
	failed := page.find("failed", 0)
	if failed == nil || !strings.Contains(failed.Reason, "transport ended") {
		t.Fatalf("lost transport must not be reported as healthy idle: %+v", failed)
	}
	if page.find("closed", 0) != nil {
		t.Fatal("transport loss was reported as a clean close")
	}
	// .
	// .
	types := page.types()
	if len(types) != 3 || types[0] != "transcript_partial" || types[1] != "vad_probability" || types[2] != "failed" {
		t.Fatalf("observer drain order = %v", types)
	}
}

func TestVoiceObserverDrainsAndRetiresAfterCleanEngineEnd(t *testing.T) {
	a := newVoiceApp(t)
	page := &pageLog{}
	a.voiceEventSink = page.enqueue
	send, observed := observerWire(t, a)
	send("transcript_partial", 1)
	send("vad_probability", 2)
	holdWait(t, "telemetry before clean end", func() bool { return page.find("vad_probability", 2) != nil })
	send("session_end", 3)
	send("EOF", 0)
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("clean engine end stranded the application's observer")
	}
	if page.find("failed", 0) != nil || page.find("closed", 0) == nil || page.find("session_end", 3) == nil {
		t.Fatalf("a clean engine end must drain its evidence and close cleanly: %v", page.types())
	}
	types := page.types()
	if len(types) != 4 || types[len(types)-1] != "closed" {
		t.Fatalf("terminal preceded queued events: %v", types)
	}
}

func TestTelemetryUsesTheCurrentPolicyAndNeverReplaysWithheldWords(t *testing.T) {
	a := newVoiceApp(t)
	page := &pageLog{}
	a.voiceEventSink = page.enqueue
	send, _ := observerWire(t, a)
	persist := func(*Config) (bool, error) { return true, nil }
	set := func(mode string) {
		t.Helper()
		if _, err := a.applyConfigChangeWith(map[string]any{"speech.speakers": map[string]any{"mode": mode}}, persist); err != nil {
			t.Fatal(err)
		}
	}
	send("transcript_partial", 1)
	send("vad_probability", 2)
	holdWait(t, "unrestricted partial", func() bool { return page.find("vad_probability", 2) != nil })
	set("only")
	send("transcript_partial", 3)
	send("vad_probability", 4)
	holdWait(t, "restricted partial", func() bool { return page.find("vad_probability", 4) != nil })
	set("all")
	send("transcript_partial", 5)
	send("vad_probability", 6)
	holdWait(t, "relaxed partial", func() bool { return page.find("vad_probability", 6) != nil })
	if page.find("transcript_partial", 1) == nil || page.find("transcript_partial", 5) == nil || page.find("transcript_partial", 3) != nil {
		t.Fatalf("telemetry ignored a policy change or replayed withheld words: %v", page.types())
	}
	if n := a.speakerWithheldPartials.Load(); n != 1 {
		t.Fatalf("withheld count = %d, want 1", n)
	}
	send("EOF", 0)
}

func TestLifecycleTelemetryDoesNotSmuggleTranscriptFields(t *testing.T) {
	for _, typ := range []string{"vad_probability", "session_ready", "turn_started", "session_end", "future_event"} {
		ev := pluginhost.Event{Type: typ, SessionID: "vs-wire", Raw: []byte(`{"text":"private utterance","speaker":"private label"}`)}
		got, shown := voiceEventFor(ev, false)
		if !shown || got.Type != typ {
			t.Fatalf("lifecycle evidence disappeared: %+v %v", got, shown)
		}
		if got.Text != "" || got.Speaker != "" {
			t.Errorf("%s smuggled transcript fields across the privacy boundary: %+v", typ, got)
		}
	}
}
