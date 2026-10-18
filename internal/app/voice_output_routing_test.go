package app

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// TYPED WORDS, THE ENGINE'S VOICE. In earbuds the operator types and the
// replies are spoken; with a speech engine active the voice is the
// engine's, which speaks only inside a session. A page holds an
// OUTPUT-ONLY session open for that — a speaker and no microphone — and a
// reply to words nobody spoke is bound to it when the reply exists, under
// exactly the custody a spoken turn's reply has.

// outputHandle is an output-only session as the application holds it: a
// binding with a speaker and no source, on a fake engine, opened on the
// engine chosen to speak.
func outputHandle(t *testing.T, a *App, id string) (*voiceHandle, *fakeEngineSession) {
	t.Helper()
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(audio.Format{Rate: 16000, Channels: 1})}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.BindOutput(id, "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngineSession{}
	h := &voiceHandle{id: id, v: f, b: b, done: make(chan struct{}), engine: speakingEngine(t, a)}
	a.voiceSessions.Store(id, h)
	a.voiceModes.Store(id, false)
	return h, f
}

// speakingEngine is the engine an output-only session is opened on: a
// live lane under its plugin id which, with nothing chosen to speak, is
// the default the speaking pointer selects (speechEngineFor).
func speakingEngine(t *testing.T, a *App) *pluginhost.ActivePlugin {
	t.Helper()
	if ap := a.speechEngineFor(""); ap != nil {
		return ap
	}
	c := pluginhosttest.SessionClientFrames(make(chan []byte, 4), io.Discard, 8)
	ap := pluginWithTestVoice(&pluginhost.ActivePlugin{ID: "id.test.voice", Title: "Test Voice"}, pluginhost.NewVoiceSession(c))
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, ap)
	a.pluginMu.Unlock()
	return ap
}

type sunkReplies struct {
	mu   sync.Mutex
	refs []dashboard.VoiceReplyRef
}

func (s *sunkReplies) sink(ref dashboard.VoiceReplyRef, _ string) {
	s.mu.Lock()
	s.refs = append(s.refs, ref)
	s.mu.Unlock()
}
func (s *sunkReplies) all() []dashboard.VoiceReplyRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]dashboard.VoiceReplyRef(nil), s.refs...)
}

func TestATypedReplyIsSpokenByTheEngineThroughTheOutputSession(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenOff, speakOn)
	_, f := outputHandle(t, a, "vs-out")
	var sunk sunkReplies
	a.voiceReplySink = sunk.sink

	a.settleVoice(context.Background(), "the answer to what was typed")

	f.mu.Lock()
	synths := append([]string(nil), f.synths...)
	f.mu.Unlock()
	if len(synths) != 1 || !strings.HasSuffix(synths[0], "|the answer to what was typed") {
		t.Fatalf("the reply was not synthesized on the output session: %v", synths)
	}
	refs := sunk.all()
	if len(refs) != 1 || refs[0].Route != "plugin" || refs[0].SessionID != "vs-out" || refs[0].SynthesisID == "" || refs[0].TextOnly {
		t.Fatalf("the page must be told the ENGINE speaks this reply, so no other voice does: %+v", refs)
	}
	if !a.voiceReplyShown.Swap(false) {
		t.Fatal("the reply reached every screen with its voice; the turn's own path must not print it a second time")
	}
}

func TestWithoutAnOutputSessionATypedReplyIsLeftAlone(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenOff, speakOn)
	var sunk sunkReplies
	a.voiceReplySink = sunk.sink

	a.settleVoice(context.Background(), "no engine voice is open")
	if n := len(sunk.all()); n != 0 || a.voiceReplyShown.Load() {
		t.Fatalf("with no output session the typed reply is not the voice path's: refs=%d shown=%v", n, a.voiceReplyShown.Load())
	}

	// A duplex conversation is open instead, and replies were not asked for
	// aloud.
	a.publishVoiceMode(VoiceModeConfig{}) // unset: auto
	plane := audio.NewPlane()
	_ = plane.Register(&audio.Endpoint{ID: "mic", Source: &liveSource{f: mono16k}})
	_ = plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)})
	b, err := plane.Bind("vs-talk", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngineSession{}
	a.voiceSessions.Store("vs-talk", &voiceHandle{id: "vs-talk", v: f, b: b, done: make(chan struct{})})
	a.settleVoice(context.Background(), "typed beside a conversation")
	f.mu.Lock()
	n := len(f.synths)
	f.mu.Unlock()
	if n != 0 || len(sunk.all()) != 0 {
		t.Fatalf("under auto a conversation's session was borrowed for a typed reply: synths=%d refs=%d", n, len(sunk.all()))
	}
}

