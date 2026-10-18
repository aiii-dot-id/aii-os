package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/coder/websocket"
)

func linesWith(logs *logsink.Capture, text string) []string {
	var out []string
	for _, l := range logs.Lines() {
		if strings.Contains(l, text) {
			out = append(out, l)
		}
	}
	return out
}

// A GAP IS DECLARED ON THE FRAME THAT FOLLOWS THE LOSS, AND SAID THERE,
// ONCE (the voice architecture's rule 2, which the page already keeps;
// found live: an engine received a discontinuity and nothing said where it
// came from). The page's microphone queue holds voiceStreamBuffer frames;
// what it cannot take is dropped. The frames queued before the loss reach
// the engine first, unmarked and in order; the first frame enqueued after
// the loss carries the gap, at its own start — the first sample after the
// loss — however many frames the loss took; and that declaration is
// logged once, with the session, the frames dropped and the samples they
// spanned, the stream and the sample — never once per dropped frame, and
// never for frames that arrived whole.
func TestAGapTheHostDeclaresIsLoggedOncePerGap(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	f := audio.Format{Rate: 16000, Channels: 1}
	b := newBrowserSource(f)
	b.named("vs-gap")
	pcm := func(seq uint32) audio.Frame {
		return audio.Frame{Kind: audio.KindPCM, Stream: 7, Seq: seq, Start: int64(seq) * 320, PCM: make([]byte, 640)}
	}
	read := func() audio.Frame {
		t.Helper()
		fr, err := b.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return fr
	}
	const said = "the microphone's queue"
	seq := uint32(0)
	// gap fills the queue, drops drop more, then pushes the frame after
	// the loss, and reads what the engine is given.
	gap := func(drop int) (lostFrom, lostTo, after int64) {
		t.Helper()
		first := seq
		for i := 0; i < voiceStreamBuffer; i++ {
			b.push(pcm(seq))
			seq++
		}
		lostFrom = int64(seq) * 320
		for i := 0; i < drop; i++ {
			b.push(pcm(seq))
			seq++
		}
		lostTo, after = int64(seq)*320, int64(seq)*320
		for i := 0; i < voiceStreamBuffer; i++ {
			if fr := read(); fr.Kind != audio.KindPCM || fr.Seq != first+uint32(i) {
				t.Fatalf("queued frame %d (before the loss) reached the engine as %+v: frames queued before a loss arrive first, unmarked and in order", i, fr)
			}
		}
		b.push(pcm(seq)) // the first frame after the loss: it carries the gap
		if fr := read(); fr.Kind != audio.KindDiscontinuity || fr.Start != after || fr.Stream != 7 {
			t.Fatalf("the gap was not declared at the first sample after the loss (%d): %+v", after, fr)
		}
		if fr := read(); fr.Kind != audio.KindPCM || fr.Seq != seq {
			t.Fatalf("the frame after the gap was not delivered: %+v", fr)
		}
		seq++
		b.push(pcm(seq)) // whole again
		if fr := read(); fr.Kind != audio.KindPCM || fr.Seq != seq {
			t.Fatalf("a frame after the declared gap came out as %+v", fr)
		}
		seq++
		return lostFrom, lostTo, after
	}
	// Control: frames that arrive whole say nothing.
	b.push(pcm(seq))
	seq++
	read()
	if got := linesWith(logs, said); len(got) != 0 {
		t.Fatalf("frames that arrived whole were logged as a gap: %q", got)
	}
	from, to, at := gap(3)
	got := linesWith(logs, said)
	if len(got) != 1 {
		t.Fatalf("one gap of 3 dropped frames was said %d times: %q", len(got), got)
	}
	for _, want := range []string{"session vs-gap", "3 frame(s) from the page were dropped", "(samples " + itoa(from) + " to " + itoa(to) + ")", "stream 7", "at sample " + itoa(at) + ", the first after the loss", "voice.error"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("the gap's line does not say %q: %s", want, got[0])
		}
	}
	gap(1)
	if got := linesWith(logs, said); len(got) != 2 || !strings.Contains(got[1], "1 frame(s) from the page were dropped") || !strings.Contains(got[1], "(4 dropped in this session)") {
		t.Fatalf("a second gap was not said once, with its own count: %q", got)
	}
}

