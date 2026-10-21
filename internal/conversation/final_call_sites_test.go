package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// final_call_sites_test.go — the turn's three tool-less closing calls:
// the token fence, the context wall, and the round boundary (the round
// limit, the declared-budget checkpoint, a yield). Each fits the request
// without tools, makes the call, counts it, keeps its reasoning and reads
// its text; each says in its own words which of them failed. These pin
// what a closing call that goes wrong leaves behind, at every site.

// closingSite builds a turn that reaches one site after a first round
// that said "looking" and ran one tool, with closing as the reply to the
// closing call (none: the call itself fails).
type closingSite struct {
	name   string
	label  string // the error's words for a failed call
	circ   string // the error's words for an unusable reply
	system string
	defs   ToolDefiner
	cfg    Config
	first  llm.Response
	rounds int // the rounds used when the site is reached
}

func closingSites() []closingSite {
	fenced := usedResp(600_000, toolCall("a", "bash", `{}`))
	fenced.Choices[0].Message.Content = "looking"
	wide := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "bash", Description: strings.Repeat("d", 900), Parameters: map[string]any{"type": "object"}}}}}
	return []closingSite{
		{name: "token fence", label: "LLM final call under budget pressure: ", circ: "under budget pressure",
			system: "system", defs: oneDefs{}, cfg: testBounds(Config{MaxIterations: limits.Of(10), TurnTokenBudget: limits.Of(500_000)}), first: fenced, rounds: 2},
		{name: "context wall", label: "LLM final call under context pressure: ", circ: "under context pressure",
			system: strings.Repeat("s", 670-len(showNote)) + systemAdditions(), defs: wide, cfg: testBounds(Config{ContextBudgetTokens: 1000, MaxIterations: limits.Of(6)}),
			first: resp("looking", toolCall("a", "bash", `{}`)), rounds: 2},
		{name: "round limit", label: "LLM final call: ", circ: "at the round limit",
			system: "system", defs: oneDefs{}, cfg: testBounds(Config{MaxIterations: limits.Of(1)}), first: resp("looking", toolCall("a", "bash", `{}`)), rounds: 1},
	}
}

func (s closingSite) run(t *testing.T, closing *llm.Response, steer Steering) (*scriptLLM, Result, error) {
	t.Helper()
	first := s.first
	first.ModelID = "model-a"
	if !first.Usage.Reported {
		first.Usage = llm.Usage{Reported: true, PromptTokens: 10, TotalTokens: 10}
	}
	script := []llm.Response{first}
	if closing != nil {
		script = append(script, *closing)
	}
	client := &scriptLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": "ok"}}, s.defs, nil, nil, s.cfg)
	if steer != nil {
		loop.SetSteering(steer)
	}
	res, err := loop.Run(context.Background(), s.system, []llm.Message{{Role: "user", Content: "go"}})
	return client, res, err
}