// A NEWER REPLY TAKES THE VOICE OVER. An operator who is typing can ask
// again while the last answer is still being read out; what the engine was
// still speaking is fenced BY ITS OWN ID before the new reply is enqueued.
func TestANewerTypedReplyTakesTheVoiceOverFromTheOneStillSpeaking(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenOff, speakOn)
	_, f := outputHandle(t, a, "vs-out")
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}

	a.settleVoice(context.Background(), "a long first answer")
	first := f.producingNow()
	if first == "" {
		t.Fatal("fixture: the first reply is not being spoken")
	}
	a.settleVoice(context.Background(), "the answer they are waiting for")

	ops := f.opsSeen()
	fence, second := -1, -1
	for i, op := range ops {
		if op == "interrupt:"+first+":vs-out" && fence < 0 {
			fence = i
		}
		if op == "synthesize:vs-out" {
			second = i
		}
	}
	if fence < 0 || second < fence {
		t.Fatalf("the reply still speaking was not fenced by its id before the newer one was enqueued: %v", ops)
	}
	if now := f.producingNow(); now == "" || now == first {
		t.Fatalf("the newer reply is not the one being spoken: %q (first %q)", now, first)
	}
}

// SPEAK OFF IS STILL THE FIRST WORD, and an ended or closing output
// session is not a voice: the reply is text, under the session's own
// provenance where there is one, and nothing is synthesized.
func TestATypedReplyRespectsTheModeAndTheSessionsEnd(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenOff, speakOff)
	h, f := outputHandle(t, a, "vs-out")
	var sunk sunkReplies
	a.voiceReplySink = sunk.sink

	a.settleVoice(context.Background(), "replies are not spoken in this mode")
	f.mu.Lock()
	n := len(f.synths)
	f.mu.Unlock()
	refs := sunk.all()
	if n != 0 || len(refs) != 1 || !refs[0].TextOnly || refs[0].Route != "plugin" {
		t.Fatalf("under speak off the reply is text with the session's provenance: synths=%d refs=%+v", n, refs)
	}

	voiceModeDoor(t, a, listenOff, speakOn)
	h.closing.Store(true)
	a.voiceReplyShown.Store(false)
	a.settleVoice(context.Background(), "the session is closing")
	f.mu.Lock()
	n = len(f.synths)
	f.mu.Unlock()
	if n != 0 || a.voiceReplyShown.Load() {
		t.Fatalf("a closing output session was given a reply to speak: synths=%d", n)
	}

	// Silence is not a reply.
	h.closing.Store(false)
	a.settleVoice(context.Background(), "   ")
	f.mu.Lock()
	n = len(f.synths)
	f.mu.Unlock()
	if n != 0 {
		t.Fatalf("an empty reply was synthesized: %d", n)
	}
}

