package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// degen_guard_test.go — the output-side sibling of the repeat-result
// nudge. The fixture is the 2026-08-26 collapse's shape: one line and
// one sentence repeated until the context wall.

func collapseText() string {
	return "The next step is the grep.\n" +
		strings.Repeat("post the tool call now\n", 30) +
		strings.Repeat("I will now write the reply now. ", 40)
}

func TestDetectorFiresOnCollapse(t *testing.T) {
	if !degenerateEmission(collapseText()) {
		t.Fatal("the incident's own shape was not detected")
	}
	if !degenerateEmission(strings.Repeat("I will now write the first token. ", 50)) {
		t.Fatal("single-line sentence cycling was not detected")
	}
}

func TestDetectorStaysQuietOnHonestText(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("line about topic number ")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(" with distinct content\n")
	}
	if degenerateEmission(b.String()) {
		t.Fatal("distinct lines flagged as collapse")
	}
	if degenerateEmission("A short normal answer. It has a few sentences. They differ from each other.") {
		t.Fatal("ordinary prose flagged as collapse")
	}
	code := strings.Repeat("}\n", 40) + strings.Repeat("ok\n", 30)
	if degenerateEmission(code) {
		t.Fatal("short structural lines flagged as collapse")
	}
}

type countTools struct {
	fakeTools
	execs int
}

func (c *countTools) Execute(ctx context.Context, tc llm.ToolCall) string {
	c.execs++
	return c.fakeTools.Execute(ctx, tc)
}

func TestDegenerateEmissionNudgedThenEnded(t *testing.T) {
	client := &optsLLM{script: []llm.Response{
		resp(collapseText(), toolCall("a", "bash", `{}`)),
		resp(collapseText(), toolCall("b", "bash", `{}`)),
		textResp("never reached"),
	}}
	tools := &countTools{fakeTools: fakeTools{results: map[string]string{"bash": "ok"}}}
	loop := New(client, tools, oneDefs{}, nil, nil, Config{MaxIterations: 10})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.seen) != 2 {
		t.Fatalf("made %d calls, want 2 (second detection ends the turn)", len(client.seen))
	}
	if tools.execs != 0 {
		t.Fatalf("a collapsing emission's tool calls ran %d time(s) — they must be discarded", tools.execs)
	}
	second := client.seen[1]
	if !strings.Contains(second[len(second)-1].Content, "degenerated into repetition") {
		t.Fatalf("the corrective note did not reach the second call: %q", second[len(second)-1].Content)
	}
	if !strings.Contains(res.Spoken, "detected in the model's output twice") {
		t.Fatalf("the second strike did not end the turn with a declaration: %q", res.Spoken)
	}
}

func TestDegenerateFinalAnswerIsMarked(t *testing.T) {
	client := &optsLLM{script: []llm.Response{textResp(collapseText())}}
	loop := New(client, &fakeTools{results: map[string]string{}}, oneDefs{}, nil, nil, Config{MaxIterations: 5})
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.seen) != 1 {
		t.Fatalf("a degenerate final answer must not retry: %d calls", len(client.seen))
	}
	if !strings.Contains(res.Spoken, "treat its repeating tail as noise") {
		t.Fatalf("the reader was not warned: %q", res.Spoken)
	}
}
