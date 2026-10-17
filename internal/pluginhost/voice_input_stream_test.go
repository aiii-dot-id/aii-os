package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// streamWire is the host's input transport, kept whole so the engine
// double reads it when the test decides.
type streamWire struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *streamWire) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// frames decodes every whole frame written so far. The host writes a
// frame's header and its payload as two writes, so what is read here while
// it is writing may end inside a frame: that frame is still being written
// and is counted once it is whole. A frame the host cut short for good is
// not hidden by this: every caller waits for a count, and the count never
// comes.
func (w *streamWire) frames(t *testing.T) []audio.Frame {
	t.Helper()
	w.mu.Lock()
	raw := append([]byte(nil), w.buf.Bytes()...)
	w.mu.Unlock()
	r := bytes.NewReader(raw)
	var out []audio.Frame
	for {
		fr, err := audio.ReadFrame(r)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return out
		}
		if err != nil {
			t.Fatalf("the host wrote a frame that does not decode: %v", err)
		}
		out = append(out, fr)
	}
}

// streamOpen is one open as the engine double received it.
type streamOpen struct {
	session  string
	declared bool   // audio.input.stream was present
	stream   uint32 // its value
	topLevel bool   // a stray top-level "stream" argument
}

type streamEngine struct {
	worker bool // the voice platform's worker rule; off = an engine that predates the field

	mu     sync.Mutex
	opens  []streamOpen
	served map[uint32]bool
	// echo is the "input" object the status reply carries, verbatim; ""
	// leaves it out, as an engine that predates the echo does.
	echo string
}

func (e *streamEngine) setEcho(input string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.echo = input
}

func (e *streamEngine) statusInput() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.echo == "" {
		return ""
	}
	return `,"input":` + e.echo
}

func (e *streamEngine) admit(o streamOpen) (refusal string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.worker && o.declared {
		if e.served[o.stream] {
			return "input stream number already served an earlier session"
		}
		e.served[o.stream] = true
	}
	e.opens = append(e.opens, o)
	return ""
}

func (e *streamEngine) open(session string) (streamOpen, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, o := range e.opens {
		if o.session == session {
			return o, true
		}
	}
	return streamOpen{}, false
}

func (e *streamEngine) heard(frames []audio.Frame, cur streamOpen) (pcm []byte, foreign int, failure string) {
	filter := e.worker && cur.declared
	adopted, have := uint32(0), false
	for _, fr := range frames {
		if fr.Kind != audio.KindPCM {
			continue
		}
		switch {
		case filter:
			if fr.Stream != cur.stream {
				foreign++
				continue
			}
		case !have:
			adopted, have = fr.Stream, true
		case fr.Stream != adopted:
			if failure == "" {
				failure = fmt.Sprintf("input on stream %d after adopting stream %d", fr.Stream, adopted)
			}
			continue
		}
		pcm = append(pcm, fr.PCM...)
	}
	return pcm, foreign, failure
}

type streamHarness struct {
	v      *VoiceSession
	eng    *streamEngine
	wire   *streamWire
	frames chan []byte
	ctx    context.Context
}

