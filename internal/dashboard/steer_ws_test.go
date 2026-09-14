package dashboard

import (
	"context"
	"errors"
	"sync"
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

// .
// .
type steerHarness struct {
	mu      sync.Mutex
	steers  []string
	active  bool
	release chan struct{}
	done    chan struct{}
	ctxSeen context.Context
}

func newSteerHarness() *steerHarness {
	return &steerHarness{release: make(chan struct{}), done: make(chan struct{})}
}

func (h *steerHarness) handler() *WSHandler {
	return &WSHandler{
		Speaker:  "identity",
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "X"}, nil },
		HandleMessage: func(ctx context.Context, msg string) (string, error) {
			h.mu.Lock()
			h.active = true
			h.ctxSeen = ctx
			h.mu.Unlock()
			select {
			case <-h.release:
			case <-ctx.Done():
				h.mu.Lock()
				h.active = false
				h.mu.Unlock()
				close(h.done)
				return "", ctx.Err()
			case <-time.After(10 * time.Second):
				return "", errors.New("harness turn was never released")
			}
			h.mu.Lock()
			h.active = false
			h.mu.Unlock()
			close(h.done)
			return "answered: " + msg, nil
		},
		TurnActive: func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.active
		},
		Steer: func(text string) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if !h.active {
				return false, nil
			}
			h.steers = append(h.steers, text)
			return true, nil
		},
		PendingSteers: func() []string {
			h.mu.Lock()
			defer h.mu.Unlock()
			out := make([]string, len(h.steers))
			copy(out, h.steers)
			return out
		},
		CancelTurn: func() bool {
			h.mu.Lock()
			ctx := h.ctxSeen
			live := h.active
			h.mu.Unlock()
			_ = ctx
			return live
		},
	}
}

func (h *steerHarness) steered() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.steers))
	copy(out, h.steers)
	return out
}

func TestOperatorReachesARunningTurnOverWebSocket(t *testing.T) {
	h := newSteerHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "start the long job"})

	// .
	// .
	// .
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		live := h.active
		h.mu.Unlock()
		if live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never started — the read loop may still be blocking on it")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// .
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "stop, that file is already fixed"})

	ack := drainUntil(t, conn, "steered")
	if ack.Message != "stop, that file is already fixed" {
		t.Fatalf("the acknowledgement did not carry the operator's words: %+v", ack)
	}
	if got := h.steered(); len(got) != 1 || got[0] != "stop, that file is already fixed" {
		t.Fatalf("the words did not reach the running turn: %v", got)
	}

	close(h.release)
	if resp := drainUntil(t, conn, "response"); resp.Message == "" {
		t.Fatal("the turn produced no answer after being steered")
	}
}

// .
func TestCancelReachesARunningTurnOverWebSocket(t *testing.T) {
	h := newSteerHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "cancel"})
	if m := drainUntil(t, conn, "error"); m.Message == "" {
		t.Fatal("cancelling with no turn running must say so, not stay silent")
	}

	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "start the long job"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		live := h.active
		h.mu.Unlock()
		if live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	sendMsg(t, conn, ClientMessage{Type: "cancel"})
	if m := drainUntil(t, conn, "cancelled"); m.Type != "cancelled" {
		t.Fatalf("cancel was not acknowledged: %+v", m)
	}
	close(h.release)
}

// .
// .
// .
func TestAnOrdinaryMessageStillOpensATurn(t *testing.T) {
	h := newSteerHarness()
	close(h.release)
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "hello"})

	var answer string
	for i := 0; i < 20 && answer == ""; i++ {
		if m := readMsg(t, conn); m.Type == "response" && m.Message != "" {
			answer = m.Message
		}
	}
	if answer != "answered: hello" {
		t.Fatalf("an ordinary message did not open a turn; got %q", answer)
	}
	if got := h.steered(); len(got) != 0 {
		t.Fatalf("an ordinary message was swallowed as a steer: %v", got)
	}
}

// .
// .
// .
func TestSteeringQueueIsBroadcastAndEmpties(t *testing.T) {
	h := newSteerHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "start the long job"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		live := h.active
		h.mu.Unlock()
		if live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "that file is already fixed"})
	q := drainUntil(t, conn, "steering")
	if len(q.Pending) != 1 || q.Pending[0] != "that file is already fixed" {
		t.Fatalf("the broadcast queue does not carry the waiting words: %+v", q.Pending)
	}

	// .
	// .
	other := dialWS(t, addr)
	waitForConns(t, s, 2)
	sendMsg(t, other, ClientMessage{Type: "query", Query: "steering"})
	q2 := drainUntil(t, other, "steering")
	if len(q2.Pending) != 1 {
		t.Fatalf("a second screen saw a different queue: %+v", q2.Pending)
	}

	// .
	// .
	h.mu.Lock()
	h.steers = nil
	h.mu.Unlock()
	s.BroadcastSteering()
	q3 := drainUntil(t, conn, "steering")
	if len(q3.Pending) != 0 {
		t.Fatalf("the queue did not empty when the words were delivered: %+v", q3.Pending)
	}
	close(h.release)
}
