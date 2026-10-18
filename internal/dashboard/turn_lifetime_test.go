package dashboard

import (
	"context"
	"testing"
	"time"
)

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
				return "", ctx.Err() // the defect path: abort mid-turn
			}
		},
	}

	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	// Connection A opens the turn.
	connA := dialWS(t, addr)
	sendMsg(t, connA, ClientMessage{Type: "chat", Message: "begin long turn"})

	select {
	case <-turnStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}

	// The owner reloads: A drops. Before the fix this cancels the turn.
	connA.CloseNow()
	// The close has settled when the server has let A go — counted, not
	// slept on. It also makes the count below B's alone: until A's
	// handler returns, a wait for one registration would find A.
	waitForConnsAtMost(t, s, 0)

	// The reloaded page connects BEFORE the turn ends.
	connB := dialWS(t, addr)
	defer connB.CloseNow()

	waitForConns(t, s, 1)

	close(release) // the turn runs to completion

	// The response must reach B: broadcast, not owner mail.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m := readMsg(t, connB)
		if m.Type == "error" {
			t.Fatalf("turn errored after owner reload (the defect): %s", m.Message)
		}
		if m.Type == "response" && m.Done && m.Message == "turn finished while owner was gone" {
			return // pass
		}
	}
	t.Fatal("no finished response reached the reloaded page within 5s")
}

// Detachment must not create immortals: server Shutdown ends in-flight
// turns (their LLM calls are cancelled), bounded, so shutdown
// terminates. The app-level turn gate drains the goroutine itself.
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
