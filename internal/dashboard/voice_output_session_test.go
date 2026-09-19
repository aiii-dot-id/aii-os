package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .
func TestAnOutputSessionHasASpeakerAndNoMicrophone(t *testing.T) {
	plane := audio.NewPlane()
	type asked struct{ in, out, mode string }
	opens := make(chan asked, 1)
	made := make(chan *outputStub, 1)
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string) (VoiceSession, error) {
			opens <- asked{in, out, mode}
			b, err := plane.BindOutput("vs-out", out, true)
			if err != nil {
				return nil, err
			}
			ss := &outputStub{stubSession: stubSession{id: "vs-out", b: b, done: make(chan struct{}),
				finished: make(chan int64, 1), interrupts: make(chan struct{}, 1), reports: make(chan PlaybackReport, 1)},
				closes: make(chan string, 1)}
			made <- ss
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
	drainUntil(t, conn, "status")

	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "output", Rate: 48000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("the output session did not open: %+v", st.VoiceSession)
	}
	got := <-opens
	if got.in != "" || !strings.HasSuffix(got.out, ":spk") || got.mode != "output" {
		t.Fatalf("the application was asked for the wrong session: %+v", got)
	}
	ss := <-made
	mics, speakers := 0, 0
	for _, ep := range plane.Endpoints() {
		if ep.Input {
			mics++
		}
		if ep.Output {
			speakers++
			if ep.BoundTo != "vs-out" {
				t.Fatalf("the page's speaker is not the session's: %+v", ep)
			}
		}
	}
	if mics != 0 || speakers != 1 {
		t.Fatalf("an output session made %d microphone endpoint(s) and %d speaker(s); want none and one", mics, speakers)
	}

	// .
	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageBinary, streamFrame(48000, 1, audio.KindPCM, 1, 0, make([]byte, 960))); err != nil {
		t.Fatal(err)
	}
	if msg := drainUntil(t, conn, "error"); !strings.Contains(msg.Message, "no microphone") {
		t.Fatalf("audio for a session with no microphone must be refused in words: %+v", msg)
	}
	select {
	case end := <-ss.finished:
		t.Fatalf("an input that does not exist was finished at %d", end)
	default:
	}

	// .
	// .
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "close"}})
	select {
	case mode := <-ss.closes:
		if mode != "drain" {
			t.Fatalf("close asked for %q", mode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the close never reached the session")
	}
	ss.b.Release()
	close(ss.done)
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
		t.Fatalf("the session's end: %+v", st.VoiceSession)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(plane.Endpoints()) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(plane.Endpoints()); n != 0 {
		t.Fatalf("%d endpoint(s) were left on the plane after the session ended", n)
	}
}

// .
type outputStub struct {
	stubSession
	closes chan string
}

func (s *outputStub) Close(ctx context.Context, mode string) error { s.closes <- mode; return nil }
