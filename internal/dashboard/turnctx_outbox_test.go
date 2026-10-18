package dashboard

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPlaintextStartArmsTurnContext(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start("")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if addr == "" {
		t.Fatal("no address")
	}
	ctx := s.serverTurnCtx()
	if ctx == context.Background() {
		t.Fatal("turn context was never armed on the plaintext path — turns would run on Background and survive shutdown")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("armed turn context was not reaped by Shutdown")
	}
}

func TestExplicitOutboxQueryDoesNotVoidOnFailedWrite(t *testing.T) {
	var mu sync.Mutex
	var marked []string
	var settled []bool
	h := &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			return []OutboxItem{{ID: "precious", To: "operator", Content: "unseen"}}, func(shown bool) {
				mu.Lock()
				defer mu.Unlock()
				settled = append(settled, shown)
				if shown {
					marked = append(marked, "precious")
				}
			}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.sendOutbox(context.Background(), &websocket.Conn{}, h)
	mu.Lock()
	defer mu.Unlock()
	if len(marked) != 0 {
		t.Fatalf("a failed outbox write still marked delivered: %v — the message was voided", marked)
	}
	// And what was taken is given back, once, for the next carrier.
	if len(settled) != 1 || settled[0] {
		t.Fatalf("the take of a write that did not land was settled %v, want given back once", settled)
	}
}

// The success half of the same rule: a write that lands marks.
func TestExplicitOutboxQueryMarksAfterWrite(t *testing.T) {
	var mu sync.Mutex
	live := false
	var marked []string
	h := &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			mu.Lock()
			defer mu.Unlock()
			var items []OutboxItem
			if live {
				items = []OutboxItem{{ID: "precious", To: "operator", Content: "seen"}}
			}
			return items, func(shown bool) {
				if !shown {
					return
				}
				mu.Lock()
				for _, it := range items {
					marked = append(marked, it.ID)
				}
				mu.Unlock()
			}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())
	_ = dialWS(t, addr)

	var sconn carrier
	deadline := time.Now().Add(5 * time.Second)
	for sconn == nil {
		if time.Now().After(deadline) {
			t.Fatal("server never registered the connection")
		}
		time.Sleep(20 * time.Millisecond)
		s.wsMu.Lock()
		for c := range s.wsConns {
			sconn = c
		}
		s.wsMu.Unlock()
	}

	mu.Lock()
	live = true
	mu.Unlock()
	s.sendOutbox(context.Background(), sconn, h)
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, id := range marked {
		if id == "precious" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a landed write did not mark delivered (marked: %v)", marked)
	}
}
