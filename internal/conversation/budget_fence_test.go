package conversation

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type optsLLM struct {
	script []llm.Response
	i      int
	opts   []llm.ChatOptions
	seen   [][]llm.Message
}

func (c *optsLLM) Chat(_ context.Context, msgs []llm.Message, o llm.ChatOptions) (*llm.Response, error) {
	c.seen = append(c.seen, append([]llm.Message(nil), msgs...))
	c.opts = append(c.opts, o)
	r := c.script[c.i%len(c.script)]
	c.i++
	return &r, nil
}

type oneDefs struct{}

func (oneDefs) ToolDefinitions(Checkpoint) []llm.ToolDefinition {
	return []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "bash"}}}
}

func usedResp(total int, calls ...llm.ToolCall) llm.Response {
	r := resp("", calls...)
	r.Usage = llm.Usage{Reported: true, PromptTokens: total, TotalTokens: total}
	return r
}

func TestTurnTokenBudgetFencesTheTurn(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(300_000, toolCall("a", "bash", `{}`)),
		usedResp(300_000, toolCall("b", "bash", `{}`)),
		textResp("wrapped up what I had"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(500_000)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.opts) != 3 {
		t.Fatalf("made %d calls, want 2 working + 1 bounded wrap-up", len(client.opts))
	}
	if len(client.opts[0].Tools) == 0 {
		t.Fatal("working calls must offer tools (harness self-check)")
	}
	if !client.opts[2].DisableTools {
		t.Fatal("the wrap-up call did not disable tool invocation")
	}
	if !strings.Contains(res.Spoken, "token budget (500000) is spent") {
		t.Fatalf("the turn did not declare its fence: %q", res.Spoken)
	}
}

func TestTheTokenFenceAsksForTheContinuation(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(300_000, toolCall("a", "bash", `{}`)),
		usedResp(300_000, toolCall("b", "bash", `{}`)),
		textResp("wrapped up what I had"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(500_000)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.ContinuedAtTokenBudget {
		t.Fatalf("a turn ended by its token fence did not ask for the continuation: %+v", res)
	}
	if res.ContinuedAtCap || res.ContinuedAtPressure || res.ExhaustedBudget || res.ExhaustedCallBudget {
		t.Fatalf("the fence's ending was reported as another ending: %+v", res)
	}
	if !strings.Contains(res.Spoken, "while its work session is still active, the work continues automatically in a fresh turn") {
		t.Fatalf("the fence promises more than the caller does: %q", res.Spoken)
	}
	// When the caller schedules no fresh turn the reply closes on the one
	// line that says so, in the fence's line's place.
	const why = "no work session is active"
	if got, want := res.NothingContinues(why), "wrapped up what I had\n\n"+nothingContinuesLine(res.ToolCallsUsed, why); got != want {
		t.Fatalf("the reply of a fenced turn nothing continues = %q, want %q", got, want)
	}
}

// A provider that reports no usage still draws against the fence, by
// estimate: unknown spend is never free.
func TestSilentUsageCountsAgainstTheFence(t *testing.T) {
	big := resp(strings.Repeat("y", 40_000), toolCall("a", "bash", `{}`))
	// Usage deliberately unreported.
	client := &optsLLM{script: []llm.Response{big, textResp("done what I could")}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(1_000)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.opts) != 2 {
		t.Fatalf("made %d calls, want 1 silent working call + 1 wrap-up", len(client.opts))
	}
	if !strings.Contains(res.Spoken, "is spent") {
		t.Fatalf("silent spend did not trip the fence: %q", res.Spoken)
	}
}

func TestTheFenceSaysAboutWhenACallDidNotReportItsSpend(t *testing.T) {
	reported := func(total int, text string) llm.Response {
		r := textResp(text)
		r.Usage = llm.Usage{Reported: true, PromptTokens: total, TotalTokens: total}
		return r
	}
	const closes = " The answer above ends the turn; while its work session is still active, the work continues automatically in a fresh turn.]"
	for _, c := range []struct {
		name   string
		script []llm.Response
		fence  int
		spoken string // the reply, with <n> where an estimate's digits stand
		logged string
	}{
		{name: "every call reported",
			script: []llm.Response{usedResp(300_000, toolCall("a", "bash", `{}`)), usedResp(300_000, toolCall("b", "bash", `{}`)), reported(1_000, "wrapped up")},
			fence:  500_000,
			spoken: "wrapped up\n\n[This turn's token budget (500000) is spent: 601000 tokens over 3 calls." + closes,
			logged: "TURN BUDGET: fence at 500000 tokens — 601000 spent over 3 call(s); turn ended with a bounded wrap-up"},
		{name: "a working call reported nothing",
			script: []llm.Response{resp(strings.Repeat("y", 40_000), toolCall("a", "bash", `{}`)), reported(1_000, "wrapped up")},
			fence:  1_000,
			spoken: strings.Repeat("y", 40_000) + "\n\nwrapped up\n\n[This turn's token budget (1000) is spent: about <n> tokens over 2 calls." + closes,
			logged: "TURN BUDGET: fence at 1000 tokens — about <n> spent over 2 call(s), 1 of them unreported; turn ended with a bounded wrap-up"},
		{name: "the wrap-up reported nothing",
			script: []llm.Response{usedResp(300_000, toolCall("a", "bash", `{}`)), usedResp(300_000, toolCall("b", "bash", `{}`)), textResp("wrapped up")},
			fence:  500_000,
			spoken: "wrapped up\n\n[This turn's token budget (500000) is spent: about 600000 tokens over 3 calls." + closes,
			logged: "TURN BUDGET: fence at 500000 tokens — about 600000 spent over 3 call(s), 1 of them unreported; turn ended with a bounded wrap-up"},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := logsink.CaptureForTest(t)
			loop := New(&optsLLM{script: c.script}, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
				testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(c.fence)}))
			res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err != nil {
				t.Fatal(err)
			}
			// <n> is the estimate's own digits: more than the fence, or the
			// turn would not have ended there.
			digits := regexp.MustCompile(`about (\d+) `).FindStringSubmatch(res.Spoken)
			n := ""
			if digits != nil {
				n = digits[1]
			}
			if want := strings.ReplaceAll(c.spoken, "<n>", n); res.Spoken != want {
				t.Errorf("the reply = %q, want %q", res.Spoken, want)
			}
			if want := strings.ReplaceAll(c.logged, "<n>", n); !logs.Contains(want) {
				t.Errorf("the log does not hold %q:\n%s", want, logs.String())
			}
			if got, _ := strconv.Atoi(n); strings.Contains(c.spoken, "<n>") && got < c.fence {
				t.Errorf("the estimate said is %q, under the fence of %d it ended the turn at", n, c.fence)
			}
		})
	}
}

// Under budget, nothing changes: no declaration, all calls offered tools.
func TestUnderBudgetTurnsAreUntouched(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		usedResp(1_000, toolCall("a", "bash", `{}`)),
		usedResp(1_000, toolCall("b", "bash", `{}`)),
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(600_000)}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Spoken, "token budget") {
		t.Fatalf("an under-budget turn declared a fence: %q", res.Spoken)
	}
}