func itoa(n int64) string {
	var b [20]byte
	i := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// A GAP THE PAGE DECLARES IS SAID AS IT ARRIVES, ONCE. A discontinuity
// frame from the page is the page's own word that its microphone lost
// audio before the socket; the host passes it on to the engine, and now
// says so — once per frame, which is once per gap — with the session, the
// page, the stream and the sample.
func TestAGapThePageDeclaresIsLoggedOnceAsItArrives(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	plane := audio.NewPlane()
	held := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string, processing *CaptureProcessing) (VoiceSession, error) {
			return held, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	ctx := context.Background()
	drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("open: %+v", st.VoiceSession)
	}
	// send writes frames, then a frame the host refuses: the read loop
	// takes them in order, so the refusal says the frames were handled.
	send := func(frames ...[]byte) {
		t.Helper()
		for _, fr := range append(frames, streamFrame(48000, 1, audio.KindPCM, 1, 0, make([]byte, 640))) {
			if err := conn.Write(ctx, websocket.MessageBinary, fr); err != nil {
				t.Fatal(err)
			}
		}
		drainUntil(t, conn, "error")
	}
	const said = "declared a gap in its microphone"
	// Control: whole frames say nothing.
	send(streamFrame(16000, 1, audio.KindPCM, 1, 0, make([]byte, 640)), streamFrame(16000, 1, audio.KindPCM, 2, 320, make([]byte, 640)))
	if got := linesWith(logs, said); len(got) != 0 {
		t.Fatalf("whole frames were logged as a gap: %q", got)
	}
	send(streamFrame(16000, 1, audio.KindDiscontinuity, 3, 4800, nil), streamFrame(16000, 1, audio.KindPCM, 4, 4800, make([]byte, 640)))
	got := linesWith(logs, said)
	if len(got) != 1 {
		t.Fatalf("one gap the page declared was said %d times: %q", len(got), got)
	}
	for _, want := range []string{"session vs-held", "stream 0", "at sample 4800", "voice.error"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("the page's gap line does not say %q: %s", want, got[0])
		}
	}
	send(streamFrame(16000, 1, audio.KindDiscontinuity, 5, 9600, nil))
	if got := linesWith(logs, said); len(got) != 2 || !strings.Contains(got[1], "at sample 9600") {
		t.Fatalf("a second gap the page declared was not said once: %q", got)
	}
}

func TestAThirtySecondEngineStallLosesNothing(t *testing.T) {
	const rate, frame = 48000, 960 // the page's 20 ms frame at 48 kHz
	pcm := func(seq uint32) audio.Frame {
		return audio.Frame{Kind: audio.KindPCM, Seq: seq, Start: int64(seq) * frame, PCM: make([]byte, 2*frame)}
	}
	stall := func(t *testing.T, seconds int) (*browserSource, uint32) {
		t.Helper()
		b := newBrowserSource(audio.Format{Rate: rate, Channels: 1})
		b.named("vs-stall")
		n := uint32(seconds * 50)
		for seq := uint32(0); seq < n; seq++ {
			b.push(pcm(seq)) // the engine reads nothing meanwhile
		}
		return b, n
	}
	read := func(t *testing.T, b *browserSource) audio.Frame {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		fr, err := b.Read(ctx)
		if err != nil {
			t.Fatalf("the engine's read found nothing queued — a frame the page sent was lost: %v", err)
		}
		return fr
	}
	const said = "the microphone's queue"

	t.Run("30 s", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		b, n := stall(t, 30)
		for seq := uint32(0); seq < n; seq++ {
			if fr := read(t, b); fr.Kind != audio.KindPCM || fr.Seq != seq {
				t.Fatalf("after a 30 s stall, frame %d reached the engine as %+v: every frame arrives, in order, unmarked", seq, fr)
			}
		}
		b.push(pcm(n)) // the page goes on, the engine reads again
		if fr := read(t, b); fr.Kind != audio.KindPCM || fr.Seq != n {
			t.Fatalf("the frame after the stall came out as %+v", fr)
		}
		if d := b.dropped.Load(); d != 0 {
			t.Fatalf("a 30 s stall dropped %d frame(s)", d)
		}
		if got := linesWith(logs, said); len(got) != 0 {
			t.Fatalf("a 30 s stall declared a gap: %q", got)
		}
	})

	t.Run("past 32 s", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		b, n := stall(t, 34)
		kept := uint32(voiceStreamBuffer)
		for seq := uint32(0); seq < kept; seq++ {
			if fr := read(t, b); fr.Kind != audio.KindPCM || fr.Seq != seq {
				t.Fatalf("frame %d, queued before the loss, reached the engine as %+v", seq, fr)
			}
		}
		b.push(pcm(n)) // the first frame after the loss carries the gap
		if fr := read(t, b); fr.Kind != audio.KindDiscontinuity || fr.Start != int64(n)*frame {
			t.Fatalf("the gap was not declared at the first sample after the loss (%d): %+v", int64(n)*frame, fr)
		}
		if fr := read(t, b); fr.Kind != audio.KindPCM || fr.Seq != n {
			t.Fatalf("the frame after the gap was not delivered: %+v", fr)
		}
		got := linesWith(logs, said)
		if len(got) != 1 {
			t.Fatalf("one stall past the queue was said %d times: %q", len(got), got)
		}
		lost := n - kept
		for _, want := range []string{"session vs-stall", "(1600 frames)", itoa(int64(lost)) + " frame(s) from the page were dropped", "(samples " + itoa(int64(kept)*frame) + " to " + itoa(int64(n)*frame) + ")"} {
			if !strings.Contains(got[0], want) {
				t.Fatalf("the gap's line does not say %q: %s", want, got[0])
			}
		}
	})
}
