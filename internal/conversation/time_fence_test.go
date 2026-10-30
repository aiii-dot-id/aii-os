package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// slowLLM is a model each of whose calls takes step on the loop's clock.
type slowLLM struct {
	optsLLM
	now  *time.Time
	step time.Duration
}

func (c *slowLLM) Chat(ctx context.Context, msgs []llm.Message, o llm.ChatOptions) (*llm.Response, error) {
	*c.now = c.now.Add(c.step)
	r, err := c.optsLLM.Chat(ctx, msgs, o)
	if err == nil && o.DisableTools {
		// Asked without tools, it answers: the turn's last call.
		answer := usedResp(1_000)
		answer.Choices[0].Message.Content = "wrapped up what I had"
		return &answer, nil
	}
	return r, err
}

// timeFencedRun runs one turn of a model that calls a tool on every call,
// spending few tokens, each call taking twenty minutes, under a fence of
// fence (zero: none) and a round ceiling of six.
func timeFencedRun(t *testing.T, fence time.Duration) (*slowLLM, Result) {
	t.Helper()
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	client := &slowLLM{optsLLM: optsLLM{script: []llm.Response{usedResp(1_000, toolCall("a", "bash", `{}`))}}, now: &now, step: 20 * time.Minute}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, oneDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(6), TurnTokenBudget: limits.Unlimited, TurnTime: fence}))
	loop.now = func() time.Time { return now }
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	return client, res
}

func TestATurnFencedByTimeWrapsUpAtItsTime(t *testing.T) {
	client, res := timeFencedRun(t, 45*time.Minute)
	// Calls end at 20, 40 and 60 minutes: the fence is checked before each
	// round, so the third call (begun at 40) is the last working one and
	// the fourth is the wrap-up.
	if len(client.opts) != 4 {
		t.Fatalf("made %d calls, want 3 working calls and 1 bounded wrap-up at the 45-minute fence", len(client.opts))
	}
	if len(client.opts[0].Tools) == 0 {
		t.Fatal("working calls must offer tools (harness self-check)")
	}
	if !client.opts[3].DisableTools {
		t.Fatal("the wrap-up call at the time fence did not withdraw the tools")
	}
	last := client.seen[3]
	if got := last[len(last)-1].Content; !strings.Contains(got, timePressureNote) {
		t.Fatalf("the wrap-up call was not told that the turn's time is spent: %q", got)
	}
	if !res.ContinuedAtTimeFence || res.ContinuedAtTokenBudget || res.ExhaustedBudget || res.ContinuedAtCap || res.ContinuedAtPressure {
		t.Fatalf("a turn ended by its time fence did not ask for the continuation as that ending: %+v", res)
	}
	if !strings.Contains(res.Said(), "This turn's time (45m0s) is spent: 4000 tokens over 4 calls.") {
		t.Fatalf("the turn did not declare its time fence: %q", res.Said())
	}
}

// The control half: the same turn with no time fence runs to its round
// ceiling, so what ended the fenced turn was its time.
func TestATurnWithNoTimeFenceRunsPastThatTime(t *testing.T) {
	client, res := timeFencedRun(t, 0)
	if res.ContinuedAtTimeFence {
		t.Fatalf("a turn with no time fence was ended by one: %+v", res)
	}
	if len(client.opts) < 6 {
		t.Fatalf("made %d calls; with no time fence the turn runs to its six rounds", len(client.opts))
	}
}