// A closing call that fails is counted as a call that happened, names its
// site, and leaves the words said before it standing, marked interrupted.
func TestAFailedClosingCallIsCountedAndNamedAtEverySite(t *testing.T) {
	for _, s := range closingSites() {
		t.Run(s.name, func(t *testing.T) {
			client, res, err := s.run(t, nil, nil)
			if err == nil || !strings.HasPrefix(err.Error(), s.label+"script exhausted") {
				t.Fatalf("the failed closing call's error = %v, want it to begin %q", err, s.label)
			}
			if len(client.requests) != 2 {
				t.Fatalf("requests = %d, want the round and the closing call", len(client.requests))
			}
			if res.Usage.Calls != 2 || res.Usage.Silent != 1 {
				t.Fatalf("usage = %+v, want 2 calls with the failed one silent", res.Usage)
			}
			if res.Spoken != "looking" || res.Interrupted != "the substrate failed mid-turn" {
				t.Fatalf("spoken %q, interrupted %q", res.Spoken, res.Interrupted)
			}
			if res.ModelID != "model-a" || res.RoundsUsed != s.rounds || res.ToolCallsUsed != 1 {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}

// A closing reply that asks for the withdrawn tools is refused at every
// site — after the call was counted, its reasoning kept, and the turn
// attributed to the model that answered it.
func TestAClosingReplyThatAsksForToolsIsRefusedAtEverySite(t *testing.T) {
	for _, s := range closingSites() {
		t.Run(s.name, func(t *testing.T) {
			closing := resp("one more", toolCall("b", "bash", `{}`))
			closing.ModelID = "model-b"
			closing.Choices[0].Reasoning = "closing thoughts"
			_, res, err := s.run(t, &closing, nil)
			if err == nil || err.Error() != "LLM final response "+s.circ+" requested tools that were disabled" {
				t.Fatalf("err = %v", err)
			}
			if res.ModelID != "model-b" {
				t.Fatalf("model = %q, want the closing call's model-b", res.ModelID)
			}
			if res.Usage.Calls != 2 || res.Usage.Silent != 1 {
				t.Fatalf("usage = %+v, want both calls counted, the closing one silent", res.Usage)
			}
			if len(res.Reasoning) != 1 || res.Reasoning[0].Call != 2 || res.Reasoning[0].Text != "closing thoughts" || res.Reasoning[0].Model != "model-b" {
				t.Fatalf("reasoning = %+v, want the closing call's, as call 2", res.Reasoning)
			}
			if res.Spoken != "looking" {
				t.Fatalf("spoken = %q", res.Spoken)
			}
		})
	}
}

// A closing reply with no text is refused at every site.
func TestAnEmptyClosingReplyIsRefusedAtEverySite(t *testing.T) {
	for _, s := range closingSites() {
		t.Run(s.name, func(t *testing.T) {
			closing := textResp("  ")
			_, res, err := s.run(t, &closing, nil)
			if err == nil || err.Error() != "LLM final response "+s.circ+" has no text" {
				t.Fatalf("err = %v", err)
			}
			if res.Usage.Calls != 2 || res.Spoken != "looking" {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}

func TestAClosingReplyOfCallsAndNoTextIsNotCalledEmptyAtEverySite(t *testing.T) {
	for _, s := range closingSites() {
		t.Run(s.name, func(t *testing.T) {
			closing := resp("", toolCall("b", "bash", `{}`))
			_, res, err := s.run(t, &closing, nil)
			if err == nil || err.Error() != "LLM final response "+s.circ+" requested tools that were disabled" {
				t.Fatalf("err = %v", err)
			}
			if res.Spoken != "looking" || res.Interrupted != "the substrate failed mid-turn" {
				t.Fatalf("A REPLY OF CALLS IS TOLD AS %q (spoken %q)", res.Interrupted, res.Spoken)
			}
			// The control: a closing reply with neither is the empty one,
			// and so is a closing response with no choices at all.
			blank := textResp("  ")
			if _, res, _ := s.run(t, &blank, nil); res.Interrupted != "the model returned an empty reply" {
				t.Fatalf("a closing reply with no text and no call is told as %q", res.Interrupted)
			}
			choiceless := llm.Response{}
			if _, res, err := s.run(t, &choiceless, nil); err == nil || res.Interrupted != "the model returned an empty reply" {
				t.Fatalf("A CLOSING RESPONSE WITH NO CHOICES IS TOLD AS %q (%v)", res.Interrupted, err)
			}
		})
	}
}

// hugeSteer is an operator's mid-turn words too long for any request the
// budget allows: the closing request cannot be fit, so no closing call is
// made, and the words come back with the failure, unanswered.
func hugeSteer() *scriptSteering {
	return &scriptSteering{words: []string{"read this " + strings.Repeat("word ", 3000)}}
}

func TestAClosingRequestThatCannotFitMakesNoCallAtEverySite(t *testing.T) {
	for _, s := range closingSites() {
		t.Run(s.name, func(t *testing.T) {
			if s.cfg.ContextBudgetTokens == 0 {
				// Room for the round, none for the words.
				s.cfg.ContextBudgetTokens = 2000
			}
			closing := textResp("never asked")
			client, res, err := s.run(t, &closing, hugeSteer())
			var limit *llm.ContextLimitError
			if !errors.As(err, &limit) {
				t.Fatalf("err = %v, want the fit's context limit", err)
			}
			if len(client.requests) != 1 || res.Usage.Calls != 1 {
				t.Fatalf("requests %d, calls %d: a request that cannot fit is not sent", len(client.requests), res.Usage.Calls)
			}
			if len(res.Unanswered) != 1 || res.Unanswered[0] != hugeSteer().words[0] {
				t.Fatalf("the unanswered words were lost: %.200q", res.Unanswered)
			}
			if strings.Contains(res.Spoken, "read this") {
				t.Fatalf("the unanswered words rode the identity's reply: %.200q", res.Spoken)
			}
		})
	}
}

// At the round boundary the fit comes before the stop check: a turn
// stopped during its last round whose closing request also cannot fit
// fails on the fit, and makes no closing call either way.
func TestAtTheRoundLimitTheFitFailsBeforeTheStopIsAsked(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	client := &captureLLM{script: []llm.Response{resp("", toolCall("1", "work", `{}`)), textResp("closing")}}
	loop := New(client, &stopper{stop: func() { cancel(StoppedByOperator) }}, oneDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(1), ContextBudgetTokens: 2000}))
	loop.SetSteering(hugeSteer())
	res, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
	var limit *llm.ContextLimitError
	if !errors.As(err, &limit) || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the fit's context limit, not the stop", err)
	}
	if len(client.seen) != 1 || res.Usage.Calls != 1 {
		t.Fatalf("provider calls begun %d, counted %d; want 1", len(client.seen), res.Usage.Calls)
	}
	if res.Interrupted != "stopped by the operator" {
		t.Fatalf("interrupted = %q", res.Interrupted)
	}
}

// The round boundary's one message carries the operator's words first and
// the loop's closing ask after them.
func TestTheBoundaryCarriesTheOperatorBeforeTheLoop(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{resp("", toolCall("c1", "note", `{}`)), textResp("wrapped up")}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(1)}))
	loop.SetSteering(&scriptSteering{words: []string{"answer with what you have"}})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	final := client.requests[len(client.requests)-1]
	last := final[len(final)-1].Content
	if want := steeringNote([]string{"answer with what you have"}) + "\n\nYou've reached this turn's round limit."; !strings.HasPrefix(last, want) {
		t.Fatalf("the boundary message = %q, want it to begin %q", last, want)
	}
}

// A yield on the last round is a yield, not an exhausted run: its closing
// line, its flag, and the answer it carried, in that order after the reply.
func TestAYieldOnTheLastRoundIsAYield(t *testing.T) {
	client := &captureLLM{script: []llm.Response{resp("", toolCall("c1", "work", `{}`)), textResp("standing by")}}
	tools := &answerTools{fakeTools: fakeTools{results: map[string]string{"work": "yielding"}}, answer: "X holds"}
	res, err := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxIterations: limits.Of(1)})).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Yielded || res.ExhaustedBudget || res.ContinuedAtCap {
		t.Fatalf("result = %+v", res)
	}
	want := "standing by\n\nBest current answer: X holds\n\n" + declare("Turn yielded after %d tool calls — the gate is free; a delivery or message wakes the next turn.", 1)
	if res.Spoken != want {
		t.Fatalf("spoken = %q, want %q", res.Spoken, want)
	}
	last := joined(client.seen[1])
	if !strings.Contains(last, "round limit") || strings.Contains(last, "You yielded this turn") {
		t.Fatalf("on the last round the limit's ask is the one sent: %q", last[len(last)-200:])
	}
}
