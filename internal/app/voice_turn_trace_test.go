package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// voice_turn_trace_test.go — every spoken turn leaves ONE voice.end
// VOICE_TURN line with the host's time at each stage, or "not reached" and
// why. Model-free: the clock is stepped
// by the test, the turn is a stand-in, the engine a fake.

// stepClock is the trace's clock in a test's hands. Each reading answers
// the present and then moves it by the next of steps, so stages no test
// can stand between (a final and its release, in one call of the observer)
// are still told apart; advance moves it by hand. A test takes it before it
// makes its App, so the clock is given back after that App has stopped.
type stepClock struct {
	mu    sync.Mutex
	now   time.Time
	steps []time.Duration
}

func stepVoiceClock(t *testing.T, stepsMS ...int) *stepClock {
	t.Helper()
	c := &stepClock{now: time.Date(2026, 10, 11, 16, 0, 0, 0, time.UTC)}
	for _, ms := range stepsMS {
		c.steps = append(c.steps, time.Duration(ms)*time.Millisecond)
	}
	prev := voiceNow
	voiceNow = c.read
	t.Cleanup(func() { voiceNow = prev })
	return c
}

func (c *stepClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := c.now
	if len(c.steps) > 0 {
		c.now, c.steps = c.now.Add(c.steps[0]), c.steps[1:]
	}
	return at
}

func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// turnLines is every VOICE_TURN line the log holds.
func turnLines(logs *logsink.Capture) []string {
	var out []string
	for _, l := range logs.Lines() {
		if strings.Contains(l, "voice.end: VOICE_TURN ") {
			out = append(out, l)
		}
	}
	return out
}

