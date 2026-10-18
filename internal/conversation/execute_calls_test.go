package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// execute_calls_test.go — a round's tool calls: what each walk (the
// serial one and the parallel read batch) does when a call cannot be
// run, recorded, or ends the turn.

// A call named with no executor to run it ends the turn with the error,
// after it was announced: the operator saw it asked for.
func TestACallWithNoExecutorEndsTheTurn(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{resp("looking", toolCall("c1", "bash", `{}`)), textResp("never")}}
	em := &fakeEmitter{}
	res, err := New(client, nil, &fakeDefs{}, nil, em, Config{}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != `tool "bash" requested but no executor is available` {
		t.Fatalf("err = %v", err)
	}
	if len(em.emitted) != 1 || em.emitted[0] != "tool_call:bash" {
		t.Fatalf("emitted = %v, want the call announced before the refusal", em.emitted)
	}
	if client.calls != 1 || res.ToolCallsUsed != 1 || res.Spoken != "looking" {
		t.Fatalf("calls %d, result %+v", client.calls, res)
	}
}

// stopFirst stops the turn while it runs its first call and counts
// every call it runs.
type stopFirst struct {
	stop context.CancelFunc
	ran  []string
}

func (s *stopFirst) Execute(_ context.Context, call llm.ToolCall) Observation {
	s.ran = append(s.ran, call.Function.Name)
	s.stop()
	return Observation{Text: "done"}
}

// In the serial walk a call that had not begun when the turn stopped is
// recorded, not run, and its result is the not-run notice.
func TestASerialCallAfterAStopIsRecordedNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tools := &stopFirst{stop: cancel}
	tr := &fakeTranscript{}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "first", `{}`), toolCall("c2", "second", `{}`)), textResp("never")}}
	_, err := New(client, tools, &fakeDefs{}, tr, nil, Config{}).Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(tools.ran) != 1 || tools.ran[0] != "first" {
		t.Fatalf("ran %v, want only the call begun before the stop", tools.ran)
	}
	var phases []string
	for _, e := range tr.events {
		phases = append(phases, e.tool+":"+e.phase)
	}
	if got := strings.Join(phases, ","); got != "first:started,first:done,second:started,second:done" {
		t.Fatalf("records = %s", got)
	}
	if last := tr.events[3]; !last.failed || !strings.HasPrefix(last.result, "Not run") || last.ordinal != 2 {
		t.Fatalf("the unbegun call's record = %+v", last)
	}
	if client.calls != 1 {
		t.Fatalf("provider calls = %d, want none after the stop", client.calls)
	}
}

// doneFails records starts and refuses every result row.
type doneFails struct {
	fakeTranscript
}

func (d *doneFails) RecordToolDone(string, int, string, string, Observation) error {
	return errors.New("disk full")
}

// A result that cannot be recorded ends the turn before the next call runs.
func TestAResultThatCannotBeRecordedEndsTheTurn(t *testing.T) {
	tools := &fakeTools{results: map[string]string{"a": "ok", "b": "ok"}}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "a", `{}`), toolCall("c2", "b", `{}`)), textResp("never")}}
	_, err := New(client, tools, &fakeDefs{}, &doneFails{}, nil, Config{}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != "record tool result: disk full" {
		t.Fatalf("err = %v", err)
	}
	if len(tools.calls) != 1 || client.calls != 1 {
		t.Fatalf("tools ran %v, provider calls %d", tools.calls, client.calls)
	}
}

// safeYield is a parallel-safe executor whose "work" call yields with an
// answer, the way `work yield answer=` does.
type safeYield struct {
	mu  sync.Mutex
	ran []string
}

func (s *safeYield) Execute(_ context.Context, call llm.ToolCall) Observation {
	s.mu.Lock()
	s.ran = append(s.ran, call.Function.Name)
	s.mu.Unlock()
	if call.Function.Name == "work" {
		return Observation{Text: "yielding", EndTurn: true, EndTurnAnswer: "X holds"}
	}
	return Observation{Text: "read"}
}

func (s *safeYield) ParallelSafe(llm.ToolCall) bool { return true }

// A yield inside a parallel read batch ends the turn as a serial one does,
// its answer spoken.
func TestAYieldInAParallelBatchEndsTheTurn(t *testing.T) {
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("c1", "read", `{}`), toolCall("c2", "work", `{}`)),
		textResp("standing by"),
	}}
	res, err := New(client, &safeYield{}, &fakeDefs{}, nil, nil, Config{MaxIterations: 10}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Yielded || len(client.seen) != 2 {
		t.Fatalf("yielded %v after %d calls", res.Yielded, len(client.seen))
	}
	if !strings.Contains(res.Spoken, "standing by\n\nBest current answer: X holds\n\n[Turn yielded after 2 tool calls") {
		t.Fatalf("spoken = %q", res.Spoken)
	}
	if !strings.Contains(joined(client.seen[1]), "You yielded this turn") {
		t.Fatal("the closing call was not told the turn yielded")
	}
}

// A parallel batch whose start rows cannot be written launches nothing.
func TestAParallelBatchThatCannotRecordItsStartRunsNothing(t *testing.T) {
	tools := &safeYield{}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "read", `{}`), toolCall("c2", "read", `{}`)), textResp("never")}}
	em := &fakeEmitter{}
	_, err := New(client, tools, &fakeDefs{}, &fakeTranscript{err: errors.New("disk full")}, em, Config{}).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil || err.Error() != "record tool start: disk full" {
		t.Fatalf("err = %v", err)
	}
	if len(tools.ran) != 0 {
		t.Fatalf("ran %v before the batch's start was recorded", tools.ran)
	}
	if len(em.emitted) != 1 {
		t.Fatalf("emitted %v: the first call is announced, then its record fails", em.emitted)
	}
}
