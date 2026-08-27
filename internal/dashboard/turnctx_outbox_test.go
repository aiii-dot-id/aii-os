package dashboard

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The turn context is the difference between "a reload drops the
// connection, not the work" and "a shutdown cannot stop the work":
// the plaintext serve path never armed it, so every chat turn ran on
// context.Background and survived Shutdown (D42, Sev 2026-08-26).
// Armed means: present before the first connection, reaped by
// Shutdown.
func TestPlaintextStartArmsTurnContext(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "T"})
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

// A failed write must not mark delivered: the explicit outbox query
// used to discard sendMsg's result and void every item on a closing
// socket (D43, Sev 2026-08-26). A conn the server does not know is
// the deterministic failed write — sendMsg refuses it before any
// byte moves.
func TestExplicitOutboxQueryDoesNotVoidOnFailedWrite(t *testing.T) {
	var mu sync.Mutex
	var marked []string
	h := &WSHandler{
		IdentityName: "Void",
		GetOutbox: func() ([]OutboxItem, error) {
			return []OutboxItem{{ID: "precious", To: "operator", Content: "unseen"}}, nil
		},
		MarkDelivered: func(id string) error {
			mu.Lock()
			marked = append(marked, id)
			mu.Unlock()
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.sendOutbox(context.Background(), &websocket.Conn{}, h)
	mu.Lock()
	defer mu.Unlock()
	if len(marked) != 0 {
		t.Fatalf("a failed outbox write still marked delivered: %v — the message was voided", marked)
	}
}

// The success half of the same rule: a write that lands marks.
func TestExplicitOutboxQueryMarksAfterWrite(t *testing.T) {
	var mu sync.Mutex
	live := false
	var marked []string
	h := &WSHandler{
		IdentityName: "Mark",
		GetOutbox: func() ([]OutboxItem, error) {
			mu.Lock()
			defer mu.Unlock()
			if !live {
				return nil, nil
			}
			return []OutboxItem{{ID: "precious", To: "operator", Content: "seen"}}, nil
		},
		MarkDelivered: func(id string) error {
			mu.Lock()
			marked = append(marked, id)
			mu.Unlock()
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())
	_ = dialWS(t, addr)

	var sconn *websocket.Conn
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
