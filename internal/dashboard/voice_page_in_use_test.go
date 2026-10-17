package dashboard

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// THE HAND IS CARRIED TO THE APPLICATION, AND ONLY WHERE THE PAGE SAID SO.
func TestAnOpenCarriesTheOperatorsGestureOnlyWhenThePageSaysSo(t *testing.T) {
	if OperatorGesture(context.Background()) {
		t.Fatal("a plain context carries a gesture")
	}
	if !OperatorGesture(WithOperatorGesture(context.Background())) {
		t.Fatal("a marked context lost its gesture")
	}
}

// A REFUSED OPEN SAYS WHICH PAGE HOLDS THE VOICE, when another page of the
// identity does. The words of the control stay as they were; the page is
// told one more sentence it can act on.
func TestVoiceOpenRefusalWordsAreUnchangedByThemselves(t *testing.T) {
	said := voiceOpenRefusal("127.0.0.1:1", voiceOutputMode, ErrVoiceEngineRefused)
	if said != "The speech engine couldn't start speaking." {
		t.Fatalf("the control's sentence changed: %q", said)
	}
	if strings.Contains(said, "Another page") {
		t.Fatalf("the holder was named where none was asked about: %q", said)
	}
}

// The sentence names the machine a page is on and never the port of its
// connection, and says so when that machine is the asking page's own.
func TestTheHoldingPageIsNamedByItsMachine(t *testing.T) {
	for _, c := range []struct{ asking, holding, want string }{
		{"127.0.0.1:50001", "192.0.2.7:41000", "Another page holds the voice session (the page at 192.0.2.7)."},
		{"192.0.2.7:41000", "127.0.0.1:50001", "Another page holds the voice session (the page at 127.0.0.1)."},
		{"127.0.0.1:50001", "127.0.0.1:50002", "Another page on this machine holds the voice session."},
		{"[2001:db8::1]:50001", "[2001:db8::2]:443", "Another page holds the voice session (the page at 2001:db8::2)."},
		{"127.0.0.1:50001", "no-port", "Another page holds the voice session (the page at no-port)."},
	} {
		if got := voiceHeldElsewhere(c.asking, c.holding); got != c.want {
			t.Errorf("asked from %s with %s holding: %q, want %q", c.asking, c.holding, got, c.want)
		}
	}
}

// THROUGH THE SOCKET, WITH TWO PAGES. The first page opens and holds the
// engine's one session. The second is refused by the engine and is told
// which page holds it; its open carried the operator's gesture to the
// application only because the page said so. (A page refused with no other
// page holding anything is told the control's sentence and nothing more:
// TestARefusedOpenIsOneSentenceAndAHostRefusalStaysPlain.)
func TestARefusedPageIsToldWhichPageHoldsTheVoice(t *testing.T) {
	plane := audio.NewPlane()
	held := &heldSession{done: make(chan struct{}), released: make(chan struct{}), inputClosed: make(chan struct{})}
	var mu sync.Mutex
	var gestures []bool
	opened := false
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		AudioPlane:  func() *audio.Plane { return plane },
		VoiceEngine: func() bool { return true },
		VoiceSessionOpen: func(ctx context.Context, _, _, _ string, _ *CaptureProcessing) (VoiceSession, error) {
			mu.Lock()
			defer mu.Unlock()
			gestures = append(gestures, OperatorGesture(ctx))
			if !opened {
				opened = true
				return held, nil
			}
			return nil, errOpenRefused
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	open := func(gesture bool) ServerMessage {
		t.Helper()
		conn := dialWS(t, addr)
		drainUntil(t, conn, "status")
		sendMsg(t, conn, ClientMessage{Type: "voice_session", Voice: &VoiceRequest{Action: "open", Mode: voiceOutputMode, Rate: 48000, Channels: 1, Gesture: gesture}})
		return drainUntil(t, conn, "voice_session")
	}
	if st := open(false); st.VoiceSession == nil || st.VoiceSession.State != "open" {
		t.Fatalf("the first page's open: %+v", st.VoiceSession)
	}
	const want = "The speech engine couldn't start speaking. Another page on this machine holds the voice session."
	for _, gesture := range []bool{true, false} {
		st := open(gesture)
		if st.VoiceSession == nil || st.VoiceSession.State != "refused" || st.VoiceSession.Reason != want {
			t.Fatalf("a page refused while another holds the session (gesture %v) was told %+v, want %q", gesture, st.VoiceSession, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gestures) != 3 || gestures[0] || !gestures[1] || gestures[2] {
		t.Fatalf("the gesture each open carried to the application: %v, want [false true false]", gestures)
	}
}
