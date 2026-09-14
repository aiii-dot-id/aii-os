package dashboard

import (
	"context"
	"github.com/coder/websocket"
	"sync"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .

func TestTurnFramesReachReloadedPage(t *testing.T) {
	block := make(chan struct{})
	var mu sync.Mutex
	turnLive := false
	active := func() bool { mu.Lock(); defer mu.Unlock(); return turnLive }
	s := New("127.0.0.1", 0, &WSHandler{
		Speaker:    "identity",
		TurnActive: active,
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			mu.Lock()
			turnLive = true
			mu.Unlock()
			<-block
			mu.Lock()
			turnLive = false
			mu.Unlock()
			return "done after reload", nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// .
	// .
	pageOne := dialWS(t, addr)
	sendMsg(t, pageOne, map[string]interface{}{"type": "chat", "message": "begin the turn"})
	m := drainUntil(t, pageOne, "response")
	if !m.Stream {
		t.Fatalf("page one: expected stream:true response, got %+v", m)
	}

	// .
	pageTwo := dialWS(t, addr)
	waitForConns(t, s, 2)

	// .
	// .
	m2 := drainUntil(t, pageTwo, "response")
	if !m2.Stream {
		t.Fatalf("reloaded page: expected stream:true response, got %+v", m2)
	}

	// .
	close(block)
	done := 0
	for _, page := range []*websocket.Conn{pageOne, pageTwo} {
		for i := 0; i < 20; i++ {
			m := readMsg(t, page)
			if m.Type == "response" && m.Done {
				if m.Message != "done after reload" {
					t.Fatalf("reply %q", m.Message)
				}
				done++
				break
			}
		}
	}
	if done != 2 {
		t.Fatalf("final reply reached %d of 2 pages", done)
	}
}
