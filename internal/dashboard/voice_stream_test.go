package dashboard

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/coder/websocket"
)

// The Off gesture may enqueue an interrupt immediately before Abort. The
// engine may refuse that interrupt after the lane has closed; it must not
// surface as a failure of the page's next conversation. A genuine refusal
// while the same lane remains live must still be visible — as its one
// sentence (T74), the engine's words going to the log.
func TestInterruptRefusalBelongsOnlyToALiveVoiceSession(t *testing.T) {
	err := errors.New("session not ready")
	ready := &connVoice{session: &stubSession{done: make(chan struct{})}}
	var reports []string
	report := func(s string) { reports = append(reports, s) }
	ready.reportRefusal(voiceInterrupt, err, report)
	if len(reports) != 1 || reports[0] != "Couldn't stop speaking." {
		t.Fatalf("a live interrupt refusal was hidden, or not told as its sentence: %v", reports)
	}
	ready.retiring.Store(true)
	ready.reportRefusal(voiceInterrupt, err, report)
	if len(reports) != 1 {
		t.Fatalf("the late refusal of a retired lane reached the page: %v", reports)
	}
	endedDone := make(chan struct{})
	ended := &connVoice{session: &stubSession{done: endedDone}}
	close(endedDone)
	ended.reportRefusal(voiceInterrupt, err, report)
	if len(reports) != 1 {
		t.Fatalf("the refusal of an ended lane reached the page: %v", reports)
	}
}

// echoChannel stands in for the engine's audio pair: every input frame
// comes back as output, and the input's end ends the output.
type echoChannel struct {
	in  chan audio.Frame
	out chan audio.Frame
}

func newEchoChannel() *echoChannel {
	e := &echoChannel{in: make(chan audio.Frame, 64), out: make(chan audio.Frame, 64)}
	go func() {
		for fr := range e.in {
			e.out <- fr
			if fr.Kind == audio.KindEnd {
				close(e.out)
				return
			}
		}
		close(e.out)
	}()
	return e
}
func (e *echoChannel) WriteInput(fr audio.Frame) error { e.in <- fr; return nil }
func (e *echoChannel) ReadOutput() (audio.Frame, error) {
	fr, ok := <-e.out
	if !ok {
		return audio.Frame{}, io.EOF
	}
	return fr, nil
}
func (e *echoChannel) Close() error { return nil }

// stubSession is the application's session as a stub: bound on the
// plane, pumped through the echo, finished at the sample the page
// names, ended when the echo's output ends.
type stubSession struct {
	id         string
	b          *audio.Binding
	p          *audio.Pump
	finished   chan int64
	interrupts chan struct{}
	reports    chan PlaybackReport
	done       chan struct{}
	// inputClosed stands in for the engine ending the input on its own.
	// Nil until a test arms it, which is the ordinary case: an engine
	// that never stops listening never closes it.
	inputClosed chan struct{}
	inputWhy    string
	// malformed is the engine's frames the driver withheld (Q11).
	malformed uint64
}

func (s *stubSession) ID() string { return s.id }
func (s *stubSession) Finish(ctx context.Context, end int64) error {
	s.finished <- s.p.Cutoff(end)
	return nil
}
func (s *stubSession) Close(ctx context.Context, mode string) error { return nil }
func (s *stubSession) Interrupt(ctx context.Context) error          { s.interrupts <- struct{}{}; return nil }
func (s *stubSession) PlaybackReport(ctx context.Context, r PlaybackReport) error {
	s.reports <- r
	return nil
}
func (s *stubSession) Label() string                 { return "Listening" }
func (s *stubSession) Malformed() uint64             { return s.malformed }
func (s *stubSession) InputClosed() <-chan struct{}  { return s.inputClosed }
func (s *stubSession) InputCompletionReason() string { return s.inputWhy }
func (s *stubSession) Done() <-chan struct{}         { return s.done }
func (s *stubSession) Released() <-chan struct{}     { return s.b.Released() }

func streamFrame(rate, channels int, kind audio.Kind, seq uint32, start int64, pcm []byte) []byte {
	out := make([]byte, voiceStreamHeaderBytes+len(pcm))
	out[0], out[1], out[2] = voiceStreamVersion, byte(channels), byte(kind)
	binary.LittleEndian.PutUint32(out[4:8], uint32(rate))
	binary.LittleEndian.PutUint32(out[8:12], seq)
	binary.LittleEndian.PutUint64(out[12:20], uint64(start))
	copy(out[voiceStreamHeaderBytes:], pcm)
	return out
}

