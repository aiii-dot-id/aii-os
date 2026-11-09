package conversation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// windowProbe answers a call with a result of exactly the window its
// context says, as a tool that pages its answer from the top does.
type windowProbe struct{ seen []int }

func (p *windowProbe) Execute(ctx context.Context, _ llm.ToolCall) Observation {
	n := limits.ToolResultChars(ctx)
	p.seen = append(p.seen, n)
	return Observation{Text: "HEAD" + strings.Repeat("x", n-4)}
}

// A CALL IS TOLD THE WINDOW ITS RESULT IS SHOWN IN (row 46). The loop cuts
// a result longer than prompt.max_tool_result_chars and keeps its tail, so
// a tool that pages from the top (web_fetch) must size its page to the
// window in force for the turn: the loop hands it on the call's context,
// and a result that fills it exactly reaches the model whole, head first.
// The window is the allowance less the room of the status line that ends
// every result (2026-11-06), reserved before the call runs.
func TestEachToolCallIsToldTheWindowItsResultIsShownIn(t *testing.T) {
	probe := &windowProbe{}
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("1", "pager", "{}")), textResp("done")}}
	loop := New(client, probe, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 180}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	if len(probe.seen) != 1 || probe.seen[0] >= 180 || probe.seen[0] < 180-120 {
		t.Fatalf("the call was told %v, want the turn's 180 less the status line's room", probe.seen)
	}
	var shown string
	for _, m := range client.requests[len(client.requests)-1] {
		if m.Role == "tool" {
			shown = m.Content
		}
	}
	if !strings.HasPrefix(shown, "HEAD") || strings.Contains(shown, "truncated") {
		t.Fatalf("a result that fills its window is shown whole: %.60q", shown)
	}
	if limits.ToolResultChars(context.Background()) != limits.DefaultToolResultChars {
		t.Fatal("outside a turn a call has the default window")
	}
}

// cadenceTools is a pair of tools: "cadence" sets the identity's
// reflection cadence (as work update reflect_every= does), "pager" answers
// with a result of exactly the window its context says. ParallelSafe makes
// a batch of pagers run in parallel, as read-only tools do.
type cadenceTools struct {
	mu       sync.Mutex // the parallel case runs two pagers at once
	cadence  int
	sets     int // the cadence the "cadence" call sets (1 when unset)
	seen     []int
	parallel bool
	// slow: a pager answers after this long, so a batch's other call is
	// counted before it (the refusal pair).
	slow time.Duration
}

func (c *cadenceTools) reflectEvery(context.Context) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cadence
}

func (c *cadenceTools) windows() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.seen...)
}

func (c *cadenceTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch call.Function.Name {
	case "cadence":
		c.cadence = max(c.sets, 1)
		return Observation{Text: "Work session updated."}
	case "refuse":
		return Observation{Text: "Error: refused before running", Failed: true, Refused: true}
	default:
		if c.slow > 0 {
			c.mu.Unlock()
			time.Sleep(c.slow)
			c.mu.Lock()
		}
		n := limits.ToolResultChars(ctx)
		c.seen = append(c.seen, n)
		if n < 4 {
			return Observation{Text: strings.Repeat("x", n)}
		}
		return Observation{Text: "HEAD" + strings.Repeat("x", n-4)}
	}
}

func (c *cadenceTools) ParallelSafe(call llm.ToolCall) bool {
	return c.parallel && (call.Function.Name == "pager" || call.Function.Name == "refuse")
}

func pagerResults(t *testing.T, msgs []llm.Message) []string {
	t.Helper()
	var out []string
	for _, m := range msgs {
		if m.Role == "tool" && (strings.HasPrefix(m.Content, "HEAD") || strings.Contains(m.Content, "truncated")) {
			out = append(out, m.Content)
		}
	}
	return out
}

