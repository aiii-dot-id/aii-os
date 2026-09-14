package conversation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .

// .
// .
// .
type barrierTools struct {
	need    int
	mu      sync.Mutex
	arrived int
	release chan struct{}
	safe    map[string]bool
	order   []string
}

func newBarrierTools(need int, safe map[string]bool) *barrierTools {
	return &barrierTools{need: need, release: make(chan struct{}), safe: safe}
}

func (b *barrierTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	b.mu.Lock()
	b.arrived++
	b.order = append(b.order, call.Function.Name)
	if b.arrived == b.need {
		close(b.release)
	}
	b.mu.Unlock()
	select {
	case <-b.release:
		return Observation{Text: "ok:" + call.Function.Name}
	case <-time.After(3 * time.Second):
		return Observation{Text: "BARRIER TIMEOUT: calls did not overlap", Failed: true}
	}
}

func (b *barrierTools) ParallelSafe(call llm.ToolCall) bool { return b.safe[call.Function.Name] }

func TestReadOnlyBatchRunsConcurrently(t *testing.T) {
	ex := newBarrierTools(2, map[string]bool{"read_a": true, "read_b": true})
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("1", "read_a", `{}`), toolCall("2", "read_b", `{}`)),
		textResp("done"),
	}}
	loop := New(client, ex, &fakeDefs{}, nil, nil, Config{MaxIterations: 8})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	final := client.seen[len(client.seen)-1]
	var results []string
	for _, m := range final {
		if m.Role == "tool" {
			results = append(results, m.Content)
		}
	}
	if len(results) != 2 || !strings.HasPrefix(results[0], "ok:read_a") || !strings.HasPrefix(results[1], "ok:read_b") {
		t.Fatalf("results out of order or missing: %v", results)
	}
}

// .
type serialTools struct {
	mu       sync.Mutex
	inflight int
	max      int
	safe     map[string]bool
	order    []string
}

func (s *serialTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	s.mu.Lock()
	s.order = append(s.order, call.Function.Name)
	s.inflight++
	if s.inflight > s.max {
		s.max = s.inflight
	}
	s.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	s.mu.Lock()
	s.inflight--
	s.mu.Unlock()
	return Observation{Text: "ok"}
}

func (s *serialTools) ParallelSafe(call llm.ToolCall) bool { return s.safe[call.Function.Name] }

// .
// .
func TestUnsafeCallsNeverOverlap(t *testing.T) {
	ex := &serialTools{safe: map[string]bool{}}
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("1", "write_a", `{}`), toolCall("2", "write_b", `{}`)),
		textResp("done"),
	}}
	loop := New(client, ex, &fakeDefs{}, nil, nil, Config{MaxIterations: 8})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if ex.max != 1 {
		t.Fatalf("unsafe calls overlapped: max in-flight %d", ex.max)
	}
}

// .
// .
// .
func TestPlainExecutorStaysSerial(t *testing.T) {
	ft := &fakeTools{results: map[string]string{"read": "ok"}}
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("1", "read", `{}`), toolCall("2", "read", `{}`)),
		textResp("done"),
	}}
	loop := New(client, ft, &fakeDefs{}, nil, nil, Config{MaxIterations: 3})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(ft.calls) != 2 {
		t.Fatalf("calls = %v", ft.calls)
	}
}

// .
// .
// .
// .
// .
// .
func TestMixedBatchExecutesSeriallyInArrivalOrder(t *testing.T) {
	ex := &serialTools{safe: map[string]bool{"read_b": true, "read_c": true}}
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("1", "write_a", `{}`), toolCall("2", "read_b", `{}`), toolCall("3", "read_c", `{}`)),
		textResp("done"),
	}}
	loop := New(client, ex, &fakeDefs{}, nil, nil, Config{MaxIterations: 8})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if ex.max != 1 {
		t.Fatalf("mixed batch overlapped: max in-flight %d", ex.max)
	}
	want := []string{"write_a", "read_b", "read_c"}
	if len(ex.order) != 3 || ex.order[0] != want[0] || ex.order[1] != want[1] || ex.order[2] != want[2] {
		t.Fatalf("execution order %v, want %v — a read ran against the pre-write world", ex.order, want)
	}
}
