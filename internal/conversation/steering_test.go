package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// Steering: what the operator said after the turn began has to reach the
// model inside the turn, and it has to reach it in a shape the endpoints
// have actually been asked about.
//
// These assert on scriptLLM.requests — what the MODEL SAW — rather than
// on the loop's internals, because the wire shape is the whole property.

// scriptSteering hands over its words once, the way a real drain does.
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

// trailingUserMessages counts the consecutive user messages at the end of
// a request. This is the assertion that matters: user(text) after
// user(tool_result) is proven accepted (internal/app/toollimit_live_test.go),
// user(text) after user(text) is not, and the loop must never build the
// second. A count of 1 is the contract; 2 is the untested shape.
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

// The negative control: same script, no steerer. If this ever shows the
// frame, the test above is passing on something the loop always does.
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

// The shape this design exists to protect. On the last iteration the loop
// already owed the model a message ("you've reached the tool call limit")
// and the operator may have spoken in the same gap. Appended in turn they
// would be user(text) after user(text) — the shape no endpoint has been
// asked about. They must arrive as ONE message.
func TestSteeringAndTheToolLimitShareOneMessage(t *testing.T) {
	client := &scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)), // iteration 0 is also the last
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
	if !strings.Contains(last.Content, "tool call limit") {
		t.Fatalf("the loop stopped owing the model its limit notice: %q", last.Content)
	}
}

// Multiple rounds: the operator speaks once, and the words must not be
// re-delivered at every later boundary. A steer said twice is the
// operator having said it twice, which they did not.
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
	// The steer stays in the message list once appended, so it appears in
	// every LATER request — that is history, and history is the point.
	// What must not happen is a second APPEND. So the count is taken
	// within the final list, which carries the whole turn.
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