// oneTurnLine waits for the turn's line and holds it to being the only one.
func oneTurnLine(t *testing.T, logs *logsink.Capture) string {
	t.Helper()
	holdWait(t, "the turn's VOICE_TURN line", func() bool { return len(turnLines(logs)) > 0 })
	lines := turnLines(logs)
	if len(lines) != 1 {
		t.Fatalf("a spoken turn leaves exactly one VOICE_TURN line, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	t.Log(lines[0])
	return lines[0]
}

var stageInLine = regexp.MustCompile(` (final|released|turn_start|reply|enqueued|acknowledged|synthesis_start|first_frame) (\d+ ms|not reached)`)

// stagesOf reads a VOICE_TURN line back: each stage's milliseconds, or -1
// for "not reached". A line that does not name every stage once, in order,
// fails the test.
func stagesOf(t *testing.T, line string) [voiceStages]int {
	t.Helper()
	var out [voiceStages]int
	found := stageInLine.FindAllStringSubmatch(line[strings.Index(line, ":"):], -1)
	if len(found) != int(voiceStages) {
		t.Fatalf("the line names %d stage(s), want %d: %s", len(found), voiceStages, line)
	}
	for i, m := range found {
		if m[1] != voiceStageNames[i] {
			t.Fatalf("stage %d is %q, want %q: %s", i, m[1], voiceStageNames[i], line)
		}
		out[i] = -1
		if m[2] != "not reached" {
			out[i], _ = strconv.Atoi(strings.TrimSuffix(m[2], " ms"))
		}
	}
	return out
}

func synthesisRaw(typ, session, synth string, stream int) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": typ, "session_id": session, "synthesis_id": synth, "output_stream": stream})
	return pluginhost.Event{Type: typ, SessionID: session, Raw: raw}
}

// tracedSink is a session's output as OpenVoiceSession wraps it, over a
// sink that keeps what it is given, with the fake engine naming stream as
// synth's.
func tracedSink(a *App, h *voiceHandle, f *fakeEngineSession, stream uint32, synth string) (*firstFrameSink, *audio.CaptureSink) {
	f.mu.Lock()
	f.streams = map[uint32]string{stream: synth}
	f.mu.Unlock()
	kept := audio.NewCaptureSink(mono16k)
	return &firstFrameSink{Sink: kept, wrote: func(fr audio.Frame, at time.Time) { a.voiceFrameWritten(h.id, fr, at) }}, kept
}

// admittedSynthesis waits for the heard words' turn to settle and answers
// the synthesis it left in flight.
func admittedSynthesis(t *testing.T, h *voiceHandle) string {
	t.Helper()
	awaitWork(t, h)
	synth := h.loadInflight().id
	if synth == "" {
		t.Fatal("the turn's reply was not admitted to the engine")
	}
	return synth
}

// A SPOKEN FINAL THAT IS ANSWERED AND SPOKEN: one line, every stage a
// number, and the differences between stages are the amounts the clock was
// stepped by between them.
func TestASpokenTurnLeavesOneLineWithEveryStageTimed(t *testing.T) {
	ctx := context.Background()
	stepVoiceClock(t, 10, 20, 30, 40, 50, 60, 70)
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	stubWake(t, func() (string, error) { return "an answer", nil })
	h, f := newTrackedSession(a, "vs-trace", true)

	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "a private question"), dropEvent)
	synth := admittedSynthesis(t, h)
	if got := turnLines(logs); len(got) != 0 {
		t.Fatalf("the line was written before the first frame: %v", got)
	}
	a.voiceEngineEvent(nil, synthesisRaw(pluginhost.EventSynthesisStart, h.id, synth, 1), dropEvent)
	sink, kept := tracedSink(a, h, f, 1, synth)
	frames := []audio.Frame{
		{Kind: audio.KindPCM, Stream: 1, Seq: 1, PCM: make([]byte, 640)},
		{Kind: audio.KindPCM, Stream: 1, Seq: 2, Start: 320, PCM: make([]byte, 640)},
		{Kind: audio.KindEnd, Stream: 1, Seq: 3, Start: 640},
	}
	for _, fr := range frames {
		if err := sink.Write(ctx, fr); err != nil {
			t.Fatal(err)
		}
	}
	line := oneTurnLine(t, logs)
	if !strings.Contains(line, "VOICE_TURN session vs-trace final 7:") {
		t.Fatalf("the line does not name its session and final: %s", line)
	}
	if strings.Contains(line, "private question") || strings.Contains(line, "an answer") {
		t.Fatalf("the line carries the words: %s", line)
	}
	want := [voiceStages]int{0, 10, 30, 60, 100, 150, 210, 280}
	if got := stagesOf(t, line); got != want {
		t.Fatalf("stage times %v, want %v (the clock's steps): %s", got, want, line)
	}
	if strings.Contains(line, "not reached") {
		t.Fatalf("a turn that was spoken reached every stage: %s", line)
	}
	// The observer changes no frame: the sink was given exactly these.
	if got := kept.Frames(); len(got) != len(frames) || got[1].Start != 320 || got[2].Kind != audio.KindEnd {
		t.Fatalf("the sink was not given the frames it was written: %+v", got)
	}
}

// A FINAL HELD FOR ITS SPEAKER: released minus final is the hold.
func TestAHeldFinalsLineShowsTheHoldForItsSpeaker(t *testing.T) {
	clock := stepVoiceClock(t)
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"quinn-one"}, Revision: 3}
	logs := logsink.CaptureForTest(t)
	stubWake(t, func() (string, error) { return "an answer", nil })
	h, f := newTrackedSession(a, "vs-held", true)

	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "hello there"), dropEvent)
	clock.advance(1234 * time.Millisecond)
	a.voiceEngineEvent(nil, observationRaw(h.id, 12, map[string]any{"refers_to": 7, "speaker": "Quinn", "speaker_id": "quinn-one", "decision": "known", "score": 0.93}), dropEvent)
	synth := admittedSynthesis(t, h)
	sink, _ := tracedSink(a, h, f, 1, synth)
	if err := sink.Write(context.Background(), audio.Frame{Kind: audio.KindPCM, Stream: 1, PCM: make([]byte, 640)}); err != nil {
		t.Fatal(err)
	}
	line := oneTurnLine(t, logs)
	got := stagesOf(t, line)
	if got[stageFinal] != 0 || got[stageReleased] != 1234 {
		t.Fatalf("released minus final must be the 1234 ms the final was held: %s", line)
	}
	// No synthesis_start was sent: the stage says so, and the frame still counts.
	if got[stageSynthesisStart] != -1 || got[stageFirstFrame] != 1234 {
		t.Fatalf("synthesis_start must be not reached and first_frame timed: %s", line)
	}
}

