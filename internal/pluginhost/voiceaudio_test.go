package pluginhost

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

var mono16k = audio.Format{Rate: 16000, Channels: 1}

func pcmRamp(n int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(i))
	}
	return b
}

// containedFakechild is the loopback engine under the platform's real
// containment, with the same skip rule the native-lane proof uses. It
// carries the containment as the host passes it: its built line is part
// of readiness.
func containedFakechild(t *testing.T) supervisor.Spec {
	t.Helper()
	bin := fakechildBin
	skipWhereTheSandboxCannotBeEstablished(t)
	argv, wall, err := containArgv(context.Background(), []string{bin, "session-audio"}, nil, nativeFiles{})
	if err != nil {
		t.Skipf("this host cannot contain a native child: %v", err)
	}
	return supervisor.Spec{Argv: argv, ArgvContainment: wall}
}

func audioVoicePlugin(t *testing.T, argv []string, env ...string) *ActivePlugin {
	t.Helper()
	return voicePlugin(t, supervisor.Spec{Argv: argv}, env...)
}

func voicePlugin(t *testing.T, spec supervisor.Spec, env ...string) *ActivePlugin {
	t.Helper()
	spec.PluginID, spec.SessionMode, spec.AudioPair, spec.Env = "audio.voice", true, true, env
	sup, err := supervisor.Start(spec, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sup.Close() })
	ap := &ActivePlugin{ID: "audio.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ap.sessionCancel)
	return ap
}

func fired(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// Session audio reaches a CONTAINED child on inherited descriptors and
// comes back with every span intact; the endpoints are the session's
// while it is open and are given back on the engine's terminal word —
// not on the close that was merely admitted.
func TestVoiceSessionCarriesAudioThroughAContainedChildAndReleasesOnTerminal(t *testing.T) {
	ap := voicePlugin(t, containedFakechild(t))
	ctx := context.Background()
	plane := audio.NewPlane()
	src, err := audio.NewFileSource(bytes.NewReader(pcmRamp(1000)), mono16k, 320)
	if err != nil {
		t.Fatal(err)
	}
	sink := audio.NewCaptureSink(mono16k)
	_ = plane.Register(&audio.Endpoint{ID: "mic", Label: "file", Source: src})
	_ = plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: sink})
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatalf("open with audio: %v", err)
	}
	_, p := ap.Voice.Load().Audio()
	if p == nil {
		t.Fatal("an admitted open with audio runs a pump")
	}
	// The reply (echo) arrives while the session stays OPEN: a per-synthesis
	// END retires that stream, it does not end the resident output pump
	// (host-output-lifetime correction). Wait for the echo, not for Done.
	deadline := time.Now().Add(10 * time.Second)
	for sink.End() != 1000 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !bytes.Equal(sink.PCM(), pcmRamp(1000)) || sink.End() != 1000 {
		t.Fatalf("the engine's output: %d samples, end %d", len(sink.PCM())/2, sink.End())
	}
	for _, fr := range sink.Frames() {
		if fr.Stream != 1 {
			t.Fatalf("the instance's stream id travels on every frame: %+v", fr)
		}
	}
	if fired(b.Released(), 100*time.Millisecond) || !ap.Pinned() {
		t.Fatal("the endpoints are the session's while it is open")
	}
	// Admission of the close is not release; the engine's session_end is.
	if err := ap.Voice.Load().Close(ctx, "abort", "done"); err != nil {
		t.Fatal(err)
	}
	if !fired(p.Done(), 5*time.Second) {
		t.Fatal("both pumps end at the session's terminal word, not the reply's END")
	}
	if !fired(b.Released(), 5*time.Second) {
		t.Fatal("the engine's terminal word must give the endpoints back")
	}
	if !fired(ap.PinReleased(), 5*time.Second) || ap.Pinned() {
		t.Fatal("the pin releases with the endpoints")
	}
	if _, err := plane.Bind("s2", "mic", "spk", true); err != nil {
		t.Fatalf("after release the endpoints are free: %v", err)
	}
	if ap.Voice.Load().Stray() != 0 {
		t.Fatalf("no frame arrived without a session to take it: %d", ap.Voice.Load().Stray())
	}
}

// tickSource is a live microphone stand-in: one 20 ms frame every tick
// under its own sample clock, up to a total.
type tickSource struct {
	f     audio.Format
	next  int64
	seq   uint32
	every time.Duration
	total int64
	ended bool
}

