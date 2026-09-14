package dashboard

import (
	"context"
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
// .
// .
// .
// .
func TestTurnSurvivesOwnerConnectionClose(t *testing.T) {
	turnStarted := make(chan struct{})
	release := make(chan struct{})

	h := &WSHandler{
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			close(turnStarted)
			select {
			case <-release:
				return "turn finished while owner was gone", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// .
	connA := dialWS(t, addr)
	sendMsg(t, connA, ClientMessage{Type: "chat", Message: "begin long turn"})

	select {
	case <-turnStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}

	// .
	connA.CloseNow()

	// .
	connB := dialWS(t, addr)
	defer connB.CloseNow()

	time.Sleep(50 * time.Millisecond)
	close(release)

	// .
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsg(t, connB)
		if m.Type == "error" {
			t.Fatalf("turn errored after owner reload (the defect): %s", m.Message)
		}
		if m.Type == "response" && m.Done && m.Message == "turn finished while owner was gone" {
			return
		}
	}
	t.Fatal("no finished response reached the reloaded page within 5s")
}

// .
// .
// .
func TestDetachedTurnDiesAtShutdown(t *testing.T) {
	turnStarted := make(chan struct{})
	turnReturned := make(chan error, 1)

	h := &WSHandler{
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			close(turnStarted)
			<-ctx.Done()
			turnReturned <- ctx.Err()
			return "", ctx.Err()
		},
	}

	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "begin"})
	select {
	case <-turnStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}

	s.Shutdown(context.Background())

	select {
	case err := <-turnReturned:
		if err != context.Canceled {
			t.Fatalf("turn ended with %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn leaked past shutdown — detachment created an immortal")
	}
}
