package app

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// observerWireRaw is observerWire with each event's params written as
// given: the real framed notification, supervisor, driver and observer.
func observerWireRaw(t *testing.T, a *App) func(params string) {
	t.Helper()
	frames := make(chan []byte, 16)
	c := pluginhosttest.SessionClientFrames(frames, io.Discard, 32)
	v := pluginhost.NewVoiceSession(c)
	ctx, cancel := context.WithCancel(context.Background())
	runDone, observed := make(chan struct{}), make(chan struct{})
	go func() { defer close(runDone); _ = c.Run(ctx) }()
	go func() { defer close(observed); a.observeEngine(nil, v) }()
	t.Cleanup(func() {
		close(frames)
		cancel()
		for _, ch := range []chan struct{}{runDone, observed} {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Error("the wire reader or the observer did not retire")
			}
		}
	})
	return func(params string) {
		frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":` + params + `}`)
	}
}

func TestThePageSeesNoFrameThatDoesNotDecode(t *testing.T) {
	cases := []struct {
		name, frame string
		notice      bool   // a final keeps its withheld notice
		logs        string // the line this side writes, when it decides
		applies     string // a lifecycle frame applies, its bad field read as empty
	}{
		{"an array", `[1]`, false, "", ""},
		{"null", `null`, false, "", ""},
		{"type as a number", `{"type":5,"session_id":"vs-wire","sequence":2}`, false, "", ""},
		{"a partial with sequence as a string", `{"type":"transcript_partial","session_id":"vs-wire","sequence":"2","text":"private words"}`, false, "", ""},
		{"a partial with sequence as a float", `{"type":"transcript_partial","session_id":"vs-wire","sequence":2.5,"text":"private words"}`, false, "", ""},
		{"a partial with text as a number", `{"type":"transcript_partial","session_id":"vs-wire","sequence":2,"text":5}`, false, "", ""},
		{"an observation with score as a string", `{"type":"speaker_observation","session_id":"vs-wire","sequence":2,"refers_to":1,"speaker":"Quinn","speaker_id":"quinn","decision":"known","score":"0.9"}`, false, "", ""},
		{"a final with text as a number", `{"type":"transcript_final","session_id":"vs-wire","sequence":2,"text":5,"speaker":"Quinn"}`, true, "", ""},
		{"a final with an invalid speaker track", `{"type":"transcript_final","session_id":"vs-wire","sequence":2,"text":"private words","speaker":"Quinn","track_id":"track-a","start_sample":10,"end_sample":5}`, true, "speaker track", ""},
		{"a failure with a numeric reason", `{"type":"failure","session_id":"vs-wire","sequence":2,"reason":5}`, false, "", pluginhost.EventFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			a := newVoiceApp(t)
			page := &pageLog{}
			a.voiceEventSink = page.enqueue
			turns, err := a.store.RecentTurnsIncludingSystem(100)
			if err != nil {
				t.Fatal(err)
			}
			send := observerWireRaw(t, a)
			send(c.frame)
			send(`{"type":"speech_start","session_id":"vs-wire","sequence":3}`)
			holdWait(t, "the valid event after the malformed frame", func() bool { return page.find(pluginhost.EventSpeechStart, 3) != nil })

			page.mu.Lock()
			got := append(page.evs[:0:0], page.evs...)
			page.mu.Unlock()
			want := []string{pluginhost.EventSpeechStart}
			if c.notice {
				want = []string{"transcript_withheld", pluginhost.EventSpeechStart}
			}
			if c.applies != "" {
				want = []string{c.applies, pluginhost.EventSpeechStart}
			}
			if len(got) != len(want) {
				t.Fatalf("the page saw %+v, want %v", got, want)
			}
			for i, ev := range got {
				if ev.Type != want[i] || ev.Text != "" || ev.Speaker != "" || ev.SpeakerID != "" || ev.Attribution != "" {
					t.Errorf("page event %d = %+v, want a bare %s", i, ev, want[i])
				}
			}
			if c.applies == pluginhost.EventFailure && got[0].Reason != "the engine reported a failure" {
				t.Errorf("a failure whose reason did not decode is shown with %q, want the host's generic line", got[0].Reason)
			}
			if c.notice && (got[0].SessionID != "vs-wire" || got[0].Reason == "") {
				t.Errorf("the withheld notice does not name its session and reason: %+v", got[0])
			}
			after, err := a.store.RecentTurnsIncludingSystem(100)
			if err != nil {
				t.Fatal(err)
			}
			a.heardHistory.mu.Lock()
			heard := len(a.heardHistory.rows)
			a.heardHistory.mu.Unlock()
			if len(after) != len(turns) || heard != 0 {
				t.Errorf("a withheld frame reached the record: %d turns before, %d after; %d heard rows", len(turns), len(after), heard)
			}
			if c.logs != "" {
				found := false
				for _, l := range logs.Lines() {
					found = found || strings.Contains(l, "voice.refusal") && strings.Contains(l, "vs-wire") && strings.Contains(l, c.logs)
				}
				if !found {
					t.Errorf("no voice.refusal line names the session and %q:\n%s", c.logs, logs.String())
				}
			}
		})
	}
}

// THE SESSION'S STATUS COUNTS WHAT WAS WITHHELD (Q11): the page's
// voice_session status asks the handle, which asks the driver for the
// session it holds.
func TestAVoiceSessionsStatusCountsItsMalformedFrames(t *testing.T) {
	h := &voiceHandle{id: "vs-1", v: &fakeEngineSession{current: "vs-1", malformed: 3}}
	if n := h.Malformed(); n != 3 {
		t.Fatalf("the session's status counts %d malformed frames, want the driver's 3", n)
	}
}