func (s *tickSource) Format() audio.Format { return s.f }
func (s *tickSource) Read(ctx context.Context) (audio.Frame, error) {
	if s.ended {
		return audio.Frame{}, io.EOF
	}
	if s.next >= s.total {
		s.ended = true
		s.seq++
		return audio.Frame{Kind: audio.KindEnd, Stream: 1, Seq: s.seq, Start: s.next}, nil
	}
	select {
	case <-time.After(s.every):
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	}
	s.seq++
	fr := audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: s.seq, Start: s.next, PCM: pcmRamp(320)}
	s.next += 320
	return fr, nil
}

// FinishInputNow fixes the cutoff where the delivery stands and the
// engine receives exactly that: every sample through the boundary, none
// past it, the stream ended there — the input tail preserved.
func TestFinishInputNowCutsTheInputExactlyWhereItStood(t *testing.T) {
	ap := audioVoicePlugin(t, []string{fakechildBin, "session-audio"})
	ctx := context.Background()
	plane := audio.NewPlane()
	sink := audio.NewCaptureSink(mono16k)
	_ = plane.Register(&audio.Endpoint{ID: "mic", Label: "live", Source: &tickSource{f: mono16k, every: 2 * time.Millisecond, total: 16000 * 10}})
	_ = plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: sink})
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatal(err)
	}
	_, p := ap.Voice.Load().Audio()
	deadline := time.Now().Add(5 * time.Second)
	for p.Delivered() < 3200 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	end, err := ap.Voice.Load().FinishInputNow(ctx, "in1")
	if err != nil {
		t.Fatalf("finish_input now: %v", err)
	}
	if end < 3200 || end%320 != 0 {
		t.Fatalf("the boundary is where the delivery stood, on a frame the engine already has: %d", end)
	}
	// The echo up to the cutoff arrives while the session stays OPEN: a
	// per-synthesis END retires that stream, it does not end the output pump
	// (host-output-lifetime correction). Wait for the echo, not for Done.
	deadline2 := time.Now().Add(10 * time.Second)
	for sink.End() != end && time.Now().Before(deadline2) {
		time.Sleep(time.Millisecond)
	}
	if got := int64(len(sink.PCM()) / 2); got != end || sink.End() != end || p.Delivered() != end {
		t.Fatalf("the engine received %d samples with end %d; delivered %d; boundary %d", got, sink.End(), p.Delivered(), end)
	}
	if err := ap.Voice.Load().Close(ctx, "drain", "done"); err != nil {
		t.Fatalf("a drain close after the cutoff: %v", err)
	}
	if !fired(p.Done(), 5*time.Second) {
		t.Fatal("both pumps end at the session's terminal word, not the reply's END")
	}
	if !fired(b.Released(), 5*time.Second) {
		t.Fatal("session_end releases the endpoints")
	}
}

// THE CONVERSION IS THE HOST'S, AT ITS ENDPOINTS, THROUGH THE REAL LANE:
// a 48 kHz microphone and a 48 kHz speaker around a contained engine
// that answers the open with 16 kHz. The engine receives the input on
// its own clock (the open named both endpoint formats; the answer named
// the engine's), the echo comes back on the speaker's clock with every
// count exact, and the cutoff the operator names in the microphone's
// clock reaches the engine as its next whole sample.
func TestTheHostConvertsAtItsEndpointsToTheEngineFormats(t *testing.T) {
	ap := voicePlugin(t, containedFakechild(t), "FAKE_ENGINE_RATE=16000")
	ctx := context.Background()
	plane := audio.NewPlane()
	f48 := audio.Format{Rate: 48000, Channels: 1}
	src, err := audio.NewFileSource(bytes.NewReader(pcmRamp(3000)), f48, 320)
	if err != nil {
		t.Fatal(err)
	}
	sink := audio.NewCaptureSink(f48)
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatalf("open with audio: %v", err)
	}
	if in, out := ap.Voice.Load().EngineFormats(); in.Rate != 16000 || out.Rate != 16000 || in.Channels != 1 {
		t.Fatalf("the engine's answer is the formats the pump converts to: %s / %s", in, out)
	}
	_, pump := ap.Voice.Load().Audio()
	if pump == nil {
		t.Fatal("no pump")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && sink.End() < 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if pump.Delivered() != 3000 || pump.Received() != 1000 || sink.End() != 3000 || len(sink.PCM())/2 != 3000 {
		t.Fatalf("delivered %d (microphone clock), received %d (engine clock), speaker end %d, %d speaker samples", pump.Delivered(), pump.Received(), sink.End(), len(sink.PCM())/2)
	}
	if got := pump.EngineCutoff(1000); got != 334 {
		t.Fatalf("a cutoff at microphone sample 1000 is engine sample 334, got %d", got)
	}
	if err := ap.Voice.Load().Close(ctx, "abort", "done"); err != nil {
		t.Fatal(err)
	}
}