// A TURN WITH NOTHING TO SPEAK STILL LEAVES ITS LINE: the speech stages say
// "not reached", and why.
func TestATurnWithNothingToSpeakSaysWhatWasNotReachedAndWhy(t *testing.T) {
	for _, c := range []struct {
		name, reply, why string
		speakOff         bool
	}{
		{"text only: speak is off", "an answer", replyNotSpokenOutcome, true},
		{"the turn answered with silence", "  ", "the turn ended with no reply to speak", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			stepVoiceClock(t, 10, 20, 30)
			a := newVoiceApp(t)
			if c.speakOff {
				a.publishVoiceMode(VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 1})
			}
			logs := logsink.CaptureForTest(t)
			stubWake(t, func() (string, error) { return c.reply, nil })
			h, f := newTrackedSession(a, "vs-quiet", true)

			a.voiceEngineEvent(nil, finalRaw(h.id, 3, "words"), dropEvent)
			awaitWork(t, h)
			line := oneTurnLine(t, logs)
			want := [voiceStages]int{0, 10, 30, 60, -1, -1, -1, -1}
			if got := stagesOf(t, line); got != want {
				t.Fatalf("stage times %v, want %v: %s", got, want, line)
			}
			if !strings.HasSuffix(line, "; not reached: "+c.why) {
				t.Fatalf("the line does not say why nothing was spoken (%q): %s", c.why, line)
			}
			if len(f.synthed()) != 0 {
				t.Fatalf("nothing was to be enqueued: %v", f.synthed())
			}
		})
	}
}

// A SYNTHESIS STOPPED BEFORE ITS FIRST FRAME: one line, first_frame not
// reached, by the operator's barge-in and by the reply that replaces it.
func TestASynthesisStoppedBeforeItsFirstFrameLeavesItsLine(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, why string
		stop      func(*App, *voiceHandle)
	}{
		{"fenced by the operator's barge-in", "operator", func(_ *App, h *voiceHandle) {
			if err := h.Interrupt(ctx); err != nil {
				t.Fatal(err)
			}
		}},
		{"superseded by a newer reply", "a newer reply took its place", func(a *App, h *voiceHandle) {
			if got := a.synthesizeReply(ctx, h.id, h.gen.Load(), "a newer answer"); got != replyAdmitted {
				t.Fatalf("the newer reply was not admitted: %d", got)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			stepVoiceClock(t)
			a := newVoiceApp(t)
			logs := logsink.CaptureForTest(t)
			stubWake(t, func() (string, error) { return "an answer", nil })
			h, f := newTrackedSession(a, "vs-stopped", true)

			a.voiceEngineEvent(nil, finalRaw(h.id, 5, "words"), dropEvent)
			synth := admittedSynthesis(t, h)
			a.voiceEngineEvent(nil, synthesisRaw(pluginhost.EventSynthesisStart, h.id, synth, 1), dropEvent)
			c.stop(a, h)
			line := oneTurnLine(t, logs)
			got := stagesOf(t, line)
			if got[stageSynthesisStart] != 0 || got[stageFirstFrame] != -1 {
				t.Fatalf("synthesis_start must be timed and first_frame not reached: %s", line)
			}
			if !strings.HasSuffix(line, "; not reached: its synthesis was fenced before any frame reached the sink: "+c.why) {
				t.Fatalf("the line does not say what stopped the synthesis: %s", line)
			}
			// A frame that arrives after the fence writes no second line.
			sink, _ := tracedSink(a, h, f, 1, synth)
			if err := sink.Write(ctx, audio.Frame{Kind: audio.KindPCM, Stream: 1, PCM: make([]byte, 640)}); err != nil {
				t.Fatal(err)
			}
			oneTurnLine(t, logs)
		})
	}
}

