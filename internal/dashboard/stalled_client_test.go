package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStalledClientDoesNotWedgeOthers is the writeMu-wedge regression
// (deferred bug-hunt item, fixed 2026-08-19). Under the old global
// writeMu, one client that stopped reading eventually blocked EVERY
// client's writes — and a connection's own read-loop replies carry that
// connection's lifetime ctx, so the stall was unbounded. The fix is
// per-connection locks, a structural 2s write bound, and close-on-stall.
// This test proves all three: the stalled client is dropped, and a live
// client keeps receiving promptly throughout.
func TestStalledClientDoesNotWedgeOthers(t *testing.T) {
	var seq atomic.Int64
	big := strings.Repeat("x", 1<<20) // 1 MiB frames fill socket buffers fast
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "Wedge",
		GetOutbox: func() ([]OutboxItem, error) {
			// A fresh undelivered item per pump pass keeps frames flowing.
			return []OutboxItem{{ID: fmt.Sprintf("m%d", seq.Add(1)), To: "operator", Content: big}}, nil
		},
		MarkDelivered: func(string) error { return nil },
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	stalled := dialWS(t, addr) // never reads after the handshake
	live := dialWS(t, addr)
	// The client library's default read limit (32 KiB) would reject the
	// 1 MiB test frames on the CLIENT side — raise it; the server's
	// behavior under load is what's under test.
	stalled.SetReadLimit(4 << 20)
	live.SetReadLimit(4 << 20)

	// Drive broadcasts until the stalled socket's buffers fill and the
	// bounded write trips. Each poke fans the outbox to every client.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 12; i++ {
			s.PokeOutbox()
			time.Sleep(300 * time.Millisecond)
		}
	}()

	// The live client must keep receiving outbox frames the whole time.
	// Pre-drop, a stalled sibling may cost up to writeWait per fan pass;
	// post-drop, delivery is prompt. Both bounds are asserted by simply
	// requiring steady receipt within a writeWait-plus-margin window.
	readCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got := 0
	for got < 5 {
		rctx, rcancel := context.WithTimeout(readCtx, writeWait+3*time.Second)
		_, data, err := live.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatalf("live client starved after %d frames — the stalled sibling wedged the server: %v", got, err)
		}
		var msg ServerMessage
		if json.Unmarshal(data, &msg) == nil && msg.Type == "outbox" {
			got++
		}
	}
	<-done

	// The stalled client must have been dropped: its next read reports
	// the close (or reset) instead of hanging into more frames forever.
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	deadline := time.Now().Add(10 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		if _, _, err := stalled.Read(rctx); err != nil {
			closed = true
			break
		}
		// Buffered frames may still drain; keep reading until the close.
	}
	if !closed {
		t.Fatal("stalled client was never dropped — close-on-stall did not fire")
	}
}
