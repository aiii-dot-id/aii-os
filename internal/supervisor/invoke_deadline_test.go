package supervisor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvokeHonoursItsDeadlineAgainstAChildThatNeverReads(t *testing.T) {
	sup, err := Start(Spec{PluginID: "deaf.example", Argv: []string{fakechildBin, "deaf"}}, &mockDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()

	// Large enough that the pipe cannot swallow it.
	frame := []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"x","arguments":{"pad":"` +
		strings.Repeat("p", 512*1024) + `"}}}`)

	ctx := &deadlineOnceCalled{Context: context.Background(), done: make(chan struct{})}
	go func() {
		for {
			sup.mu.Lock()
			c := sup.child
			sup.mu.Unlock()
			if c != nil && c.called.Load() {
				close(ctx.done)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	start := time.Now()
	_, err = sup.Invoke(ctx, frame)
	if err == nil {
		t.Fatal("a child that never reads cannot succeed")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the deadline was not honoured: %v", elapsed)
	}
	// THE WRITE HAD BEGUN, AND THE CALLER IS TOLD SO. The frame is larger
	// than the pipe holds and the child never reads, so when the deadline
	// came part of it was written and the rest was not: no timing decides
	// that. The error is the typed one for a call whose time ran out after
	// the child was given it, not the bare context error a call gets that
	// left the queue with nothing written.
	var delivered *InvokeTimeoutError
	if !errors.As(err, &delivered) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a call cut off while its frame was being written must say delivery had begun, and keep the context's error: %v", err)
	}
	// And it says when the child it stopped had started, as a call cut off
	// waiting for its reply does (call_told_test.go).
	if delivered.Started.IsZero() || !strings.Contains(err.Error(), "child (started ") {
		t.Fatalf("a call cut off while its frame was being written must say when its child started: %v", err)
	}
}

// deadlineOnceCalled is a context whose deadline passes when the test says
// so: Done closes and Err is the deadline's error from then on.
type deadlineOnceCalled struct {
	context.Context
	done chan struct{}
}

func (d *deadlineOnceCalled) Done() <-chan struct{} { return d.done }

func (d *deadlineOnceCalled) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// And a call waiting its turn leaves when its own context says so,
// rather than sitting behind the one in front of it.
func TestAQueuedInvokeLeavesOnItsOwnDeadline(t *testing.T) {
	sup, err := Start(Spec{PluginID: "deaf.example", Argv: []string{fakechildBin, "deaf"}}, &mockDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()

	// Hold the turn with a call that will not finish quickly.
	held := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"x","arguments":{"pad":"`+
			strings.Repeat("p", 512*1024)+`"}}}`))
		held <- err
	}()

	waitFor(t, "the first call to take the turn", func() bool { return len(sup.invokeGate) == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h","method":"invoke.call","params":{"operation":"y"}}`))
	if err == nil {
		t.Fatal("the queued call cannot have succeeded")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("a queued call must leave on its own deadline: %v", elapsed)
	}
	// And it was queued for all of that: the call in front still holds the
	// turn, so what follows is said of a call that never had it.
	select {
	case err := <-held:
		t.Fatalf("rig: the call holding the turn had returned before the queued one left: %v", err)
	default:
	}
	// NOTHING OF THIS CALL WAS WRITTEN, and its error says no more than
	// that its own time ran out: the bare context error, never the typed
	// one that means the child had been given the call.
	var delivered *InvokeTimeoutError
	if errors.As(err, &delivered) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a call that left the queue was reported as one the child had been given: %v", err)
	}
	<-held
}

// bigReply answers every nested call with a reply no pipe holds whole,
// and counts the calls it was asked.
type bigReply struct{ calls atomic.Int32 }

func (d *bigReply) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	d.calls.Add(1)
	return []byte(`{"value":"` + strings.Repeat("x", 900<<10) + `"}`), nil
}

func TestAReplyToANestedCallHonoursTheDeadline(t *testing.T) {
	aNestedReplyHonoursTheDeadline(t, childSpec("hostcall-deaf"))
}

// aNestedReplyHonoursTheDeadline is the proof, for a child started from
// spec in the hostcall-deaf mode: the Windows wall runs the same proof
// over its own pipes (appcontainer_windows_test.go).
func aNestedReplyHonoursTheDeadline(t *testing.T, spec Spec) {
	t.Helper()
	d := &bigReply{}
	s := mustStart(t, spec, d)
	generation, restarts := s.Generation(), s.Restarts()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	returned := make(chan error, 1)
	go func() {
		_, err := s.Invoke(ctx, []byte(testReq))
		returned <- err
	}()
	var err error
	select {
	case err = <-returned:
	case <-time.After(10 * time.Second):
		t.Fatalf("a call with a 300ms deadline is still in flight after %v: the reply to the child's nested call was written past the deadline", time.Since(start))
	}
	if d.calls.Load() == 0 {
		t.Fatal("rig: the child's nested call never reached the host")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the deadline was not honoured: %v", took)
	}
	var cut *InvokeTimeoutError
	if !errors.As(err, &cut) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a call cut off while its child was being answered must say delivery had begun, and keep the context's error: %v", err)
	}
	waitFor(t, "the child's successor", func() bool {
		g := s.Generation()
		return g != 0 && g != generation
	})
	if n := s.Restarts(); n != restarts {
		t.Fatalf("the host's own cancel was counted against the child: restarts %d -> %d", restarts, n)
	}
	// The turn is free, and the successor serves a call that listens.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	resp, err := s.Invoke(ctx2, []byte(`{"jsonrpc":"2.0","id":"h2","method":"invoke.call","params":{"listen":true}}`))
	if err != nil || !strings.Contains(string(resp), `"upstream_bytes"`) {
		t.Fatalf("the call after a cut-off one must be served: %v %s", err, resp)
	}
}