// A REPLY TO WORDS THAT WERE TYPED has no final behind it: its trace begins
// at the reply.
func TestATypedRepliesLineBeginsAtTheReply(t *testing.T) {
	stepVoiceClock(t, 15, 25)
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	h, f := outputHandle(t, a, "vs-typed")

	a.settleVoice(context.Background(), "an answer to typed words")
	synth := h.loadInflight().id
	if synth == "" {
		t.Fatal("the typed reply was not admitted to the output session")
	}
	sink, _ := tracedSink(a, h, f, 4, synth)
	if err := sink.Write(context.Background(), audio.Frame{Kind: audio.KindEnd, Stream: 4}); err != nil {
		t.Fatal(err)
	}
	line := oneTurnLine(t, logs)
	want := [voiceStages]int{-1, -1, -1, 0, 15, 40, -1, -1}
	if got := stagesOf(t, line); got != want {
		t.Fatalf("stage times %v, want %v: %s", got, want, line)
	}
	if !strings.Contains(line, "VOICE_TURN session vs-typed reply to typed words:") ||
		!strings.HasSuffix(line, "; not reached: its output stream ended at the sink with no audio frame") {
		t.Fatalf("a typed reply whose stream had no audio: %s", line)
	}
}

// THE FIRST STAMP WINS, AND THE LINE IS WRITTEN ONCE.
func TestATraceKeepsItsFirstStampAndWritesOneLine(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	clock := stepVoiceClock(t)
	tr := newVoiceTurnTrace("vs-once", 9, stageFinal)
	clock.advance(40 * time.Millisecond)
	tr.stamp(stageReleased)
	clock.advance(time.Second)
	tr.stamp(stageReleased) // a second path passes the same stage
	tr.end("first")
	tr.end("second")
	tr.settle("third")
	line := oneTurnLine(t, logs)
	if got := stagesOf(t, line); got[stageReleased] != 40 || !strings.HasSuffix(line, "; not reached: first") {
		t.Fatalf("the first stamp and the first end must win: %s", line)
	}
	var none *voiceTurnTrace // a turn nobody traces stamps and writes nothing
	none.stamp(stageReply)
	none.settle("nothing")
	none.end("nothing")
	oneTurnLine(t, logs)
}

// THE OBSERVER IS IN PLACE WHEN A SESSION OPENS, and gives the page's sink
// every frame unchanged.
func TestAnOpenedSessionWritesItsOutputThroughTheFirstFrameObserver(t *testing.T) {
	a := speechRouteApp(t, "", "")
	runningEngine(t, a, "id.engine.only")
	opensReach(t)
	h, err := openSpeaking(a, "spk")
	if err != nil {
		t.Fatal(err)
	}
	seen, ok := h.b.Sink.(*firstFrameSink)
	if !ok {
		t.Fatalf("the session's output is written to %T, not through the first-frame observer", h.b.Sink)
	}
	kept, ok := seen.Sink.(*audio.CaptureSink)
	if !ok || seen.Format() != mono16k {
		t.Fatalf("the observer does not stand in front of the endpoint's own sink: %T %v", seen.Sink, seen.Format())
	}
	for i := 0; i < 3; i++ {
		fr := audio.Frame{Kind: audio.KindPCM, Stream: 2, Seq: uint32(i + 1), Start: int64(320 * i), PCM: []byte(fmt.Sprintf("%0640d", i))}
		if err := h.b.Sink.Write(context.Background(), fr); err != nil {
			t.Fatal(err)
		}
	}
	got := kept.Frames()
	if len(got) != 3 || got[2].Seq != 3 || got[2].Start != 640 || string(got[1].PCM) != fmt.Sprintf("%0640d", 1) {
		t.Fatalf("the sink was not given the frames as written: %d frame(s)", len(got))
	}
}

