package pluginhost

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// malformedRig is one open session "s1" on a mock engine, its events
// written raw onto the lane.
func malformedRig(t *testing.T) (*VoiceSession, *mockEngine) {
	t.Helper()
	h2gR, h2gW, _ := os.Pipe()
	g2hR, g2hW, _ := os.Pipe()
	t.Cleanup(func() { h2gW.Close(); g2hW.Close(); h2gR.Close(); g2hR.Close() })
	c := supervisor.NewSessionClient(g2hR, h2gW, nopDispatcher{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()
	eng := &mockEngine{}
	go eng.serve(h2gR, g2hW)
	v := NewVoiceSession(c)
	octx, ocancel := context.WithTimeout(ctx, 5*time.Second)
	defer ocancel()
	if err := v.Open(octx, "s1", nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	return v, eng
}

func nextObserved(t *testing.T, v *VoiceSession, what string) Event {
	t.Helper()
	select {
	case ev := <-v.Observe():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never reached the observer", what)
		return Event{}
	}
}

func TestAMalformedFrameIsWithheldCountedAndLoggedAndTheNextIsDelivered(t *testing.T) {
	cases := []struct {
		name, frame string
		final       bool   // a final keeps its withheld notice
		seq         int64  // the notice's sequence: the frame's, where it decoded
		logs        string // what the log line names besides the session
	}{
		{"an array", `[1,2]`, false, 0, "not a JSON object"},
		{"a string", `"transcript_final"`, false, 0, "not a JSON object"},
		{"a number", `7`, false, 0, "not a JSON object"},
		{"null", `null`, false, 0, "not a JSON object"},
		{"type as a number", `{"type":5,"session_id":"s1","sequence":99}`, false, 0, "sequence 99"},
		{"sequence as a string", `{"type":"transcript_final","session_id":"s1","sequence":"99","text":"stray words"}`, true, 0, `type "transcript_final"`},
		{"sequence as a float", `{"type":"transcript_final","session_id":"s1","sequence":99.5,"text":"stray words"}`, true, 0, `type "transcript_final"`},
		{"a partial with text as a number", `{"type":"transcript_partial","session_id":"s1","sequence":99,"text":5}`, false, 0, `type "transcript_partial", sequence 99`},
		{"an observation with score as a string", `{"type":"speaker_observation","session_id":"s1","sequence":99,"refers_to":1,"speaker":"Quinn","score":"0.9"}`, false, 0, `type "speaker_observation", sequence 99`},
		{"a final with a numeric track", `{"type":"transcript_final","session_id":"s1","sequence":99,"text":"stray words","track_id":5}`, true, 99, `type "transcript_final", sequence 99`},
		{"a session_end whose session does not decode", `{"type":"session_end","session_id":5,"sequence":99}`, false, 0, `type "session_end", sequence 99`},
		{"a failure whose type does not decode", `{"type":["failure"],"session_id":"s1","sequence":99,"reason":"x"}`, false, 0, "sequence 99"},
		{"a cancellation with a numeric synthesis", `{"type":"cancellation","session_id":"s1","sequence":99,"synthesis_id":4}`, false, 0, `type "cancellation", sequence 99`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			v, eng := malformedRig(t)
			eng.emit(`{"type":"session_ready","session_id":"s1","sequence":1}`)
			if ev := nextObserved(t, v, "the first valid event"); ev.Type != EventSessionReady || ev.Sequence != 1 {
				t.Fatalf("first event = %+v", ev)
			}
			eng.emit(c.frame)
			eng.emit(`{"type":"speech_start","session_id":"s1","sequence":3}`)
			ev := nextObserved(t, v, "the event after the malformed frame")
			if c.final {
				if ev.Type != EventTranscriptFinal || ev.SessionID != "s1" || ev.Sequence != c.seq || ev.Raw != nil {
					t.Fatalf("a malformed final's notice = %+v (raw %q), want type, session and sequence %d with no body", ev, ev.Raw, c.seq)
				}
				ev = nextObserved(t, v, "the event after the notice")
			}
			if ev.Type != EventSpeechStart || ev.Sequence != 3 || !strings.Contains(string(ev.Raw), `"speech_start"`) {
				t.Fatalf("the malformed frame reached the observer, or the valid one did not follow it: %+v (raw %s)", ev, ev.Raw)
			}
			select {
			case extra := <-v.Telemetry():
				t.Fatalf("the malformed frame reached telemetry: %+v", extra)
			default:
			}
			// The watermark is the valid event's: the next one is delivered,
			// and a replay of it is not.
			eng.emit(`{"type":"speech_start","session_id":"s1","sequence":3}`)
			eng.emit(`{"type":"speech_start","session_id":"s1","sequence":4}`)
			if ev := nextObserved(t, v, "the event after the valid one"); ev.Sequence != 4 {
				t.Fatalf("after the watermark at 3, got %+v", ev)
			}
			if g := v.Gaps(); g != 1 {
				t.Errorf("gaps = %d, want 1: the engine's own numbering skipped 2", g)
			}
			if n := v.MalformedFor("s1"); n != 1 {
				t.Errorf("the session counts %d malformed frames, want 1", n)
			}
			if v.Faulted() || !v.IsOpen() {
				t.Errorf("a malformed frame changed the session: faulted=%v (%s) open=%v", v.Faulted(), v.FaultReason(), v.IsOpen())
			}
			select {
			case <-v.Untrusted():
				t.Error("a malformed frame made the session untrusted")
			case <-v.terminal():
				t.Error("a malformed frame ended the session")
			default:
			}
			var line string
			for _, l := range logs.Lines() {
				if strings.Contains(l, "voice.refusal") && strings.Contains(l, "does not decode") {
					line = l
				}
			}
			if line == "" || !strings.Contains(line, "session s1") || !strings.Contains(line, c.logs) {
				t.Errorf("the log line for the withheld frame = %q, want the session and %q", line, c.logs)
			}
		})
	}
}

func TestALifecycleFrameWithABadExplanationStillApplies(t *testing.T) {
	for _, c := range []struct {
		name, frame string
		ends        bool
		fault       string // FaultReason after it, when it ends
	}{
		{"a failure with a numeric reason", `{"type":"failure","session_id":"s1","sequence":2,"reason":5}`, true, "engine reported failure"},
		{"a session_end with a numeric reason", `{"type":"session_end","session_id":"s1","sequence":2,"reason":7}`, true, ""},
		{"a session-scoped cancellation with a numeric reason", `{"type":"cancellation","session_id":"s1","sequence":2,"reason":7}`, true, ""},
		{"a synthesis_start with a string stream", `{"type":"synthesis_start","session_id":"s1","sequence":2,"synthesis_id":"syn-1","output_stream":"1"}`, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			v, eng := malformedRig(t)
			eng.emit(c.frame)
			ev := nextObserved(t, v, "the lifecycle frame")
			if ev.Sequence != 2 || string(ev.Raw) != c.frame {
				t.Fatalf("the frame did not apply as it decoded: %+v", ev)
			}
			if c.ends {
				select {
				case <-v.terminal():
				case <-time.After(5 * time.Second):
					t.Fatal("a mistyped explanation hid the session's end")
				}
				if v.IsOpen() || v.FaultReason() != c.fault {
					t.Errorf("after it: open=%v fault=%q, want closed with %q", v.IsOpen(), v.FaultReason(), c.fault)
				}
			} else if !v.IsOpen() || v.Faulted() || v.SynthesisFor(1) != "" {
				t.Errorf("a synthesis_start with a mistyped stream: open=%v faulted=%v bound=%q", v.IsOpen(), v.Faulted(), v.SynthesisFor(1))
			}
			if n := v.MalformedFor("s1"); n != 0 {
				t.Errorf("an applied frame was counted as withheld: %d", n)
			}
			if !logs.Contains("voice.refusal: session s1") || !logs.Contains("read as empty") {
				t.Errorf("the refused field was not logged:\n%s", logs.String())
			}
		})
	}
}

func TestAMalformedInputCompletionIsReconciledFromStatusOrFaults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Run("status carries the completion", func(t *testing.T) {
		v, eng := malformedRig(t)
		eng.completion.Store(`{"stream_id":"mic","end_sample":320,"processed_end_sample":320,"sequence":2}`)
		if err := v.FinishInput(ctx, "mic", 320); err != nil {
			t.Fatal(err)
		}
		eng.emit(`{"type":"input_finished","session_id":"s1","sequence":2,"stream_id":"mic","end_sample":"320"}`)
		ev := nextObserved(t, v, "the reconciled completion")
		if ev.Type != EventInputFinished {
			t.Fatalf("the drain's word is not the completion: %+v", ev)
		}
		if done, end := v.InputFinished(); !done || end != 320 || v.Faulted() {
			t.Fatalf("finished=%v end=%d faulted=%v (%s)", done, end, v.Faulted(), v.FaultReason())
		}
		if n := v.MalformedFor("s1"); n != 1 {
			t.Errorf("the withheld frame counts %d, want 1", n)
		}
	})
	t.Run("status has nothing to give", func(t *testing.T) {
		v, eng := malformedRig(t)
		if err := v.FinishInput(ctx, "mic", 320); err != nil {
			t.Fatal(err)
		}
		eng.emit(`{"type":"input_finished","session_id":"s1","sequence":2,"stream_id":"mic","end_sample":"320"}`)
		select {
		case <-v.Untrusted():
		case <-time.After(5 * time.Second):
			t.Fatal("a completion nobody could read left the lane trusted")
		}
		if !strings.Contains(v.FaultReason(), "input_finished without an exact cutoff") {
			t.Fatalf("fault = %q", v.FaultReason())
		}
		if n := v.MalformedFor("s1"); n != 1 {
			t.Errorf("the withheld frame counts %d, want 1", n)
		}
	})
}
