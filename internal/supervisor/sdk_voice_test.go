package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// A native T3 VOICE engine, built by the SDK, run by the real supervisor on
// the RESIDENT lane: the host opens a session, fixes the input's cutoff,
// drains it closed, and everything the engine says in between arrives as an
// event carrying the session's identity and a contiguous sequence. The
// engine is examples/voice-skel, taken at the commit test/sdk-compat.rev
// pins (the gate's sdkcompat stage builds it and names it in AII_SDK_VOICE);
// the control and event wire is the one the two repositories must agree on.
//
//	cd ../aii-plugin-sdk && go build -o /tmp/voice-skel ./examples/voice-skel/
//	AII_SDK_VOICE=/tmp/voice-skel go test -run TestSDKVoiceEngine ./internal/supervisor/
func TestSDKVoiceEngineRunsAResidentSessionUnderTheRealSupervisor(t *testing.T) {
	bin := os.Getenv("AII_SDK_VOICE")
	if bin == "" {
		t.Skip("set AII_SDK_VOICE to an SDK-built voice engine (see the comment above)")
	}
	s, err := Start(Spec{
		PluginID: "com.example.voice-skel", Argv: []string{bin}, SessionMode: true,
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
	}, sessionNopDisp{})
	if err != nil {
		t.Fatalf("the supervisor could not start the SDK-built voice engine: %v", err)
	}
	defer s.Close()
	frames, stdin, ok := s.SessionChannels()
	if !ok {
		t.Fatal("a running session-mode engine exposes its lane")
	}
	c := NewSessionClientFrames(frames, stdin, s.Dispatcher(), 64)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go c.Run(ctx)

	// The open names no audio, so the session is control-only; the engine's
	// readiness and two utterances (test_critical_burst) follow as events.
	res, err := c.Control(ctx, "speech.session.open", map[string]any{"session_id": "s1", "test_critical_burst": 2})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !hasVal(res, "state", "opening") || !hasVal(res, "session_id", "s1") || !hasKey(res, "accepted") {
		t.Fatalf("open must be admitted into opening for s1: %s", res)
	}

	// A drain with no input boundary is refused by the engine in its own
	// words; the refusal is an admission verdict, not a transport fault.
	_, err = c.Control(ctx, "speech.session.close", map[string]any{"mode": "drain"})
	var refused *SessionRefusedError
	if !errors.As(err, &refused) || !strings.Contains(string(refused.Err), "no finish_input cutoff") {
		t.Fatalf("a drain before the cutoff must be refused for want of one: %v", err)
	}

	if res, err = c.Control(ctx, "speech.session.finish_input", map[string]any{"stream_id": "mic", "end_sample": 16000}); err != nil || !hasKey(res, "accepted") {
		t.Fatalf("finish_input must be admitted: %v %s", err, res)
	}
	if res, err = c.Control(ctx, "speech.session.close", map[string]any{"mode": "drain", "reason": "done"}); err != nil || !hasVal(res, "state", "draining") {
		t.Fatalf("a drain close after the cutoff must be admitted into draining: %v %s", err, res)
	}

	type event struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Sequence  int64  `json:"sequence"`
		Text      string `json:"text"`
		Speaker   string `json:"speaker"`
		EndSample int64  `json:"end_sample"`
	}
	var got []event
	for len(got) == 0 || got[len(got)-1].Type != "session_end" {
		select {
		case raw := <-c.Events():
			var ev event
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatalf("an event that is not JSON: %s", raw)
			}
			got = append(got, ev)
		case <-ctx.Done():
			t.Fatalf("the session never ended; events so far: %+v", got)
		}
	}

	// Everything the engine said, in order, under one identity.
	want := []struct{ typ, text string }{
		{"session_ready", ""},
		{"transcript_final", "utterance 1"},
		{"transcript_final", "utterance 2"},
		{"transcript_final", "(the tail, finalized)"},
		{"input_finished", ""},
		{"session_end", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("the host heard %d events, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Type != w.typ || g.Text != w.text {
			t.Fatalf("event %d is %q %q, want %q %q (all: %+v)", i+1, g.Type, g.Text, w.typ, w.text, got)
		}
		if g.SessionID != "s1" {
			t.Fatalf("event %d carries session %q, want s1", i+1, g.SessionID)
		}
		if g.Sequence != int64(i+1) {
			t.Fatalf("event %d carries sequence %d: the sequence is contiguous from one (%+v)", i+1, g.Sequence, got)
		}
		if g.Type == "transcript_final" && g.Speaker != "speaker-1" {
			t.Fatalf("the speaker label did not travel with %q: %q", g.Text, g.Speaker)
		}
	}
	if got[4].EndSample != 16000 {
		t.Fatalf("input_finished names cutoff %d, want the one the host fixed (16000)", got[4].EndSample)
	}
	if c.Faulted() {
		t.Fatalf("the lane faulted: %s", c.FaultReason())
	}
	t.Logf("VOICE RESIDENT E2E: %d events, session s1, sequence 1..%d, ended by session_end", len(got), len(got))
}
