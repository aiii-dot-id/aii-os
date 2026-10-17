package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

func TestAPlaybackControlReachesTheHoldingPageInOrder(t *testing.T) {
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
	t.Cleanup(func() {
		for _, c := range []chan struct{}{held.done, held.released} {
			select {
			case <-c:
			default:
				close(c)
			}
		}
		_ = s.Shutdown(context.Background())
	})
	holder, other := dialWS(t, addr), dialWS(t, addr)
	drainUntil(t, holder, "status")
	drainUntil(t, other, "status")
	sendMsg(t, holder, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: "meeting", Rate: 16000, Channels: 1}})
	if st := drainUntil(t, holder, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("rig: open: %+v", st.VoiceSession)
	}
	if err := s.DeliverVoiceControl(VoiceEvent{Type: "speech_start", SessionID: "vs-other"}); !errors.Is(err, ErrNoVoiceHolder) {
		t.Fatalf("a control for a session no page holds: %v", err)
	}
	if got := s.VoiceControlsUnheld(); got != 1 {
		t.Fatalf("a control no page held was counted %d times; want 1", got)
	}
	for _, typ := range []string{"speech_start", "interruption_requested", "synthesis_start"} {
		if err := s.DeliverVoiceControl(VoiceEvent{Type: typ, SessionID: held.ID(), Stream: 7}); err != nil {
			t.Fatalf("%s for the held session: %v", typ, err)
		}
	}
	var got []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for len(got) < 3 {
		var m ServerMessage
		if err := wsjson.Read(ctx, holder, &m); err != nil {
			t.Fatalf("the holding page received %v, then: %v", got, err)
		}
		if m.Type == "voice_event" && m.VoiceEvent != nil && m.VoiceEvent.SessionID == held.ID() {
			got = append(got, m.VoiceEvent.Type)
		}
	}
	if got[0] != "speech_start" || got[1] != "interruption_requested" || got[2] != "synthesis_start" {
		t.Fatalf("the holding page received its controls out of order: %v", got)
	}
	// The other page holds no session: it is sent none of them.
	octx, ocancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer ocancel()
	for {
		var m ServerMessage
		if err := wsjson.Read(octx, other, &m); err != nil {
			break // nothing more within the bound: what is asserted is that nothing was a control
		}
		if m.Type == "voice_event" && m.VoiceEvent != nil && m.VoiceEvent.SessionID == held.ID() {
			t.Fatalf("a page that holds no session was sent a playback control: %+v", m.VoiceEvent)
		}
	}
	// A session that is over has no audio left to stop on its page.
	close(held.done)
	if st := drainUntil(t, holder, "voice_session"); st.VoiceSession == nil || st.VoiceSession.State != "closed" {
		t.Fatalf("rig: closed: %+v", st.VoiceSession)
	}
	if err := s.DeliverVoiceControl(VoiceEvent{Type: "speech_start", SessionID: held.ID()}); !errors.Is(err, ErrNoVoiceHolder) {
		t.Fatalf("a control for a session that is over: %v", err)
	}
	if n := s.VoiceControlsFailed(); n != 0 {
		t.Fatalf("controls failed: %d", n)
	}
}
