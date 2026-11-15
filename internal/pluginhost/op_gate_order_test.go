package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A CALL ALREADY WAITING AT THE GATE TAKES IT WHEN IT IS RELEASED, before any
// caller that comes after the release, however soon (H2's order rule). The
// gate is a one-place channel: a release on a full channel with a sender
// blocked on it moves that sender's value into the buffer as part of the
// receive, so the slot passes to the waiter in the release itself, and no
// later caller can find it free. A scheduled operation's continuation comes a
// gap after its call has ended; an identity call made during that call is the
// waiter, so it goes first by construction, not by the gap's length.
func TestACallWaitingAtTheGateTakesItBeforeAnyLaterCaller(t *testing.T) {
	var g opGate
	if err := g.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	took := make(chan struct{})
	go func() {
		if err := g.acquire(context.Background()); err == nil {
			close(took)
		}
	}()
	// The property needs the waiter PARKED on the gate before the release;
	// that is read from the goroutine's own state, not assumed after a sleep.
	deadline := time.Now().Add(10 * time.Second)
	for !parkedOnGate() {
		if time.Now().After(deadline) {
			t.Fatal("the waiting call never parked on the gate")
		}
		runtime.Gosched()
	}
	g.release()
	// Released, with nothing between: a later caller finds no room.
	if g.tryAcquire() {
		t.Fatal("A CALLER THAT CAME AFTER THE RELEASE TOOK THE GATE FROM THE CALL WAITING AT IT")
	}
	select {
	case <-took:
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting call did not take the gate")
	}
	g.release()
	if !g.tryAcquire() {
		t.Fatal("a gate released by its last holder is free")
	}
}

// parkedOnGate reports whether some goroutine is blocked sending in
// opGate.acquire.
func parkedOnGate() bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "[select]") && strings.Contains(g, "(*opGate).acquire") {
			return true
		}
	}
	return false
}

// THE IDENTITY GOES FIRST, through the real dispatch path. A review's order: operation B holds the activation's
// turn; operation A's scheduled continuation queues for it; then an identity
// call queues. First come would give A the turn; the identity's call must
// have it first.
func TestAScheduledCallQueuedFirstStillGoesAfterTheIdentitysCall(t *testing.T) {
	var gate opGate
	desc := &opDescriptor{effects: "read.internal"}
	var mu sync.Mutex
	var order []string
	ran := func(name string) invoker {
		return orderInvoker(func() { mu.Lock(); order = append(order, name); mu.Unlock() })
	}
	held := make(chan struct{})
	b := &operationTool{name: "pl_b", operation: "b", plugin: "org.example.p", inv: blockingInvoker(held), desc: desc, inFlight: &gate}
	go func() { _, _ = b.Execute(context.Background(), map[string]any{}) }()
	pollUntil(t, "B to hold the turn", func() bool {
		if gate.tryAcquire() {
			gate.release()
			return false
		}
		return true
	})

	scheduled := &operationTool{name: "pl_a", operation: "a", plugin: "org.example.p", inv: ran("scheduled"), desc: desc, inFlight: &gate}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = scheduled.Execute(Scheduled(context.Background()), map[string]any{})
		done <- struct{}{}
	}()
	// Each queued call is counted parked on the gate, whichever way it waits:
	// the order is what is tested, not which function the wait is in.
	pollUntil(t, "the scheduled call to queue", func() bool { return parkedOnGateCount() == 1 })

	identity := &operationTool{name: "pl_i", operation: "i", plugin: "org.example.p", inv: ran("identity"), desc: desc, inFlight: &gate}
	go func() { _, _ = identity.Execute(context.Background(), map[string]any{}); done <- struct{}{} }()
	pollUntil(t, "the identity's call to queue", func() bool { return parkedOnGateCount() == 2 })

	close(held)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("a queued call never ran")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "identity,scheduled" {
		t.Fatalf("THE SCHEDULED CALL WENT BEFORE THE IDENTITY'S: %v", order)
	}
}

// orderInvoker says it ran, then answers.
type orderInvoker func()

func (o orderInvoker) Invoke(context.Context, []byte) ([]byte, error) {
	o()
	return json.Marshal(map[string]any{"jsonrpc": "2.0", "id": harnessRequestID, "result": json.RawMessage(`{}`)})
}

// parkedOnGateCount is how many goroutines are blocked in a select inside one
// of opGate's waits.
func parkedOnGateCount() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	count := 0
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "[select]") && strings.Contains(g, "(*opGate).acquire") {
			count++
		}
	}
	return count
}

// A SCHEDULED CALL CAN GIVE UP in either of its waits:
// while another caller is counted waiting, and while the turn is held with
// none waiting. Either way it returns its own context's reason, and the gate
// is as it was.
func TestAScheduledCallGivesUpInEitherOfItsWaits(t *testing.T) {
	t.Run("while another caller waits", func(t *testing.T) {
		var gate opGate
		if err := gate.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		other := make(chan error, 1)
		go func() { other <- gate.acquire(context.Background()) }()
		pollUntil(t, "the other caller to wait", func() bool { return parkedOnGateCount() == 1 })
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := gate.acquireAfterOthers(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a scheduled call behind a waiting caller did not leave on its deadline: %v", err)
		}
		gate.release()
		if err := <-other; err != nil {
			t.Fatalf("the waiting caller lost its turn: %v", err)
		}
		gate.release()
		if !gate.tryAcquire() {
			t.Fatal("the gate is not free after both left")
		}
	})
	t.Run("while the turn is held", func(t *testing.T) {
		var gate opGate
		if err := gate.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		if err := gate.acquireAfterOthers(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a scheduled call behind a held turn did not leave on its deadline: %v", err)
		}
		gate.release()
		if !gate.tryAcquire() {
			t.Fatal("a scheduled call that gave up kept the turn")
		}
	})
}

// On the dispatch path, a scheduled call that gives up while queued runs
// nothing, and one whose release was withdrawn while it waited is refused,
// as any other call is.
func TestAScheduledCallOnTheDispatchPathGivesUpOrIsRefusedRunningNothing(t *testing.T) {
	desc := &opDescriptor{effects: "read.internal"}
	t.Run("cancelled while queued", func(t *testing.T) {
		var gate opGate
		if err := gate.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer gate.release()
		ran := false
		op := &operationTool{name: "pl_a", operation: "a", plugin: "org.example.p", inv: orderInvoker(func() { ran = true }), desc: desc, inFlight: &gate}
		ctx, cancel := context.WithTimeout(Scheduled(t.Context()), 30*time.Millisecond)
		defer cancel()
		res, err := op.Execute(ctx, map[string]any{})
		if err != nil || res.ReasonCode != ReasonCancelledBeforeDispatch || ran {
			t.Fatalf("a scheduled call cancelled in the queue: %+v %v (ran %v)", res, err, ran)
		}
	})
	t.Run("withdrawn while queued", func(t *testing.T) {
		var gate opGate
		ran := false
		op := &operationTool{name: "pl_a", operation: "a", plugin: "org.example.p", inv: orderInvoker(func() { ran = true }), desc: desc, inFlight: &gate,
			authorized: func() bool { return false }}
		res, err := op.Execute(Scheduled(t.Context()), map[string]any{})
		if err != nil || res.ReasonCode != ReasonActivationWithdrawn || ran {
			t.Fatalf("a scheduled call of a withdrawn release: %+v %v (ran %v)", res, err, ran)
		}
		if !gate.tryAcquire() {
			t.Fatal("a refused scheduled call kept the turn")
		}
	})
}