// The page as an endpoint set: its open registers two endpoints on the
// plane and opens a session bound to them; the frames it streams reach
// the engine and the engine's output streams back with every span
// intact; its end frame finishes the input at exactly the sample it
// named; the engine's end closes the session, the page is told, and the
// endpoints leave the plane once given back.
func TestThePageIsAnEndpointSetOnTheApplicationsPlane(t *testing.T) {
	plane := audio.NewPlane()
	finished := make(chan int64, 1)
	interrupts := make(chan struct{}, 4)
	reports := make(chan PlaybackReport, 4)
	processingReport := make(chan *CaptureProcessing, 1)
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string, processing *CaptureProcessing) (VoiceSession, error) {
			processingReport <- processing
			b, err := plane.Bind("vs-1", in, out, true)
			if err != nil {
				return nil, err
			}
			p := audio.NewPump(b, newEchoChannel())
			ss := &stubSession{id: "vs-1", b: b, p: p, finished: finished, interrupts: interrupts, reports: reports, done: make(chan struct{}), malformed: 2}
			go func() {
				p.Run(context.Background())
				b.Release() // the engine's word: the session ended with its output
				close(ss.done)
			}()
			return ss, nil
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

	// The status says an engine is there.
	if st := drainUntil(t, conn, "status"); !st.Stats.VoiceEngine {
		t.Fatal("the status must say a speech engine is active")
	}
	// A frame before any session is refused.
	if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(16000, 1, audio.KindPCM, 1, 0, make([]byte, 640))); err != nil {
		t.Fatal(err)
	}
	if got := drainUntil(t, conn, "error"); got.Message == "" {
		t.Fatal("a stream frame without a session is refused with a reason")
	}
	// The operator's act.
	enabled, disabled := true, false
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1,
		Processing: &CaptureProcessing{EchoCancellation: &enabled, NoiseSuppression: &disabled, SampleRate: 48000}}})
	st := drainUntil(t, conn, "voice_session")
	if st.VoiceSession == nil || st.VoiceSession.State != "open" || st.VoiceSession.SessionID != "vs-1" {
		t.Fatalf("open: %+v", st.VoiceSession)
	}
	if p := <-processingReport; p == nil || p.EchoCancellation == nil || !*p.EchoCancellation || p.NoiseSuppression == nil || *p.NoiseSuppression || p.SampleRate != 48000 {
		t.Fatalf("the browser's capture readback did not reach the session: %+v", p)
	}
	if eps := plane.Endpoints(); len(eps) != 2 || eps[0].BoundTo != "vs-1" || eps[1].BoundTo != "vs-1" {
		t.Fatalf("the page's endpoints are registered and bound: %+v", eps)
	}
	// A frame at another rate is refused.
	if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(48000, 1, audio.KindPCM, 1, 0, make([]byte, 640))); err != nil {
		t.Fatal(err)
	}
	if got := drainUntil(t, conn, "error"); got.Message == "" {
		t.Fatal("a frame at another rate is refused")
	}
	// The operator's barge-in reaches the engine as an interruption.
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "interrupt"}})
	select {
	case <-interrupts:
	case <-time.After(5 * time.Second):
		t.Fatal("the page's interrupt must reach the engine")
	}
	// The page's playback receipt reaches the session it names — and ONLY
	// the session this page holds: a report naming another session is
	// refused to the page as evidence and reaches no engine.
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "playback", Playback: &PlaybackReport{SessionID: "vs-1", Stream: 7, Rendered: 320, Rate: 16000, Channels: 1, Terminal: true, Outcome: "drained"}}})
	select {
	case r := <-reports:
		if r.Stream != 7 || r.Rendered != 320 || !r.Terminal || r.Outcome != "drained" || r.SessionID != "vs-1" {
			t.Fatalf("the receipt arrives whole: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the page's receipt must reach the session")
	}
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "playback", Playback: &PlaybackReport{SessionID: "vs-0", Stream: 7, Rendered: 320, Rate: 16000, Channels: 1}}})
	if ev := drainUntil(t, conn, "voice_event"); ev.VoiceEvent == nil || ev.VoiceEvent.Type != "receipt_refused" || !strings.Contains(ev.VoiceEvent.Reason, "vs-0") || ev.VoiceEvent.Stream != 7 {
		t.Fatalf("a report naming another session is refused to the page, naming the stream it owes: %+v", ev.VoiceEvent)
	}
	select {
	case r := <-reports:
		t.Fatalf("a refused report must reach no session: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
	// Three frames and the release: the echo comes back, then the end.
	pcm := make([]byte, 640)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	for seq := uint32(1); seq <= 3; seq++ {
		if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(16000, 1, audio.KindPCM, seq, int64(seq-1)*320, pcm)); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(16000, 1, audio.KindEnd, 4, 960, nil)); err != nil {
		t.Fatal(err)
	}
	var got []audio.Frame
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < 4 && time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		typ, data, err := conn.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if typ != websocket.MessageBinary {
			continue // a text frame (the closed state) may interleave
		}
		f, fr, err := decodeVoiceStream(data)
		if err != nil {
			t.Fatal(err)
		}
		if f != (audio.Format{Rate: 16000, Channels: 1}) {
			t.Fatalf("output format %v", f)
		}
		got = append(got, fr)
	}
	if len(got) != 4 {
		t.Fatalf("the page received %d frames back, want 4", len(got))
	}
	for i := 0; i < 3; i++ {
		if got[i].Kind != audio.KindPCM || got[i].Start != int64(i)*320 || string(got[i].PCM) != string(pcm) {
			t.Fatalf("echo %d: %+v", i, got[i])
		}
	}
	if got[3].Kind != audio.KindEnd || got[3].Start != 960 {
		t.Fatalf("the output ends where the input did: %+v", got[3])
	}
	select {
	case end := <-finished:
		if end != 960 {
			t.Fatalf("the release finished the input at %d, want 960", end)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the release must finish the input")
	}
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
		t.Fatalf("the page is told the session closed: %+v", st.VoiceSession)
	} else if st.VoiceSession.Malformed != 2 {
		// The frames the driver withheld because they did not decode ride
		// the session's own status (Q11).
		t.Fatalf("the closed session's status carries %d malformed frames, want 2", st.VoiceSession.Malformed)
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(plane.Endpoints()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if eps := plane.Endpoints(); len(eps) != 0 {
		t.Fatalf("the page's endpoints leave the plane once given back: %+v", eps)
	}
	_ = json.Marshal
}

// The interleaving the retirement check alone missed: a refusal already
// past its relevance check when Off arrives. Retirement waits for that
// send to finish, so the refusal lands before the session is marked
// retiring (and before the close that follows), never after; and no
// refusal starts sending once retirement is marked.
func TestARefusalMidSendFinishesBeforeRetirementAndNoneStartsAfter(t *testing.T) {
	cv := &connVoice{session: &stubSession{done: make(chan struct{})}}
	inSend, release := make(chan struct{}), make(chan struct{})
	sent := make(chan string, 2)
	go cv.reportRefusal(voiceInterrupt, errors.New("session not ready"), func(s string) {
		close(inSend)
		<-release
		sent <- s
	})
	<-inSend
	retired := make(chan struct{})
	go func() { cv.retire(); close(retired) }()
	select {
	case <-retired:
		t.Fatal("retirement was marked while a refusal was mid-send: the page could receive it after moving on")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-retired:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement never followed the finished send")
	}
	if len(sent) != 1 {
		t.Fatalf("the refusal under way is sent exactly once: %d", len(sent))
	}
	cv.reportRefusal(voiceInterrupt, errors.New("session not ready"), func(s string) { sent <- s })
	if len(sent) != 1 {
		t.Fatal("a refusal reported after retirement reached the page")
	}
}

// Admission is locked; the browser write is not. A send callback is allowed
// to take reportMu itself, which deterministically deadlocks if the caller
// holds that lock across the callback.
func TestARefusalSendDoesNotHoldTheRetirementLock(t *testing.T) {
	cv := &connVoice{session: &stubSession{done: make(chan struct{})}}
	done := make(chan struct{})
	go func() {
		cv.reportRefusal(voiceInterrupt, errors.New("session not ready"), func(string) {
			cv.reportMu.Lock()
			_ = cv.retiring.Load()
			cv.reportMu.Unlock()
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the refusal callback ran under the retirement lock")
	}
}
