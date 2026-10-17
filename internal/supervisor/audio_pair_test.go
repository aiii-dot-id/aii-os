package supervisor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// audioEchoRoundTrip proves one pair carries a span intact.
func audioEchoRoundTrip(t *testing.T, in io.Writer, out io.Reader, stream uint32) {
	t.Helper()
	pcm := bytes.Repeat([]byte{3, 4}, 160)
	fr := audio.Frame{Kind: audio.KindPCM, Stream: stream, Seq: 1, Start: 0, PCM: pcm}
	if err := audio.WriteFrame(in, fr); err != nil {
		t.Fatal(err)
	}
	got, err := audio.ReadFrame(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != fr.Kind || got.Stream != fr.Stream || got.Start != fr.Start || !bytes.Equal(got.PCM, fr.PCM) {
		t.Fatalf("the pair did not carry the span: got %+v want %+v", got, fr)
	}
}

// A spawn's audio pair reaches the child on inherited descriptors, keeps
// every span intact through a loopback engine, is handed out only while
// the child is alive, and ends with the child.
func TestAudioPairIsInheritedAndEndsWithTheChild(t *testing.T) {
	s, err := Start(Spec{
		PluginID: "audio.pair", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true,
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in, out, ok := s.AudioPair()
	if !ok {
		t.Fatal("a running child with an audio pair hands it out")
	}
	pcm := bytes.Repeat([]byte{1, 2}, 320)
	want := []audio.Frame{
		{Kind: audio.KindPCM, Stream: 7, Seq: 1, Start: 100, PCM: pcm},
		{Kind: audio.KindDiscontinuity, Stream: 7, Seq: 2, Start: 500},
		{Kind: audio.KindEnd, Stream: 7, Seq: 3, Start: 500},
	}
	for _, fr := range want {
		if err := audio.WriteFrame(in, fr); err != nil {
			t.Fatal(err)
		}
	}
	for i, w := range want {
		got, err := audio.ReadFrame(out)
		if err != nil {
			t.Fatalf("echo %d: %v", i, err)
		}
		if got.Kind != w.Kind || got.Stream != w.Stream || got.Seq != w.Seq || got.Start != w.Start || !bytes.Equal(got.PCM, w.PCM) {
			t.Fatalf("echo %d: got %+v want %+v", i, got, w)
		}
	}
	// The pair ends with the child: after the supervisor reaps it, the
	// host's read end is closed and nothing is handed out.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := audio.ReadFrame(out); err == nil {
		t.Fatal("after the reap the host's end must be closed")
	} else if err != io.EOF && !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after the reap: %v", err)
	}
	if _, _, ok := s.AudioPair(); ok {
		t.Fatal("a stopped supervisor hands out no audio pair")
	}
}

// A RESTART IS A NEW PAIR. The child dies; the supervisor respawns it; the
// dead session's ends are closed and the new child gets fresh pipes —
// never the old ones, on any platform.
func TestAudioPairIsFreshPerSpawnAndTheDeadOneEnds(t *testing.T) {
	s, err := Start(Spec{
		PluginID: "audio.respawn", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true,
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 3},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in, out, ok := s.AudioPair()
	if !ok {
		t.Fatal("a running child hands out its pair")
	}
	audioEchoRoundTrip(t, in, out, 11)

	// The child dies where it stands.
	pid := s.Pid()
	if pid == 0 {
		t.Fatal("a running child has a pid")
	}
	if killed, err := s.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	// The dead session's output end ends; nothing of it is handed out again.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := audio.ReadFrame(out); err != nil {
			break
		}
	}
	if _, err := audio.ReadFrame(out); err == nil {
		t.Fatal("the dead child's audio end must not still be readable")
	}
	// The restart brings a pair of its own, and it carries audio.
	var in2 io.WriteCloser
	var out2 io.ReadCloser
	for time.Now().Before(deadline) {
		var ok2 bool
		if in2, out2, ok2 = s.AudioPair(); ok2 && s.Restarts() > 0 && out2 != out {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if in2 == nil || out2 == nil {
		t.Fatal("the restarted child never handed out a pair")
	}
	if in2 == in || out2 == out {
		t.Fatal("the restarted child was handed the dead session's ends")
	}
	audioEchoRoundTrip(t, in2, out2, 12)
}

// A START THAT NEVER HAPPENS GIVES EVERYTHING BACK. Twenty refused spawns
// with an audio pair leak no descriptor and no handle — and the twenty-first
// spawn, a real one, still gets a working pair.
func TestAudioPairFailedStartLeaksNothing(t *testing.T) {
	before := openResourceCount(t)
	missing := fakechildBin + "-does-not-exist"
	for i := 0; i < 20; i++ {
		s, err := Start(Spec{
			PluginID: "audio.failed", Argv: []string{missing, "session-audio"}, SessionMode: true, AudioPair: true,
			Backoff: Backoff{Initial: time.Millisecond, Max: 2 * time.Millisecond, MaxRestarts: 0},
		}, nil)
		if err == nil {
			s.Close()
			t.Fatal("a spawn of a binary that does not exist must be refused")
		}
	}
	after := openResourceCount(t)
	if after-before > 4 {
		t.Fatalf("twenty refused spawns leaked %d open resources (%d -> %d)", after-before, before, after)
	}
	s, err := Start(Spec{
		PluginID: "audio.after-failures", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true,
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 1},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in, out, ok := s.AudioPair()
	if !ok {
		t.Fatal("after refused spawns a real one still gets its pair")
	}
	audioEchoRoundTrip(t, in, out, 13)
}
