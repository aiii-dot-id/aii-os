package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// heldReads are read-only calls: hold_* wait for the turn to end, and
// "last" counts whether it ran at all.
type heldReads struct {
	started chan struct{}
	mu      sync.Mutex
	ranLast bool
}

func (h *heldReads) Execute(ctx context.Context, call llm.ToolCall) Observation {
	if call.Function.Name == "last" {
		h.mu.Lock()
		h.ranLast = true
		h.mu.Unlock()
		return Observation{Text: "read"}
	}
	h.started <- struct{}{}
	<-ctx.Done()
	return Observation{Text: ctx.Err().Error(), Failed: true}
}

func (h *heldReads) ParallelSafe(llm.ToolCall) bool { return true }

func TestAQueuedParallelReadIsNotRunAfterAStop(t *testing.T) {
	tools := &heldReads{started: make(chan struct{}, 4)}
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("1", "hold_1", `{}`), toolCall("2", "hold_2", `{}`), toolCall("3", "hold_3", `{}`), toolCall("4", "hold_4", `{}`), toolCall("5", "last", `{}`)),
		textResp("done"),
	}}
	tr := &fakeTranscript{}
	loop := New(client, tools, &fakeDefs{}, tr, nil, testBounds(Config{MaxIterations: limits.Of(8)}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
		done <- outcome{res, err}
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-tools.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the four held reads never all started")
		}
	}
	cancel()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped turn did not end")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("a stopped turn ends cancelled, got %v", got.err)
	}
	tools.mu.Lock()
	ranLast := tools.ranLast
	tools.mu.Unlock()
	if ranLast {
		t.Fatal("the read that queued for a slot ran after the stop")
	}
	var last *toolEvent
	for i := range tr.events {
		if e := &tr.events[i]; e.tool == "last" && e.phase == "done" {
			last = e
		}
	}
	if last == nil || !last.failed || !strings.HasPrefix(last.result, "Not run") {
		t.Fatalf("the queued read's record: %+v", last)
	}
	if len(client.seen) != 1 || got.res.Usage.Calls != 1 {
		t.Fatalf("provider calls begun %d, counted %d; after the stop none may begin", len(client.seen), got.res.Usage.Calls)
	}
}

// stopper stops its turn while it runs, as the operator's stop would.
type stopper struct{ stop context.CancelFunc }

func (s *stopper) Execute(context.Context, llm.ToolCall) Observation {
	if s.stop != nil {
		s.stop()
	}
	return Observation{Text: "done"}
}

func TestTheLastRoundBeginsNoClosingCallAfterAStop(t *testing.T) {
	for _, c := range []struct {
		name      string
		rounds    int
		stop      bool
		wantCalls int
	}{
		{"stopped in the last round", 1, true, 1},
		{"stopped with rounds to spare", 8, true, 1},
		{"the last round, not stopped", 1, false, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tools := &stopper{}
			if c.stop {
				tools.stop = cancel
			}
			client := &captureLLM{script: []llm.Response{resp("", toolCall("1", "work", `{}`)), textResp("closing")}}
			loop := New(client, tools, &fakeDefs{}, &fakeTranscript{}, nil, testBounds(Config{MaxIterations: limits.Of(c.rounds)}))
			res, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
			if c.stop && !errors.Is(err, context.Canceled) {
				t.Fatalf("a stopped turn ends cancelled, got %v", err)
			}
			if !c.stop && err != nil {
				t.Fatal(err)
			}
			if len(client.seen) != c.wantCalls || res.Usage.Calls != c.wantCalls {
				t.Fatalf("provider calls begun %d, counted %d; want %d", len(client.seen), res.Usage.Calls, c.wantCalls)
			}
		})
	}
}
