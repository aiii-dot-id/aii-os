package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/coder/websocket"
)

// errFinishRefused is the engine's refusal of a finish as the driver returns
// it: the control's name and the engine's JSON-RPC error object, whose
// message names the refusal with its numbers.
var errFinishRefused = errors.New(`session control "speech.session.finish_input" refused: {"code":-32000,"message":"finish_input refused: end_sample 4800 is behind delivered 5120 (engine cutoff 5120)"}`)

// refusingSession holds its Finish until the test answers it, so a test
// can retire the lane while the engine is still deciding.
type refusingSession struct {
	*heldSession
	finishing chan int64
	answer    chan error
}

func (s *refusingSession) Finish(_ context.Context, end int64) error {
	s.finishing <- end
	return <-s.answer
}

// openRefusingPage starts a dashboard whose one session is sess, opens
// it from a page, and returns the page's socket.
func openRefusingPage(t *testing.T, sess VoiceSession) *websocket.Conn {
	t.Helper()
	plane := audio.NewPlane()
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(context.Context, string, string, string, *CaptureProcessing) (VoiceSession, error) {
			return sess, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)
	drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("open: %+v", st.VoiceSession)
	}
	return conn
}

func waitForLines(t *testing.T, logs *logsink.Capture, text string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(linesWith(logs, text)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return linesWith(logs, text)
}

func TestAFinishRefusedOnARetiringLaneTellsThePageNothing(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	sess := &refusingSession{
		heldSession: &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})},
		finishing:   make(chan int64, 1), answer: make(chan error, 1),
	}
	conn := openRefusingPage(t, sess)
	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(16000, 1, audio.KindEnd, 1, 4800, nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sess.finishing:
	case <-time.After(5 * time.Second):
		t.Fatal("the page's end frame never asked the engine to finish")
	}
	// The operator turns listening off while the engine is still deciding.
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "close"}})
	deadline := time.Now().Add(5 * time.Second)
	for sess.closedWith() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sess.closedWith() != "drain" {
		t.Fatalf("the close never reached the session: %q", sess.closedWith())
	}
	sess.answer <- errFinishRefused
	got := waitForLines(t, logs, "refused the finish")
	if len(got) != 1 {
		t.Fatalf("the moot refusal is logged once: %q", got)
	}
	for _, want := range []string{"voice.session", "session vs-held", "sample 4800", "delivered 5120", "engine cutoff 5120"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("the moot refusal's line does not carry %q: %s", want, got[0])
		}
	}
	// Nothing reaches the page: the first error it is sent after the log
	// line is the probe's own (a frame shorter than its header).
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{voiceStreamVersion}); err != nil {
		t.Fatal(err)
	}
	if m := drainUntil(t, conn, "error"); !strings.Contains(m.Message, "shorter than its header") {
		t.Fatalf("a moot finish refusal reached the page: %q", m.Message)
	}
	if got := linesWith(logs, "refused the finish"); len(got) != 1 {
		t.Fatalf("the moot refusal is logged once, not again: %q", got)
	}
}

// A RELEVANT FINISH REFUSAL REACHES THE PAGE AS ONE SENTENCE. The same
// refusal while the lane is live is the operator's to know: they are told
// that listening could not be finished and that their words so far were
// kept — never the JSON-RPC body, which goes whole to the log.
func TestAFinishRefusedOnALiveLaneIsOneSentence(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	sess := &refusingSession{
		heldSession: &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})},
		finishing:   make(chan int64, 1), answer: make(chan error, 1),
	}
	sess.answer <- errFinishRefused
	conn := openRefusingPage(t, sess)
	if err := conn.Write(context.Background(), websocket.MessageBinary, streamFrame(16000, 1, audio.KindEnd, 1, 4800, nil)); err != nil {
		t.Fatal(err)
	}
	m := drainUntil(t, conn, "error")
	if m.Message != "Couldn't finish listening — your words up to now were kept." {
		t.Fatalf("a live finish refusal reached the page as %q", m.Message)
	}
	got := waitForLines(t, logs, "refused the finish")
	if len(got) != 1 || !strings.Contains(got[0], "voice.error") || !strings.Contains(got[0], `"code":-32000`) || !strings.Contains(got[0], "sample 4800") {
		t.Fatalf("the refusal's detail is logged whole, once: %q", got)
	}
}

