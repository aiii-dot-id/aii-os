package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .
// .
// .

// .
type scriptSteering struct {
	words  []string
	drains int
}

func (s *scriptSteering) DrainSteering() []string {
	s.drains++
	said := s.words
	s.words = nil
	return said
}

// .
// .
// .
// .
// .
func trailingUserMessages(msgs []llm.Message) int {
	n := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			break
		}
		n++
	}
	return n
}

func TestSteeringReachesTheModelAtTheToolBoundary(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		textResp("done"),
	}}
	steer := &scriptSteering{words: []string{"stop — that file is already fixed"}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, Config{})
	loop.SetSteering(steer)

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	if len(client.requests) < 2 {
		t.Fatalf("expected a second call after the tool round, got %d", len(client.requests))
	}
	second := client.requests[1]
	last := second[len(second)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "already fixed") {
		t.Fatalf("the operator's words did not reach the model; last message = %+v", last)
	}
	if !strings.Contains(last.Content, "while you were working") {
		t.Fatalf("steering arrived unframed — the model cannot tell it came mid-turn: %q", last.Content)
	}
	if n := trailingUserMessages(second); n != 1 {
		t.Fatalf("steering built %d consecutive user messages; only 1 is a proven shape", n)
	}
	if steer.drains != 1 {
		t.Fatalf("drained %d times, want once per boundary", steer.drains)
	}
}

// .
// .
func TestWithoutSteeringTheBoundaryIsUnchanged(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		textResp("done"),
	}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, Config{})

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	second := client.requests[1]
	for _, m := range second {
		if strings.Contains(m.Content, "while you were working") {
			t.Fatalf("a loop with no steerer spoke for an operator who said nothing: %q", m.Content)
		}
	}
	if n := trailingUserMessages(second); n != 0 {
		t.Fatalf("nothing was said and nothing was due, but %d user message(s) were appended", n)
	}
}

// .
// .
// .
// .
// .
func TestSteeringAndTheToolLimitShareOneMessage(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		textResp("wrapped up"),
	}}
	steer := &scriptSteering{words: []string{"answer with what you have"}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil,
		Config{MaxIterations: 1})
	loop.SetSteering(steer)

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	final := client.requests[len(client.requests)-1]
	if n := trailingUserMessages(final); n != 1 {
		t.Fatalf("steering and the tool-limit message arrived as %d user messages, want 1 — "+
			"user(text) after user(text) is the shape nothing has proven", n)
	}
	last := final[len(final)-1]
	if !strings.Contains(last.Content, "answer with what you have") {
		t.Fatalf("the operator's words were lost at the limit boundary: %q", last.Content)
	}
	if !strings.Contains(last.Content, "round limit") {
		t.Fatalf("the loop stopped owing the model its limit notice: %q", last.Content)
	}
}

// .
// .
// .
func TestSteeringIsDeliveredOnce(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		resp("", toolCall("c2", "note", `{}`)),
		textResp("done"),
	}}
	steer := &scriptSteering{words: []string{"only once"}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, Config{})
	loop.SetSteering(steer)

	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	// .
	final := client.requests[len(client.requests)-1]
	framed := 0
	for _, m := range final {
		if strings.Contains(m.Content, "while you were working") {
			framed++
		}
	}
	if framed != 1 {
		t.Fatalf("the operator's one sentence sits in the turn %d times; they said it once", framed)
	}
	if steer.drains != 2 {
		t.Fatalf("drained %d times across 2 tool rounds — the boundary must always ask", steer.drains)
	}
}

// .
// .
// .
// .
// .
func TestDrainedSteeringSurvivesAFailedTurn(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		// .
	}}
	steer := &scriptSteering{words: []string{"actually, use the other file"}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, Config{})
	loop.SetSteering(steer)
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("the turn was expected to fail on its second call")
	}
	if steer.drains == 0 {
		t.Fatal("the steering was never drained — the scenario did not arise")
	}
	if !strings.Contains(res.Spoken, "actually, use the other file") || !strings.Contains(res.Spoken, "failed before") {
		t.Fatalf("drained steering was lost with the failed turn; spoken = %q", res.Spoken)
	}
}

// .
// .
// .
// .
// .
func TestAnsweredSteeringIsNotReplayedByALaterFailure(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		resp("", toolCall("c2", "note", `{}`)),
		// .
	}}
	steer := &scriptSteering{words: []string{"switch to the other branch"}}
	loop := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil, Config{})
	loop.SetSteering(steer)
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("the turn was expected to fail on its third call")
	}
	if strings.Contains(res.Spoken, "switch to the other branch") {
		t.Fatalf("steering the model already answered was replayed as unanswered: %q", res.Spoken)
	}
}
