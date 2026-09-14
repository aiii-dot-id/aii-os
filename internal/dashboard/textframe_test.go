package dashboard

import (
	"context"
	"strings"
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

func TestAnOversizedTextFrameIsRefusedNotParsed(t *testing.T) {
	h := newVoiceHarness()
	hd := h.handler()
	hd.AdmitChat = func(string) (bool, error) { return true, nil }
	hd.PendingSteers = func() []string { return nil }
	s := New("127.0.0.1", 0, hd)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	big := make([]byte, maxTextFrameBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := conn.Write(context.Background(), websocket.MessageText, big); err != nil {
		t.Fatalf("write: %v", err)
	}
	var refused bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !refused {
		if m := readMsgOrNil(conn); m != nil && m.Type == "error" && strings.Contains(m.Message, "too large") {
			refused = true
		}
	}
	if !refused {
		t.Fatal("an oversized text frame was not refused")
	}

	// .
	// .
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "still here"})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := readMsgOrNil(conn); m != nil && m.Type == "steered" {
			return
		}
	}
	t.Fatal("the connection did not survive the refusal")
}

// .
// .
func TestAVoiceFrameLargerThanTheTextBoundIsStillHeard(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	samples := maxTextFrameBytes
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		voiceFrame(voiceFrameVersion, 1, 48000, samples)); err != nil {
		t.Fatalf("write: %v", err)
	}
	h.waitHeard(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pcm) != samples*2 {
		t.Fatalf("audio above the text bound arrived as %d bytes, want %d", len(h.pcm), samples*2)
	}
}
