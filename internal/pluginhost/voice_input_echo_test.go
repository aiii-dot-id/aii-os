package pluginhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// echoLines is what the capture holds of the two echo lines.
func echoLines(c *logsink.Capture) (mismatch, foreign []string) {
	for _, l := range c.Lines() {
		switch {
		case strings.Contains(l, "reports reading its input from stream"):
			mismatch = append(mismatch, l)
		case strings.Contains(l, "input frames that came on "):
			foreign = append(foreign, l)
		}
	}
	return mismatch, foreign
}

func echoStatus(t *testing.T, h *streamHarness, input string) Snapshot {
	t.Helper()
	h.eng.setEcho(input)
	snap, err := h.v.Status(h.ctx)
	if err != nil {
		t.Fatalf("status with input %s: %v", input, err)
	}
	if h.v.Label() == "Failed" {
		t.Fatalf("status with input %s left the session Failed: the echo is log only", input)
	}
	return snap
}

func TestTheEnginesInputStreamEchoIsCheckedAgainstTheOpen(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	h := newStreamHarness(t, true)
	if err := h.openSpeaking(t, "A", newHeldSource(0x10, 2)); err != nil {
		t.Fatalf("open A: %v", err)
	}
	o, _ := h.eng.open("A")
	declared := o.stream

	echoStatus(t, h, fmt.Sprintf(`{"state":"accepting","stream":%d,"foreign_frames":0}`, declared))
	if m, f := echoLines(capture); len(m)+len(f) != 0 {
		t.Fatalf("the declared stream and no foreign frames log nothing: %q %q", m, f)
	}

	wrong := declared + 7
	for i := 0; i < 3; i++ { // polled three times, said once
		echoStatus(t, h, fmt.Sprintf(`{"state":"accepting","stream":%d}`, wrong))
	}
	m, _ := echoLines(capture)
	if len(m) != 1 {
		t.Fatalf("a mismatching stream is logged once per change, got %d lines: %q", len(m), m)
	}
	if !strings.Contains(m[0], "session A") || !strings.Contains(m[0], fmt.Sprintf("stream %d", wrong)) || !strings.Contains(m[0], fmt.Sprintf("declared stream %d", declared)) {
		t.Fatalf("the line names the session, what the engine reads and what the open declared: %q", m[0])
	}
	// Agreement again, then the same wrong number: news again.
	echoStatus(t, h, fmt.Sprintf(`{"state":"accepting","stream":%d}`, declared))
	echoStatus(t, h, fmt.Sprintf(`{"state":"accepting","stream":%d}`, wrong))
	if m, _ := echoLines(capture); len(m) != 2 {
		t.Fatalf("a mismatch after agreement is told again, got %q", m)
	}
	// A value that is not a stream number is not the declared one either.
	echoStatus(t, h, `{"state":"accepting","stream":"one"}`)
	if m, _ := echoLines(capture); len(m) != 3 {
		t.Fatalf("a non-number stream is a mismatch too, got %q", m)
	}
	// The engine stops reporting the field: nothing to check, nothing said.
	echoStatus(t, h, `{"state":"accepting"}`)
	echoStatus(t, h, `{"state":"accepting","stream":null}`)
	if m, _ := echoLines(capture); len(m) != 3 {
		t.Fatalf("an absent or null stream after a mismatch is silent, got %q", m)
	}
	if h.v.Label() == "Failed" {
		t.Fatal("a mismatch never faults the session")
	}
}

func TestTheEnginesForeignFrameCountIsLoggedOncePerIncrease(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	h := newStreamHarness(t, true)
	if err := h.openSpeaking(t, "A", newHeldSource(0x10, 2)); err != nil {
		t.Fatalf("open A: %v", err)
	}
	o, _ := h.eng.open("A")
	at := func(n int) string {
		return fmt.Sprintf(`{"state":"accepting","stream":%d,"foreign_frames":%d}`, o.stream, n)
	}
	for _, n := range []int{0, 3, 3, 3, 5, 5, 2} {
		echoStatus(t, h, at(n))
	}
	m, f := echoLines(capture)
	if len(m) != 0 {
		t.Fatalf("the declared stream is no mismatch: %q", m)
	}
	if len(f) != 2 {
		t.Fatalf("3 then 5 are two increases — one line each, nothing for repeats or a lower count; got %q", f)
	}
	if !strings.Contains(f[0], "dropped 3 input frames") || !strings.Contains(f[1], "dropped 5 input frames") || !strings.Contains(f[1], "(2 since last told)") {
		t.Fatalf("each line says the count and the growth: %q", f)
	}

	// The next session starts its own count: 4 is news for it.
	h.abort(t)
	if err := h.openSpeaking(t, "B", newHeldSource(0x80, 2)); err != nil {
		t.Fatalf("open B: %v", err)
	}
	o, _ = h.eng.open("B")
	echoStatus(t, h, at(4))
	if _, f := echoLines(capture); len(f) != 3 || !strings.Contains(f[2], "session B") {
		t.Fatalf("a new session's foreign count is told from zero: %q", f)
	}
}

// An engine that does not report the echo — the field absent, the input
// object absent, or null — is silent: nothing to check, nothing logged.
func TestAnEngineWithoutTheInputEchoLogsNothing(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	h := newStreamHarness(t, true)
	if err := h.openSpeaking(t, "A", newHeldSource(0x10, 2)); err != nil {
		t.Fatalf("open A: %v", err)
	}
	for _, input := range []string{
		``,
		`{"state":"accepting"}`,
		`{"state":"accepting","stream":null,"foreign_frames":null}`,
	} {
		echoStatus(t, h, input)
	}
	if m, f := echoLines(capture); len(m)+len(f) != 0 {
		t.Fatalf("an engine without the echo logs nothing: %q %q", m, f)
	}
}

// A session that declared no input stream, a speaker-only one, has no
// number to name. An engine that drops an earlier session's unread frames
// for it reports the count beside a null stream, and the line says the
// session declared none, where it said "other than the declared 0".
func TestForeignFramesOnASessionThatDeclaredNoStreamNameNoNumber(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	h := newStreamHarness(t, true)
	plane := audio.NewPlane()
	_ = plane.Register(&audio.Endpoint{ID: "speaker", Sink: audio.NewCaptureSink(mono16k)})
	ob, err := plane.BindOutput("out", "speaker", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.OpenWithAudio(h.ctx, "out", ob, nil); err != nil {
		t.Fatalf("output-only open: %v", err)
	}
	echoStatus(t, h, `{"state":"accepting","stream":null,"foreign_frames":2}`)
	m, f := echoLines(capture)
	if len(m) != 0 || len(f) != 1 {
		t.Fatalf("a null stream and two foreign frames are one line: mismatch %q, foreign %q", m, f)
	}
	if !strings.Contains(f[0], "session out") || !strings.Contains(f[0], "dropped 2 input frames") || !strings.Contains(f[0], "this session declared none") || strings.Contains(f[0], "declared 0") {
		t.Fatalf("the line for a session that declared no stream: %q", f[0])
	}
}
