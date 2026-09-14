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

// .
// .
func containedFakechild(t *testing.T) []string {
	t.Helper()
	bin := buildFakechild(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	argv, _, err := containArgv([]string{bin, "session-audio"})
	if err != nil {
		t.Skipf("this host cannot contain a native child: %v", err)
	}
	return argv
}

func audioVoicePlugin(t *testing.T, argv []string, env ...string) *ActivePlugin {
	t.Helper()
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "audio.voice", Argv: argv, SessionMode: true, AudioPair: true, Env: env}, nopDispatcher{})
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

// .
// .
// .
// .
func TestVoiceSessionCarriesAudioThroughAContainedChildAndReleasesOnTerminal(t *testing.T) {
	ap := audioVoicePlugin(t, containedFakechild(t))
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
	if err := ap.Voice.OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatalf("open with audio: %v", err)
	}
	_, p := ap.Voice.Audio()
	if p == nil {
		t.Fatal("an admitted open with audio runs a pump")
	}
	// .
	// .
	// .
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
	// .
	if err := ap.Voice.Close(ctx, "abort", "done"); err != nil {
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
	if ap.Voice.Stray() != 0 {
		t.Fatalf("no frame arrived without a session to take it: %d", ap.Voice.Stray())
	}
}

// .
// .
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

// .
// .
// .
func TestFinishInputNowCutsTheInputExactlyWhereItStood(t *testing.T) {
	ap := audioVoicePlugin(t, []string{buildFakechild(t), "session-audio"})
	ctx := context.Background()
	plane := audio.NewPlane()
	sink := audio.NewCaptureSink(mono16k)
	_ = plane.Register(&audio.Endpoint{ID: "mic", Label: "live", Source: &tickSource{f: mono16k, every: 2 * time.Millisecond, total: 16000 * 10}})
	_ = plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: sink})
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatal(err)
	}
	_, p := ap.Voice.Audio()
	deadline := time.Now().Add(5 * time.Second)
	for p.Delivered() < 3200 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	end, err := ap.Voice.FinishInputNow(ctx, "in1")
	if err != nil {
		t.Fatalf("finish_input now: %v", err)
	}
	if end < 3200 || end%320 != 0 {
		t.Fatalf("the boundary is where the delivery stood, on a frame the engine already has: %d", end)
	}
	// .
	// .
	// .
	deadline2 := time.Now().Add(10 * time.Second)
	for sink.End() != end && time.Now().Before(deadline2) {
		time.Sleep(time.Millisecond)
	}
	if got := int64(len(sink.PCM()) / 2); got != end || sink.End() != end || p.Delivered() != end {
		t.Fatalf("the engine received %d samples with end %d; delivered %d; boundary %d", got, sink.End(), p.Delivered(), end)
	}
	if err := ap.Voice.Close(ctx, "drain", "done"); err != nil {
		t.Fatalf("a drain close after the cutoff: %v", err)
	}
	if !fired(p.Done(), 5*time.Second) {
		t.Fatal("both pumps end at the session's terminal word, not the reply's END")
	}
	if !fired(b.Released(), 5*time.Second) {
		t.Fatal("session_end releases the endpoints")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestTheHostConvertsAtItsEndpointsToTheEngineFormats(t *testing.T) {
	ap := audioVoicePlugin(t, containedFakechild(t), "FAKE_ENGINE_RATE=16000")
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
	if err := ap.Voice.OpenWithAudio(ctx, "s1", b, nil); err != nil {
		t.Fatalf("open with audio: %v", err)
	}
	if in, out := ap.Voice.EngineFormats(); in.Rate != 16000 || out.Rate != 16000 || in.Channels != 1 {
		t.Fatalf("the engine's answer is the formats the pump converts to: %s / %s", in, out)
	}
	_, pump := ap.Voice.Audio()
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
	if err := ap.Voice.Close(ctx, "abort", "done"); err != nil {
		t.Fatal(err)
	}
}
