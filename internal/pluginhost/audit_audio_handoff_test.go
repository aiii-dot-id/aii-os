package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// Synthetic framed control and PCM transports; no child, device, identity or DB.
type auditAudioHarness struct {
	v        *VoiceSession
	frames   chan []byte
	audioOut *io.PipeWriter
	ctx      context.Context
}

func auditAudioWait(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
func auditAudioJoin(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Errorf("worker did not join: %s", what)
	}
}

// auditQueued is how deep the ATTACHED session's queue is. The queue
// moved onto the session it belongs to, so "what is buffered" is asked
// of that session; with none attached there is nothing buffered
// anywhere, which is the property under test.
func auditQueued(p *audioPair) int {
	p.omu.Lock()
	cur := p.current
	p.omu.Unlock()
	if cur != nil {
		return p.queuedFor(cur)
	}
	return 0
}

func auditNewAudioHarness(t *testing.T) *auditAudioHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	frames := make(chan []byte, 32)
	ctrlIn, ctrlOut := io.Pipe()
	pcmIn, pcmOut := io.Pipe()
	c := supervisor.NewSessionClientFrames(frames, ctrlOut, nopDispatcher{}, 32)
	v := NewVoiceSession(c)
	v.pair = newAudioPair(io.Discard, pcmIn)
	clientDone, engineDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(clientDone); _ = c.Run(ctx) }()
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
					Operation string `json:"operation"`
					Args      struct {
						SessionID string `json:"session_id"`
					} `json:"arguments"`
				} `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			reply := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"accepted":true,"audio":{"input":{"rate":16000,"channels":1},"output":{"rate":16000,"channels":1}}}}`, req.ID))
			if req.Params.Operation == "speech.session.status" {
				// Status is a session snapshot, not a control acknowledgement.
				// Keep this audio harness faithful to the public speech contract.
				reply = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"session_id":%q,"state_sequence":1,"lifecycle":"open"}}`, req.ID, req.Params.Args.SessionID))
			}
			select {
			case frames <- reply:
			case <-ctx.Done():
				return
			}
		}
	}()
	h := &auditAudioHarness{v: v, frames: frames, audioOut: pcmOut, ctx: ctx}
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
	return h
}

type auditEmptySource struct{}

func (auditEmptySource) Format() audio.Format                      { return mono16k }
func (auditEmptySource) Read(context.Context) (audio.Frame, error) { return audio.Frame{}, io.EOF }
func (auditEmptySource) Close() error                              { return nil }

type auditHeldSink struct {
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
	capture *audio.CaptureSink
}

func auditNewHeldSink() *auditHeldSink {
	return &auditHeldSink{started: make(chan struct{}), resume: make(chan struct{}), capture: audio.NewCaptureSink(mono16k)}
}
func (s *auditHeldSink) Format() audio.Format { return mono16k }
func (s *auditHeldSink) Write(ctx context.Context, fr audio.Frame) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.resume:
		return s.capture.Write(ctx, fr)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*auditHeldSink) Close() error { return nil }
func (h *auditAudioHarness) open(t *testing.T, id string, sink audio.Sink) *audio.Pump {
	t.Helper()
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: auditEmptySource{}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "speaker", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind(id, "mic", "speaker", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.v.OpenWithAudio(h.ctx, id, b, nil); err != nil {
		t.Fatal(err)
	}
	_, p := h.v.Audio()
	if p == nil {
		t.Fatal("admitted session has no pump")
	}
	return p
}
func (h *auditAudioHarness) frame(seq, stream uint32) error {
	return audio.WriteFrame(h.audioOut, audio.Frame{Kind: audio.KindPCM, Stream: stream, Seq: seq, Start: int64(seq), PCM: []byte{byte(seq), byte(seq >> 8)}})
}

// name is the control lane's half of a reply: the engine's word that an
// output stream is this session's. ADAPTATION (audio ownership, decided
// with the voice platform agent): the pair routes a frame by the binding
// its stream was given on the control lane, never by who is attached, so
// a harness that speaks on a stream says whose it is — as an engine
// does. The word carries no sequence: the harness's terminal events use
// fixed ones, and a watermark the fixture moved would swallow them.
func (h *auditAudioHarness) name(id string, stream uint32) {
	h.frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_start","session_id":%q,"synthesis_id":"syn-%d","output_stream":%d}}`, id, stream, stream))
}
func (h *auditAudioHarness) terminal(t *testing.T, id string) {
	t.Helper()
	term := h.v.terminal()
	h.frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"session_end","session_id":%q,"sequence":1}}`, id))
	auditAudioJoin(t, "actual terminal event", term)
}

// Regression acceptance: old PCM queued during an active session must never
// be delivered to a new binding. Exercise actual open, event, pump and sink.
func TestAuditQueuedAudioCannotReachSuccessorSpeaker(t *testing.T) {
	for _, backlog := range []int{1, pairBuffer} {
		t.Run(fmt.Sprint(backlog), func(t *testing.T) {
			h := auditNewAudioHarness(t)
			oldSink := auditNewHeldSink()
			oldPump := h.open(t, "old", oldSink)
			h.name("old", 11)
			if err := h.frame(0, 11); err != nil {
				t.Fatal(err)
			}
			auditAudioJoin(t, "old sink entered", oldSink.started)
			for i := 1; i <= backlog; i++ {
				if err := h.frame(uint32(i), 11); err != nil {
					t.Fatal(err)
				}
			}
			// The queue moved onto the session it belongs to; this
			// barrier is the same barrier, asked of the session that is
			// attached. The assertion below is unchanged.
			auditAudioWait(t, "old backlog buffered", func() bool { return auditQueued(h.v.pair) == backlog })
			h.terminal(t, "old")
			auditAudioJoin(t, "old pump", oldPump.Done())
			successor := audio.NewCaptureSink(mono16k)
			h.open(t, "new", successor)
			h.name("new", 22)
			if err := h.frame(9001, 22); err != nil {
				t.Fatal(err)
			}
			auditAudioWait(t, "new frame reaches successor", func() bool {
				for _, fr := range successor.Frames() {
					if fr.Stream == 22 && fr.Seq == 9001 {
						return true
					}
				}
				return false
			})
			old := 0
			for _, fr := range successor.Frames() {
				if fr.Stream == 11 {
					old++
				}
			}
			if old != 0 {
				t.Fatalf("successor speaker received %d predecessor PCM frames; own new frame also arrived", old)
			}
		})
	}
}

// Positive control: actual admitted API session, held speaker, >4 queues,
// then resumed playback. Every frame must reach the sink once and in order.
func TestAuditActualSessionKeepsEveryFrameUnderBackpressure(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := auditNewHeldSink()
	p := h.open(t, "active", sink)
	h.name("active", 33)
	const n = 1024
	writerDone := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		defer close(writerDone)
		for i := 0; i < n; i++ {
			if err := h.frame(uint32(i), 33); err != nil {
				writerErr <- err
				return
			}
		}
	}()
	t.Cleanup(func() { _ = h.audioOut.Close(); auditAudioJoin(t, "PCM writer", writerDone) })
	auditAudioJoin(t, "speaker stalled", sink.started)
	auditAudioWait(t, "full active queue", func() bool { return auditQueued(h.v.pair) == pairBuffer })
	close(sink.resume)
	auditAudioJoin(t, "PCM writer finished", writerDone)
	select {
	case err := <-writerErr:
		t.Fatal(err)
	default:
	}
	auditAudioWait(t, "all PCM rendered to synthetic sink", func() bool { return len(sink.capture.Frames()) == n })
	for i, fr := range sink.capture.Frames() {
		if fr.Seq != uint32(i) {
			t.Fatalf("frame %d is sequence %d", i, fr.Seq)
		}
	}
	if lost := h.v.Stray(); lost != 0 {
		t.Fatalf("active loss=%d", lost)
	}
	h.terminal(t, "active")
	auditAudioJoin(t, "active pump", p.Done())
}

// Positive control for the actual terminal path under a blocked writer:
// terminal cancels both the sink and backpressured reader; closing synthetic
// output then joins the descriptor reader, rather than merely a counter check.
func TestAuditTerminalJoinsBackpressuredAudioWorkers(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := auditNewHeldSink()
	p := h.open(t, "cancel", sink)
	h.name("cancel", 44)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1024; i++ {
			if h.frame(uint32(i), 44) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = h.audioOut.Close(); auditAudioJoin(t, "cancelled writer", done) })
	auditAudioJoin(t, "blocked sink", sink.started)
	auditAudioWait(t, "saturated queue", func() bool { return auditQueued(h.v.pair) == pairBuffer })
	h.terminal(t, "cancel")
	auditAudioJoin(t, "cancelled pump", p.Done())
	auditAudioJoin(t, "released writer", done)
	_ = h.audioOut.Close()
	auditAudioJoin(t, "closed pair reader", h.v.pair.ended)
}
