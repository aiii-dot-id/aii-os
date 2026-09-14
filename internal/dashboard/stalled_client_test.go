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

// .
// .
// .
// .
// .
// .
// .
// .
func TestStalledClientDoesNotWedgeOthers(t *testing.T) {
	var seq atomic.Int64
	big := strings.Repeat("x", 1<<20)
	s := New("127.0.0.1", 0, &WSHandler{
		GetOutbox: func() ([]OutboxItem, error) {
			// .
			return []OutboxItem{{ID: fmt.Sprintf("m%d", seq.Add(1)), To: "operator", Content: big}}, nil
		},
		MarkDelivered: func(string) error { return nil },
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	stalled := dialWS(t, addr)
	live := dialWS(t, addr)
	waitForConns(t, s, 2)
	// .
	// .
	// .
	stalled.SetReadLimit(4 << 20)
	live.SetReadLimit(4 << 20)

	// .
	// .
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 12; i++ {
			s.PokeOutbox()
			time.Sleep(300 * time.Millisecond)
		}
	}()

	// .
	// .
	// .
	// .
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

	// .
	// .
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer rcancel()
	deadline := time.Now().Add(10 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		if _, _, err := stalled.Read(rctx); err != nil {
			closed = true
			break
		}
		// .
	}
	if !closed {
		t.Fatal("stalled client was never dropped — close-on-stall did not fire")
	}
}
