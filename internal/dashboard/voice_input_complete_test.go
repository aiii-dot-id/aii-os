package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .
// .
func TestTheEngineEndingTheInputReachesThePageWhileTheSessionLives(t *testing.T) {
	plane := audio.NewPlane()
	made := make(chan *stubSession, 1)
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string) (VoiceSession, error) {
			b, err := plane.Bind("vs-1", in, out, true)
			if err != nil {
				return nil, err
			}
			ss := &stubSession{id: "vs-1", b: b, p: audio.NewPump(b, newEchoChannel()),
				finished: make(chan int64, 1), interrupts: make(chan struct{}, 1),
				reports: make(chan PlaybackReport, 1), done: make(chan struct{}),
				inputClosed: make(chan struct{})}
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
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "conversation", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("open: %+v", st.VoiceSession)
	}
	ss := <-made

	// .
	ss.inputWhy = "capture_limit"
	close(ss.inputClosed)

	st := drainUntil(t, conn, "voice_session")
	if st.VoiceSession == nil || st.VoiceSession.State != "input_complete" {
		t.Fatalf("the page must be told the engine stopped listening: %+v", st.VoiceSession)
	}
	// .
	// .
	if st.VoiceSession.Reason != "capture_limit" {
		t.Fatalf("the engine's reason reaches the page: %+v", st.VoiceSession)
	}
	// .
	// .
	if st.VoiceSession.SessionID != "vs-1" {
		t.Fatalf("the completion names its session: %+v", st.VoiceSession)
	}
	// .
	// .
	select {
	case <-ss.done:
		t.Fatal("an engine-ended input does not end the session")
	default:
	}
	close(ss.done)
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
		t.Fatalf("the session's own end still follows: %+v", st.VoiceSession)
	}
}

// .
// .
// .
// .
func TestAnEndedSessionDoesNotAnnounceItsInputCompletion(t *testing.T) {
	plane := audio.NewPlane()
	made := make(chan *stubSession, 1)
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, in, out, mode string) (VoiceSession, error) {
			b, err := plane.Bind("vs-2", in, out, true)
			if err != nil {
				return nil, err
			}
			done := make(chan struct{})
			closed := make(chan struct{})
			close(done)
			close(closed)
			ss := &stubSession{id: "vs-2", b: b, p: audio.NewPump(b, newEchoChannel()),
				finished: make(chan int64, 1), interrupts: make(chan struct{}, 1),
				reports: make(chan PlaybackReport, 1), done: done,
				inputClosed: closed, inputWhy: "capture_limit"}
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
	sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "conversation", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, conn, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("open: %+v", st.VoiceSession)
	}
	<-made
	// .
	// .
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("the session's end never reached the page")
		default:
		}
		st := drainUntil(t, conn, "voice_session")
		if st.VoiceSession == nil {
			continue
		}
		if st.VoiceSession.State == "input_complete" {
			t.Fatal("a session that is already over must not stop a microphone")
		}
		if st.VoiceSession.State == "closed" {
			return
		}
	}
}