func newStreamHarness(t *testing.T, worker bool) *streamHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	frames := make(chan []byte, 64)
	ctrlIn, ctrlOut := io.Pipe()
	pcmIn, pcmOut := io.Pipe()
	c := supervisor.NewSessionClientFrames(frames, ctrlOut, nopDispatcher{}, 64)
	v := NewVoiceSession(c)
	wire := &streamWire{}
	v.pair = newAudioPair(wire, pcmIn)
	eng := &streamEngine{worker: worker, served: map[uint32]bool{}}
	clientDone, engineDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(clientDone); _ = c.Run(ctx) }()
	send := func(b []byte) bool {
		select {
		case frames <- b:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(engineDone)
		for {
			raw, err := bbb.ReadFrame(ctrlIn, bbb.MaxControlFrameBytes)
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Params struct {
					Operation string                     `json:"operation"`
					Args      map[string]json.RawMessage `json:"arguments"`
				} `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			var sid string
			_ = json.Unmarshal(req.Params.Args["session_id"], &sid)
			reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"accepted":true}}`, req.ID)
			var after []byte
			switch req.Params.Operation {
			case "speech.session.open":
				o := streamOpen{session: sid}
				_, o.topLevel = req.Params.Args["stream"]
				var au struct {
					Input map[string]json.RawMessage `json:"input"`
				}
				_ = json.Unmarshal(req.Params.Args["audio"], &au)
				if raw, ok := au.Input["stream"]; ok {
					o.declared = json.Unmarshal(raw, &o.stream) == nil
				}
				if why := eng.admit(o); why != "" {
					reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":%q}}`, req.ID, why)
				} else if au.Input == nil {
					reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"accepted":true,"audio":{"input":null,"output":{"rate":16000,"channels":1}}}}`, req.ID)
				} else {
					reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"accepted":true,"audio":{"input":{"rate":16000,"channels":1},"output":{"rate":16000,"channels":1}}}}`, req.ID)
				}
			case "speech.session.close":
				after = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"session_end","session_id":%q,"sequence":1}}`, sid))
			case "speech.session.status":
				reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"session_id":%q,"state_sequence":1,"lifecycle":"open"%s}}`, req.ID, sid, eng.statusInput())
			}
			if !send([]byte(reply)) {
				return
			}
			if after != nil && !send(after) {
				return
			}
		}
	}()
	t.Cleanup(func() {
		v.mu.Lock()
		v.releaseAudioLocked()
		p := v.pump
		v.mu.Unlock()
		cancel()
		_ = ctrlIn.Close()
		_ = ctrlOut.Close()
		_ = pcmIn.Close()
		_ = pcmOut.Close()
		if p != nil {
			auditAudioJoin(t, "last pump", p.Done())
		}
		auditAudioJoin(t, "pair reader", v.pair.ended)
		auditAudioJoin(t, "control engine", engineDone)
		auditAudioJoin(t, "control client", clientDone)
		auditAudioJoin(t, "voice observer", v.endedCh)
	})
	return &streamHarness{v: v, eng: eng, wire: wire, frames: frames, ctx: ctx}
}

// heldSource is a live microphone that has spoken: its frames, from
// sample 0 on the source's own stream, then nothing until it is retired.
type heldSource struct {
	mu     sync.Mutex
	frames []audio.Frame
}

