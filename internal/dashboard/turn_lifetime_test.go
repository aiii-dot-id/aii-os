package dashboard

import (
	"context"
	"testing"
	"time"
)

// THE RELOAD-KILL DEFECT (live 2026-08-25, "LLM call abandoned (caller
// ended it): context canceled"), pinned as a seam test so it cannot
// return quietly.
//
// The defect: handleWS ran the turn goroutine under r.Context() — the
// HTTP request's lifetime, which ends when ONE browser connection
// closes. A page reload mid-turn cancelled the running LLM call. The
// turn belongs to the identity, not to whichever socket opened it.
//
// The contract this pins: a turn opened on connection A is NOT
// cancelled when A closes, and its finished response is broadcast, so
// the reloaded page (connection B) receives it.
func TestTurnSurvivesOwnerConnectionClose(t *testing.T) {
	turnStarted := make(chan struct{})
	release := make(chan struct{})

	h := &WSHandler{
		IdentityName: "X",
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

	// The reloaded page connects BEFORE the turn ends.
	connB := dialWS(t, addr)
	defer connB.CloseNow()

	time.Sleep(50 * time.Millisecond) // let the close settle
	close(release)                    // the turn runs to completion

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
		IdentityName: "X",
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