// errEngineRefused is the engine's own refusal of a control, as the driver
// returns it; errEngineGone is the application's mark that the engine was
// not there to answer at all.
var (
	errEngineRefused = errors.New(`session control "speech.session.stop_playback" refused: {"code":-32000,"message":"STALE_SYNTHESIS: generation 7 is not current (current 9)","data":{"reasonCode":"STALE_SYNTHESIS"}}`)
	errEngineGone    = fmt.Errorf("%w: %w", ErrVoiceEngineUnavailable, errors.New("session control not admitted: the request never reached the engine"))
	// errOpenRefused is the engine's refusal of an open, marked by the
	// application as the engine's.
	errOpenRefused = fmt.Errorf("%w: %w", ErrVoiceEngineRefused, errors.New(`session control "speech.session.open" refused: {"code":-32002,"message":"BUSY: session vs-other holds the engine (1 of 1)"}`))
)

// protocolText reports what of a JSON-RPC exchange a sentence shows: a
// JSON body or an error code.
func protocolText(s string) bool {
	return strings.ContainsAny(s, "{}") || strings.Contains(s, "-320") || strings.Contains(s, "session control")
}

func TestEveryVoiceControlFailureIsOneSentence(t *testing.T) {
	other := errors.New("session control admission unknown: sent, no reply")
	for _, c := range []struct {
		control voiceControl
		err     error
		want    string
	}{
		{voiceFinish, errFinishRefused, "Couldn't finish listening — your words up to now were kept."},
		{voiceFinish, other, "Couldn't finish listening — your words up to now were kept."},
		{voiceFinish, errEngineGone, "Voice engine restarting."},
		{voiceInterrupt, errEngineRefused, "Couldn't stop speaking."},
		{voiceInterrupt, other, "Couldn't stop speaking."},
		{voiceInterrupt, errEngineGone, "Voice engine restarting."},
		{voiceClose, errEngineRefused, "Couldn't end the voice session."},
		{voiceClose, other, "Couldn't end the voice session."},
		{voiceClose, errEngineGone, "Voice engine restarting."},
		{voiceListen, errOpenRefused, "The speech engine couldn't start listening."},
		{voiceListen, errEngineGone, "Voice engine restarting."},
		{voiceSpeak, errOpenRefused, "The speech engine couldn't start speaking."},
		{voiceSpeak, errEngineGone, "Voice engine restarting."},
		{voiceControl("unknown"), errEngineRefused, "That voice control didn't work."},
	} {
		got := voiceControlSentence(c.control, c.err)
		if got != c.want {
			t.Errorf("%s failing with %v is told %q, want %q", c.control, c.err, got, c.want)
		}
		if protocolText(got) {
			t.Errorf("%s's sentence carries protocol text: %q", c.control, got)
		}
	}
}

// failingSession fails every control the page can send: Finish answers in
// turn from finishes, the rest with errEngineRefused.
type failingSession struct {
	*heldSession
	finishes chan error
}

func (s *failingSession) Finish(context.Context, int64) error { return <-s.finishes }
func (s *failingSession) Interrupt(context.Context) error     { return errEngineRefused }
func (s *failingSession) PlaybackReport(context.Context, PlaybackReport) error {
	return errEngineRefused
}
func (s *failingSession) Close(ctx context.Context, mode string) error {
	_ = s.heldSession.Close(ctx, mode)
	return errEngineRefused
}