// THE WINDOW IS SET PER CALL (a review, 2026-11-06): a call that sets the
// reflection cadence is followed, in the same serial batch, by a pager
// whose window already reserves the reflection line, so a result sized to
// its window is shown whole; a parallel batch of pagers, all read-only,
// gets the same treatment; and a reserve that uses a small allowance up
// tells the call zero room, which stays zero.
func TestTheWindowIsSetPerCallAsTheCadenceStands(t *testing.T) {
	t.Run("a cadence set mid-batch", func(t *testing.T) {
		tools := &cadenceTools{}
		client := &scriptLLM{script: []llm.Response{
			resp("", toolCall("c1", "cadence", `{"action":"update","reflect_every":1}`), toolCall("c2", "pager", "{}")),
			textResp("done"),
		}}
		loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 400, ReflectEvery: tools.reflectEvery}))
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
			t.Fatal(err)
		}
		shown := pagerResults(t, client.requests[len(client.requests)-1])
		if len(shown) != 1 || !strings.HasPrefix(shown[0], "HEAD") || strings.Contains(shown[0], "truncated") || !strings.Contains(shown[0], "Reflect (METHOD.md") {
			t.Fatalf("the pager after the cadence change was told %v and shown: %.80q", tools.windows(), shown)
		}
	})
	t.Run("a cadence of two set by the first call of a serial batch", func(t *testing.T) {
		// The pager's result is the round's second completion: the
		// reflection line falls on it, and its window must say so
		// (a review, 2026-11-06: the serial walk counted its predecessors
		// twice and predicted completion three).
		tools := &cadenceTools{sets: 2}
		client := &scriptLLM{script: []llm.Response{
			resp("", toolCall("c1", "cadence", `{"action":"update","reflect_every":2}`), toolCall("c2", "pager", "{}")),
			textResp("done"),
		}}
		loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 400, ReflectEvery: tools.reflectEvery}))
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
			t.Fatal(err)
		}
		shown := pagerResults(t, client.requests[len(client.requests)-1])
		if len(shown) != 1 || !strings.HasPrefix(shown[0], "HEAD") || strings.Contains(shown[0], "truncated") || !strings.Contains(shown[0], "Reflect (METHOD.md") {
			t.Fatalf("the pager at a cadence of two was told %v and shown: %.80q", tools.windows(), shown)
		}
	})
	t.Run("a parallel batch of pagers", func(t *testing.T) {
		tools := &cadenceTools{parallel: true}
		client := &scriptLLM{script: []llm.Response{
			resp("", toolCall("p1", "pager", "{}"), toolCall("p2", "pager", "{}")),
			textResp("done"),
		}}
		loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 400}))
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
			t.Fatal(err)
		}
		shown, told := pagerResults(t, client.requests[len(client.requests)-1]), tools.windows()
		if len(shown) != 2 || len(told) != 2 {
			t.Fatalf("results %d, windows %v", len(shown), told)
		}
		for i, s := range shown {
			if !strings.HasPrefix(s, "HEAD") || strings.Contains(s, "truncated") {
				t.Fatalf("pager %d (told %d) was cut: %.80q", i+1, told[i], s)
			}
		}
	})
	t.Run("a reserve that uses the allowance up", func(t *testing.T) {
		tools := &cadenceTools{}
		client := &scriptLLM{script: []llm.Response{resp("", toolCall("p1", "pager", "{}")), textResp("done")}}
		loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 60}))
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
			t.Fatal(err)
		}
		if told := tools.windows(); len(told) != 1 || told[0] != 0 {
			t.Fatalf("a call with no room left was told %v, want 0 (not the default %d)", told, limits.DefaultToolResultChars)
		}
	})
}

// THE WINDOW TOLD ALLOWS FOR A REFUSAL'S PART (a review, 2026-11-07; the
// host): a parallel pair is told its windows before any outcome; the
// first call is refused before it runs and the status line grows by the
// refusal's part; the second, a pager that fills exactly the window it
// was told, is still shown whole, head first.
func TestAParallelPagerBesideARefusalFillsItsWindowWhole(t *testing.T) {
	tools := &cadenceTools{parallel: true, slow: 100 * time.Millisecond}
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("r1", "refuse", "{}"), toolCall("p2", "pager", "{}")),
		textResp("done"),
	}}
	loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 400}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RefusedCalls != 1 || !strings.Contains(res.StatusLine, "1 refused before running") {
		t.Fatalf("the refusal is not counted: %d, %q", res.RefusedCalls, res.StatusLine)
	}
	shown, told := pagerResults(t, client.requests[len(client.requests)-1]), tools.windows()
	if len(shown) != 1 || len(told) != 1 {
		t.Fatalf("results %d, windows %v", len(shown), told)
	}
	if !strings.HasPrefix(shown[0], "HEAD") || strings.Contains(shown[0], "truncated") || len([]rune(shown[0])) < told[0] {
		t.Fatalf("the pager (told %d) was cut beside the refusal: %.80q", told[0], shown[0])
	}
}