// heldLookup is an engine session whose naming of a stream's synthesis
// waits until the test lets it go: the driver's lock, held by someone else.
type heldLookup struct {
	engineSession
	asked chan struct{}
	gate  chan struct{}
	synth string
}

func (e *heldLookup) SynthesisFor(uint32) string {
	select {
	case e.asked <- struct{}{}:
	default:
	}
	<-e.gate
	return e.synth
}

// THE AUDIO PUMP WAITS FOR NOTHING THE CLOCK DOES. The first frame's stage
// needs the synthesis its stream belongs to, and naming it takes the
// driver's lock. A session's output is written through the observer as it
// is opened, and with that look-up held the frames of the reply still go to
// the sink at once; the stage, when the look-up returns, carries the time
// of the write and not the time of the look-up.
func TestTheFirstFrameStageNeverHoldsTheAudioPump(t *testing.T) {
	clock := stepVoiceClock(t)
	a := speechRouteApp(t, "", "")
	logs := logsink.CaptureForTest(t)
	runningEngine(t, a, "id.engine.only")
	opensReach(t)
	h, err := openSpeaking(a, "spk")
	if err != nil {
		t.Fatal(err)
	}
	held := &heldLookup{engineSession: h.v, asked: make(chan struct{}, 1), gate: make(chan struct{}), synth: "syn-held"}
	h.v = held
	trace := newVoiceTurnTrace(h.id, 4, stageFinal)
	clock.advance(100 * time.Millisecond)
	h.setInflight(held.synth, 0, trace)

	wrote := make(chan error, 1)
	go func() {
		var werr error
		for i := 0; i < 3 && werr == nil; i++ {
			werr = h.b.Sink.Write(context.Background(), audio.Frame{Kind: audio.KindPCM, Stream: 2, Seq: uint32(i + 1), Start: int64(320 * i), PCM: make([]byte, 640)})
		}
		wrote <- werr
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the reply's frames waited behind the look-up of whose stream it was")
	}
	select {
	case <-held.asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the first frame's look-up was never started")
	}
	if got := turnLines(logs); len(got) != 0 {
		t.Fatalf("the line was written before the look-up returned: %v", got)
	}
	// The clock moves on while the look-up is held: the stage must still
	// say when the frame was written.
	clock.advance(5 * time.Second)
	close(held.gate)
	stages := stagesOf(t, oneTurnLine(t, logs))
	if stages[stageFirstFrame] != 100 {
		t.Fatalf("first_frame is %d ms; the frame was written 100 ms after the final, and the look-up's wait is not the frame's", stages[stageFirstFrame])
	}
}

// A STREAM'S END IS TOLD ONLY WHEN NO AUDIO OF IT WAS. Each report runs as
// its own background job, so an end told after a played stream could run
// before the first frame's report and write "no audio frame" for a reply
// that played. A stream that played tells its first
// frame alone; one that ended with no audio tells its end.
func TestAStreamThatPlayedTellsNoEndAndOneThatDidNotTellsIt(t *testing.T) {
	var told []audio.Frame
	s := &firstFrameSink{Sink: audio.NewCaptureSink(mono16k), wrote: func(fr audio.Frame, _ time.Time) { told = append(told, fr) }}
	ctx := context.Background()
	pcm := func(stream uint32) audio.Frame {
		return audio.Frame{Kind: audio.KindPCM, Stream: stream, PCM: make([]byte, 320)}
	}
	for _, fr := range []audio.Frame{pcm(1), pcm(1), {Kind: audio.KindEnd, Stream: 1}, {Kind: audio.KindEnd, Stream: 2}} {
		if err := s.Write(ctx, fr); err != nil {
			t.Fatal(err)
		}
	}
	if len(told) != 2 || told[0].Kind != audio.KindPCM || told[0].Stream != 1 || told[1].Kind != audio.KindEnd || told[1].Stream != 2 {
		t.Fatalf("told %+v; want stream 1's first frame and stream 2's end only", told)
	}
}