func newHeldSource(fill byte, n int) *heldSource {
	s := &heldSource{}
	for i := 0; i < n; i++ {
		pcm := bytes.Repeat([]byte{fill + byte(i), fill}, 320)
		s.frames = append(s.frames, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i + 1), Start: int64(i * 320), PCM: pcm})
	}
	return s
}
func (s *heldSource) pcm() []byte {
	var out []byte
	for _, fr := range s.frames {
		out = append(out, fr.PCM...)
	}
	return out
}
func (*heldSource) Format() audio.Format { return mono16k }
func (s *heldSource) Read(ctx context.Context) (audio.Frame, error) {
	s.mu.Lock()
	if len(s.frames) > 0 {
		fr := s.frames[0]
		s.frames = s.frames[1:]
		s.mu.Unlock()
		return fr, nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return audio.Frame{}, ctx.Err()
}
func (*heldSource) Close() error { return nil }

// openSpeaking opens a duplex session over src and waits until the
// host has written all of src's frames to the transport (unread there).
func (h *streamHarness) openSpeaking(t *testing.T, id string, src *heldSource) error {
	t.Helper()
	want := len(src.frames)
	before := len(h.wire.frames(t))
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "speaker", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind(id, "mic", "speaker", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.OpenWithAudio(h.ctx, id, b, nil); err != nil {
		return err
	}
	auditAudioWait(t, id+"'s frames written", func() bool { return len(h.wire.frames(t)) >= before+want })
	return nil
}

// abort closes the current session and waits for the engine's terminal
// word: the pump is retired and nothing more is written for it.
func (h *streamHarness) abort(t *testing.T) {
	t.Helper()
	term := h.v.terminal()
	if err := h.v.Close(h.ctx, "abort", "operator interrupted"); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "session_end", term)
}

func TestAnAbortedSessionsUnreadInputNeverReachesTheNextSession(t *testing.T) {
	run := func(t *testing.T, worker bool) (pcm []byte, foreign int, failure string, a, b streamOpen, aSrc, bSrc []byte) {
		h := newStreamHarness(t, worker)
		srcA, srcB := newHeldSource(0x10, 3), newHeldSource(0x80, 4)
		aSrc, bSrc = srcA.pcm(), srcB.pcm()
		if err := h.openSpeaking(t, "A", srcA); err != nil {
			t.Fatalf("open A: %v", err)
		}
		h.abort(t) // before the engine read a single frame of A's
		if err := h.openSpeaking(t, "B", srcB); err != nil {
			t.Fatalf("open B: %v", err)
		}
		a, _ = h.eng.open("A")
		b, _ = h.eng.open("B")
		// Only now does the engine read: A's frames first, from sample 0,
		// then B's, from sample 0.
		pcm, foreign, failure = h.eng.heard(h.wire.frames(t), b)
		return pcm, foreign, failure, a, b, aSrc, bSrc
	}

	t.Run("declared stream, worker rule", func(t *testing.T) {
		pcm, foreign, failure, a, b, aSrc, bSrc := run(t, true)
		if failure != "" || !bytes.Equal(pcm, bSrc) {
			t.Fatalf("B heard %d bytes, not its own %d (A's were %d); failure=%q", len(pcm), len(bSrc), len(aSrc), failure)
		}
		if foreign != 3 {
			t.Fatalf("A's three unread frames are dropped and counted as foreign, got %d", foreign)
		}
		if !a.declared || !b.declared || a.topLevel || b.topLevel || a.stream == b.stream {
			t.Fatalf("each open declares its own stream inside audio.input: A=%+v B=%+v", a, b)
		}
	})

	// CONTROL: the same host against an engine that predates the field.
	// The race is real and this test sees it: B adopts A's audio and fails.
	t.Run("control: an engine that ignores the declaration", func(t *testing.T) {
		pcm, _, failure, _, _, aSrc, bSrc := run(t, false)
		if bytes.Equal(pcm, bSrc) || !bytes.Equal(pcm, aSrc) || failure == "" {
			t.Fatalf("without the stream rule B should adopt A's audio and fail: heard=%d bytes (A %d, B %d) failure=%q", len(pcm), len(aSrc), len(bSrc), failure)
		}
	})
}

// Every session on one driver — one engine process — declares a number
// no earlier session had, its pump writes every input frame on exactly
// that number, and an output-only session declares none.
func TestInputStreamNumbersAreFreshForTheLifeOfTheEngineProcess(t *testing.T) {
	h := newStreamHarness(t, true)
	seen := map[uint32]string{}
	var last uint32
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("s%d", i)
		before := len(h.wire.frames(t))
		if err := h.openSpeaking(t, id, newHeldSource(byte(i), 2)); err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		o, ok := h.eng.open(id)
		if !ok || !o.declared {
			t.Fatalf("%s: the open did not declare audio.input.stream: %+v", id, o)
		}
		if prev, dup := seen[o.stream]; dup {
			t.Fatalf("%s declared stream %d, already served %s", id, o.stream, prev)
		}
		if o.stream <= last {
			t.Fatalf("%s declared %d after %d: numbers only move forward", id, o.stream, last)
		}
		seen[o.stream], last = id, o.stream
		_, p := h.v.Audio()
		if p == nil {
			t.Fatalf("%s: an admitted duplex open has no pump", id)
		}
		if p.Stream != o.stream {
			t.Fatalf("%s: the pump writes on stream %d, the open declared %d", id, p.Stream, o.stream)
		}
		for _, fr := range h.wire.frames(t)[before:] {
			if fr.Stream != o.stream {
				t.Fatalf("%s: an input frame went out on stream %d, the open declared %d", id, fr.Stream, o.stream)
			}
		}
		h.abort(t)
		if i%10 == 9 {
			// An output-only session between them has no input and declares
			// none: audio.input is null, so there is no stream to name.
			oid := "out" + id
			plane := audio.NewPlane()
			_ = plane.Register(&audio.Endpoint{ID: "speaker", Sink: audio.NewCaptureSink(mono16k)})
			ob, err := plane.BindOutput(oid, "speaker", true)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.v.OpenWithAudio(h.ctx, oid, ob, nil); err != nil {
				t.Fatalf("output-only open: %v", err)
			}
			if o, ok := h.eng.open(oid); !ok || o.declared {
				t.Fatalf("an output-only open declared an input stream: %+v", o)
			}
			h.abort(t)
		}
	}
}

