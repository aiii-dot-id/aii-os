package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// textframe_test.go — the text plane keeps a bound of its own.
//
// The connection's read limit rose to 12 MiB so one binary frame can
// carry a whole utterance — and because the library has ONE limit per
// connection, JSON control messages silently inherited it. Before
// voice, text was bounded at the library's 32 KiB default; after, a
// single frame could demand a 12 MiB parse. These tests pin the
// restored separation: text refused at its own ceiling, audio still
// free to be audio.

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

	// A REFUSAL, NOT A DISCONNECT. The size ceiling the library
	// enforces kills the connection; this one answers and reads on.
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "still here"})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := readMsgOrNil(conn); m != nil && m.Type == "steered" {
			return
		}
	}
	t.Fatal("the connection did not survive the refusal")
}

// And the bound is TEXT'S, not the socket's: an utterance bigger than
// any control message could ever be is still heard.
func TestAVoiceFrameLargerThanTheTextBoundIsStillHeard(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	samples := maxTextFrameBytes // samples*2 bytes of PCM ≈ 2× the text bound
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