func TestAReplyIsFencedUntilItsPlaybackIsReported(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenOff, speakOn)
	h, f := outputHandle(t, a, "vs-out")
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}

	a.settleVoice(context.Background(), "the first answer")
	first := f.producingNow()
	if first == "" {
		t.Fatal("fixture: the first reply is not being spoken")
	}
	f.mu.Lock()
	f.streams = map[uint32]string{7: first}
	f.mu.Unlock()

	// PRODUCTION ENDS, and the page still holds what it was sent.
	a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "synthesis_end", SessionID: "vs-out",
		Raw: []byte(`{"type":"synthesis_end","session_id":"vs-out","synthesis_id":"` + first + `","output_stream":7,"playback_verified":false}`)}))
	if id := h.loadInflight().id; id != first {
		t.Fatalf("production ending gave up custody of a reply nobody has heard: %q", id)
	}
	a.settleVoice(context.Background(), "the answer they are waiting for")
	fenced := false
	for _, op := range f.opsSeen() {
		if op == "interrupt:"+first+":vs-out" {
			fenced = true
		}
	}
	if !fenced {
		t.Fatalf("the newer reply did not fence the one the page was still playing: %v", f.opsSeen())
	}

	// THE PAGE REPORTS IT PLAYED: that settles the reply, and the next one
	// has nothing to stop.
	second := f.producingNow()
	f.mu.Lock()
	f.streams[8] = second
	f.mu.Unlock()
	if err := h.PlaybackReport(context.Background(), dashboard.PlaybackReport{
		SessionID: "vs-out", Stream: 8, Rendered: 16000, Rate: 16000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatalf("the page's terminal receipt: %v", err)
	}
	if id := h.loadInflight().id; id != "" {
		t.Fatalf("a reply the page reported played is still in flight: %q", id)
	}
	before := len(f.opsSeen())
	a.settleVoice(context.Background(), "a third answer, with nothing left to stop")
	for _, op := range f.opsSeen()[before:] {
		if strings.HasPrefix(op, "interrupt:") {
			t.Fatalf("a reply whose playback was reported was fenced anyway: %v", f.opsSeen()[before:])
		}
	}

	// A receipt for a stream this session never named settles nothing, and
	// a PROGRESS report is not the end of anything.
	third := f.producingNow()
	if err := h.PlaybackReport(context.Background(), dashboard.PlaybackReport{
		SessionID: "vs-out", Stream: 9, Rendered: 1, Rate: 16000, Channels: 1, Terminal: true, Outcome: "stopped"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.streams[10] = third
	f.mu.Unlock()
	if err := h.PlaybackReport(context.Background(), dashboard.PlaybackReport{
		SessionID: "vs-out", Stream: 10, Rendered: 800, Rate: 16000, Channels: 1, Outcome: "progress"}); err != nil {
		t.Fatal(err)
	}
	if id := h.loadInflight().id; id != third {
		t.Fatalf("the reply in flight was settled by a foreign or non-terminal report: %q, want %q", id, third)
	}
}

// AN OPERATOR ABOUT TO SPEAK OUTRANKS A SESSION THAT ONLY SPEAKS. The
// engine serves one session; in earbuds a page holds it to read replies
// out — possibly a different page from the one whose microphone was just
// asked for. The output-only session is aborted and the open waits for the
// engine's word that it is over.
func TestAnOperatorAboutToSpeakTakesTheEngineFromAnOutputSession(t *testing.T) {
	a := newVoiceApp(t)
	h, f := outputHandle(t, a, "vs-out")
	done := make(chan struct{})
	h.done = done
	yielded := make(chan struct{})
	go func() {
		a.yieldVoiceOutput(context.Background(), h.engine)
		close(yielded)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.closed)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	closed := append([]string(nil), f.closed...)
	f.mu.Unlock()
	if len(closed) != 1 || !strings.HasPrefix(closed[0], "abort") {
		t.Fatalf("the output-only session was not asked to abort: %v", closed)
	}
	select {
	case <-yielded:
		t.Fatal("the open went ahead before the engine said the output session was over")
	case <-time.After(50 * time.Millisecond):
	}
	close(done) // the engine's word
	select {
	case <-yielded:
	case <-time.After(5 * time.Second):
		t.Fatal("the open never proceeded after the engine's word")
	}
	// With no output session there is nothing to yield and nothing is asked.
	a.voiceSessions.Delete("vs-out")
	a.yieldVoiceOutput(context.Background(), h.engine)
	f.mu.Lock()
	n := len(f.closed)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("a close was sent with no output session open: %d", n)
	}
}