// voiceControlFailures drives every operator voice control against an
// engine that fails each one, through the real socket, and returns what
// the page was sent for each — the messages the browser test then hands
// to the shipped page — with the sentence each must be.
func voiceControlFailures(t *testing.T) (sent []ServerMessage, want []string) {
	t.Helper()
	sess := &failingSession{
		heldSession: &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})},
		finishes:    make(chan error, 2),
	}
	sess.finishes <- errFinishRefused
	sess.finishes <- errEngineGone
	conn := openRefusingPage(t, sess)
	ctx := context.Background()
	expect := func(sentence string) {
		t.Helper()
		m := drainUntil(t, conn, "error")
		sent, want = append(sent, m), append(want, sentence)
	}
	for seq := uint32(1); seq <= 2; seq++ {
		if err := conn.Write(ctx, websocket.MessageBinary, streamFrame(16000, 1, audio.KindEnd, seq, 4800, nil)); err != nil {
			t.Fatal(err)
		}
		if seq == 1 {
			expect("Couldn't finish listening — your words up to now were kept.")
		} else {
			expect("Voice engine restarting.")
		}
	}
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "interrupt"}})
	expect("Couldn't stop speaking.")
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "playback", Playback: &PlaybackReport{SessionID: "vs-held", Stream: 3, Rendered: 160, Rate: 16000, Channels: 1, Terminal: true, Outcome: "stopped"}}})
	if ev := drainUntil(t, conn, "voice_event"); ev.VoiceEvent == nil || ev.VoiceEvent.Type != "receipt_refused" || ev.VoiceEvent.Stream != 3 || ev.VoiceEvent.Reason != "" {
		t.Fatalf("a refused receipt carries the stream it owes and no engine detail: %+v", ev.VoiceEvent)
	}
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "close"}})
	expect("Couldn't end the voice session.")
	return sent, want
}

// Through the socket the operator's page holds: each control's failure is
// sent as exactly its sentence, the detail is logged whole, and a refused
// receipt names its stream with nothing of the engine's words.
func TestTheVoiceControlsFailInSentencesAtTheSocket(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	sent, want := voiceControlFailures(t)
	for i, m := range sent {
		if m.Message != want[i] || protocolText(m.Message) {
			t.Fatalf("control failure %d reached the page as %q, want %q", i, m.Message, want[i])
		}
	}
	for _, detail := range []string{"end_sample 4800 is behind delivered 5120", "never reached the engine", "STALE_SYNTHESIS: generation 7", "playback receipt for stream 3"} {
		if got := linesWith(logs, detail); len(got) == 0 {
			t.Fatalf("the log does not carry %q:\n%s", detail, strings.Join(logs.Lines(), "\n"))
		}
	}
}

// voiceOpenRefusals asks a dashboard to open a session against each kind
// of refusal, through the real socket, and returns the states the page was
// sent with the reason each must carry: the engine's own refusal and an
// engine not there as their sentences, a host refusal as the plain
// sentence it already is.
func voiceOpenRefusals(t *testing.T) (sent []ServerMessage, want []string) {
	t.Helper()
	const hostSaid = `"id.engine.absent" is chosen for hearing and is not a speech engine running here; no session opens on another engine in its place`
	for _, c := range []struct {
		mode string
		err  error
		want string
	}{
		{"meeting", errOpenRefused, "The speech engine couldn't start listening."},
		{"conversation", errEngineGone, "Voice engine restarting."},
		{"meeting", errors.New(hostSaid), hostSaid},
		{voiceOutputMode, errOpenRefused, "The speech engine couldn't start speaking."},
	} {
		plane := audio.NewPlane()
		h := &WSHandler{
			GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
			AudioPlane:  func() *audio.Plane { return plane },
			VoiceEngine: func() bool { return true },
			VoiceSessionOpen: func(context.Context, string, string, string, *CaptureProcessing) (VoiceSession, error) {
				return nil, c.err
			},
		}
		s := New("127.0.0.1", 0, h)
		addr, err := s.Start(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
		conn := dialWS(t, addr)
		drainUntil(t, conn, "status")
		sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: c.mode, Rate: 16000, Channels: 1}})
		m := drainUntil(t, conn, "voice_session")
		if m.VoiceSession == nil || m.VoiceSession.State != "refused" {
			t.Fatalf("a refused open (%s) was sent as %+v", c.mode, m.VoiceSession)
		}
		sent, want = append(sent, m), append(want, c.want)
	}
	return sent, want
}

func TestARefusedOpenIsOneSentenceAndAHostRefusalStaysPlain(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	sent, want := voiceOpenRefusals(t)
	for i, m := range sent {
		if got := m.VoiceSession.Reason; got != want[i] || protocolText(got) {
			t.Fatalf("refused open %d reached the page as %q, want %q", i, got, want[i])
		}
	}
	if got := linesWith(logs, "BUSY: session vs-other holds the engine"); len(got) != 2 || !strings.Contains(got[0], "the engine refused the open to listen") || !strings.Contains(got[1], "the engine refused the open to speak") {
		t.Fatalf("the engine's refusal of each open is logged whole, once: %q", got)
	}
}
