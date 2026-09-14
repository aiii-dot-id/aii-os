package dashboard

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .

type voiceHarness struct {
	mu      sync.Mutex
	pcm     []byte
	rate    int
	ch      int
	err     error
	heard   chan struct{}
	answer  bool
	once    sync.Once
	present bool
}

func newVoiceHarness() *voiceHarness {
	return &voiceHarness{heard: make(chan struct{}), present: true}
}

func (h *voiceHarness) handler() *WSHandler {
	return &WSHandler{
		Speaker:  "identity",
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "X"}, nil },
		VoiceConfigured: func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.present
		},
		HearUtterance: func(_ context.Context, pcm []byte, rate, ch int, answer bool) error {
			h.mu.Lock()
			h.pcm, h.rate, h.ch, h.answer = pcm, rate, ch, answer
			err := h.err
			h.mu.Unlock()
			h.once.Do(func() { close(h.heard) })
			return err
		},
	}
}

// .
func voiceFrame(version byte, channels byte, rate uint32, samples int) []byte {
	buf := make([]byte, voiceHeaderBytes+samples*2)
	buf[0] = version
	buf[1] = channels
	binary.LittleEndian.PutUint32(buf[4:8], rate)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(buf[voiceHeaderBytes+i*2:], uint16(i))
	}
	return buf
}

func (h *voiceHarness) waitHeard(t *testing.T) {
	t.Helper()
	select {
	case <-h.heard:
	case <-time.After(5 * time.Second):
		t.Fatal("THE UTTERANCE NEVER ARRIVED — the audio path does not carry speech")
	}
}

// .
// .
func TestASpokenUtteranceReachesTheHost(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	frame := voiceFrame(voiceFrameVersion, 1, 48000, 1600)
	if err := conn.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
		t.Fatalf("write: %v", err)
	}
	h.waitHeard(t)

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rate != 48000 || h.ch != 1 {
		t.Fatalf("format arrived as %d Hz / %d ch, want 48000/1 — the header did not survive", h.rate, h.ch)
	}
	if len(h.pcm) != 1600*2 {
		t.Fatalf("audio arrived as %d bytes, want %d — the header was not stripped, or the payload was", len(h.pcm), 1600*2)
	}
}

// .
// .
// .
// .
// .
func TestTranscriptionDoesNotStopTheSocketBeingRead(t *testing.T) {
	h := newVoiceHarness()
	block := make(chan struct{})
	hd := h.handler()
	hd.HearUtterance = func(context.Context, []byte, int, int, bool) error {
		<-block
		return nil
	}
	hd.AdmitChat = func(string) (bool, error) { return true, nil }
	hd.PendingSteers = func() []string { return nil }
	s := New("127.0.0.1", 0, hd)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	defer close(block)

	conn := dialWS(t, addr)
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		voiceFrame(voiceFrameVersion, 1, 16000, 800)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// .
	// .
	// .
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "stop"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := readMsgOrNil(conn); m != nil && m.Type == "steered" {
			return
		}
	}
	t.Fatal("NOTHING WAS READ WHILE AN UTTERANCE WAS IN FLIGHT — the microphone deafened the socket")
}

// .
// .
// .
func readMsgOrNil(conn *websocket.Conn) *ServerMessage {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil
	}
	var m ServerMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return &m
}

// .
// .
// .
func TestAVoiceFrameThisHostDoesNotUnderstandIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"wrong version", voiceFrame(voiceFrameVersion+1, 1, 16000, 100)},
		{"no audio", voiceFrame(voiceFrameVersion, 1, 16000, 0)},
		{"zero channels", voiceFrame(voiceFrameVersion, 0, 16000, 100)},
		{"impossible rate", voiceFrame(voiceFrameVersion, 1, 3, 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newVoiceHarness()
			s := New("127.0.0.1", 0, h.handler())
			addr, _ := s.Start(t.TempDir())
			defer s.Shutdown(context.Background())

			conn := dialWS(t, addr)
			if err := conn.Write(context.Background(), websocket.MessageBinary, tc.frame); err != nil {
				t.Fatalf("write: %v", err)
			}
			select {
			case <-h.heard:
				t.Fatal("a malformed frame was passed to the host as audio")
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

// .
// .
// .
func TestStatusSaysWhetherTheIdentityCanHear(t *testing.T) {
	h := newVoiceHarness()
	hd := h.handler()
	s := New("127.0.0.1", 0, hd)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	waitForConns(t, s, 1)
	if got := waitForStatus(t, conn); !got.Voice {
		t.Fatal("a configured speech endpoint was not offered to the page")
	}

	// .
	h2 := newVoiceHarness()
	h2.present = false
	s2 := New("127.0.0.1", 0, h2.handler())
	addr2, _ := s2.Start(t.TempDir())
	defer s2.Shutdown(context.Background())
	if got := waitForStatus(t, dialWS(t, addr2)); got.Voice {
		t.Fatal("a microphone was offered by an identity with no speech endpoint")
	}
}

func waitForStatus(t *testing.T, conn *websocket.Conn) *StatsResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsgOrNil(conn)
		if m != nil && m.Type == "status" && m.Stats != nil {
			return m.Stats
		}
	}
	t.Fatal("no status frame arrived")
	return nil
}