// The counter never wraps: the last number is served once, and the open
// after it is refused before anything is sent, its endpoints given back.
func TestInputStreamNumbersAreNeverReusedAtTheEndOfTheRange(t *testing.T) {
	h := newStreamHarness(t, true)
	h.v.mu.Lock()
	h.v.inputStreams = math.MaxUint32 - 1
	h.v.mu.Unlock()
	if err := h.openSpeaking(t, "last", newHeldSource(1, 1)); err != nil {
		t.Fatalf("the last number is still fresh: %v", err)
	}
	if o, _ := h.eng.open("last"); o.stream != math.MaxUint32 {
		t.Fatalf("declared %d, want %d", o.stream, uint32(math.MaxUint32))
	}
	h.abort(t)
	plane := audio.NewPlane()
	_ = plane.Register(&audio.Endpoint{ID: "mic", Source: newHeldSource(2, 1)})
	_ = plane.Register(&audio.Endpoint{ID: "speaker", Sink: audio.NewCaptureSink(mono16k)})
	b, err := plane.Bind("spent", "mic", "speaker", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.OpenWithAudio(h.ctx, "spent", b, nil); !errors.Is(err, ErrInputStreamsSpent) {
		t.Fatalf("an open past the last number: %v", err)
	}
	if _, sent := h.eng.open("spent"); sent {
		t.Fatal("the refused open reached the engine")
	}
	if !fired(b.Released(), time.Second) {
		t.Fatal("the refused open's endpoints are given back")
	}
}

// A SNAPSHOT TAKEN INSIDE A FRAME COUNTS THE WHOLE FRAMES AND NO MORE. The
// host writes a frame's header and its payload as two writes, and the
// harness reads the transport while the host is still writing, so a
// snapshot can end between the two. That frame is not written yet: it is
// not a frame that does not decode, and the tests that wait for a count of
// frames must go on waiting for it.
//
// Two cuts. The header and none of the payload is what this harness's
// transport holds between the host's two writes, each write being taken
// whole: the case that happened. The header and part of the payload is
// what a real descriptor can show a reader.
func TestASnapshotTakenInsideAFrameCountsOnlyWholeFrames(t *testing.T) {
	const payload = 640
	for _, cut := range []struct {
		name string
		held int // bytes of the second frame's payload on the transport
	}{{"between the header and the payload", 0}, {"inside the payload", 40}} {
		w := &streamWire{}
		whole := audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: 1, PCM: bytes.Repeat([]byte{7}, payload)}
		if err := audio.WriteFrame(w, whole); err != nil {
			t.Fatal(err)
		}
		var next bytes.Buffer
		if err := audio.WriteFrame(&next, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: 2, Start: 320, PCM: bytes.Repeat([]byte{9}, payload)}); err != nil {
			t.Fatal(err)
		}
		at := next.Len() - payload + cut.held
		if _, err := w.Write(next.Bytes()[:at]); err != nil {
			t.Fatal(err)
		}
		if got := w.frames(t); len(got) != 1 || got[0].Seq != 1 {
			t.Fatalf("%s: a snapshot inside the second frame gave %d frames; the one whole frame is what was written", cut.name, len(got))
		}
		if _, err := w.Write(next.Bytes()[at:]); err != nil {
			t.Fatal(err)
		}
		if got := w.frames(t); len(got) != 2 || got[1].Seq != 2 {
			t.Fatalf("%s: with the second frame whole the snapshot gave %d frames, want both", cut.name, len(got))
		}
	}
}
