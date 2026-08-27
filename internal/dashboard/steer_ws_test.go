package dashboard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// The operator's report, as a test: "tried to interrupt Aeon in the
// middle of tool calls. No response. Tool calling continued after my
// entry." (2026-08-24)
//
// The turn ran INLINE in the WebSocket read loop, so the loop never
// returned to ReadMessage while it ran. The second message was not
// queued behind the turn — it was never read off the socket at all, and
// nothing existed to deliver it into the running turn or to stop it.
//
// §6 names WebSocket request/response correlation as a required proof,
// and a backend method test is not an end-to-end journey: this drives a
// real Server on a real port over a real socket.

// steerHarness is a handler whose "turn" blocks until released, so the
// test controls exactly when a turn is in flight.
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
		IdentityName: "X",
		Speaker:      "identity",
		GetStats:     func() (*StatsResponse, error) { return &StatsResponse{Name: "X"}, nil },
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
				return false, nil // no turn — an ordinary message
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

	// Wait for the turn to actually be in flight before speaking again —
	// otherwise the second message could win the race and simply open a
	// second turn, which would prove nothing about reaching a running one.
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

	// THE REPORTED MOMENT: speak while the identity is working.
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

// Cancel is the other half: information is not always enough.
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

// The negative control the other two rest on: with no turn running, a
// chat message must still open one. If steering swallowed ordinary
// messages the tests above could pass while the dashboard was mute.
func TestAnOrdinaryMessageStillOpensATurn(t *testing.T) {
	h := newSteerHarness()
	close(h.release) // never blocks: every turn completes immediately
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

// The queue is broadcast, not merely acknowledged. A toast fades; the
// question "has the identity heard me yet?" outlives it, and two tabs on
// one identity must not disagree about the answer.
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

	// A second screen asking must get the same answer, or the two
	// disagree about what the identity has yet to hear.
	other := dialWS(t, addr)
	sendMsg(t, other, ClientMessage{Type: "query", Query: "steering"})
	q2 := drainUntil(t, other, "steering")
	if len(q2.Pending) != 1 {
		t.Fatalf("a second screen saw a different queue: %+v", q2.Pending)
	}

	// The drain: the loop takes the words, and the queue must EMPTY —
	// the moment the operator has been waiting to see.
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
