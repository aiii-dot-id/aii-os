package conversation

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// seeingLLM is a scripted client that keeps every request's messages, so a
// test can read what the model was shown after a tool result.
type seeingLLM struct {
	scriptLLM
	seen [][]llm.Message
}

func (s *seeingLLM) Chat(ctx context.Context, msgs []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	s.seen = append(s.seen, append([]llm.Message(nil), msgs...))
	return s.scriptLLM.Chat(ctx, msgs, opts)
}

func toolMessages(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func lastToolMessage(t *testing.T, msgs []llm.Message) string {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "tool" {
			return msgs[i].Content
		}
	}
	t.Fatal("no tool message in the request")
	return ""
}

// THE IDENTITY IS INFORMED AT EVERY RESULT: each tool
// result ends with one status line, fixed in shape: the calls made and how
// they ended, the declared steps against those spent, the tokens against
// the turn's fence, the time against its wall; and, at the identity's own
// cadence, the ask to step back (METHOD.md, step 3).
func TestEveryToolResultCarriesTheStatusLine(t *testing.T) {
	client := &seeingLLM{scriptLLM: scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		resp("", toolCall("c2", "note", `{}`)),
		textResp("done"),
	}}}
	tools := &fakeTools{results: map[string]string{"note": "ok"}}
	declared := 0
	loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{
		TurnTokenBudget:   limits.Of(60000),
		TurnTime:          300 * time.Second,
		PredictedThisTurn: func() (int, int) { return declared, 0 },
		ReflectEvery:      func(context.Context) int { return 2 },
	}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.seen) != 3 {
		t.Fatalf("requests: %d", len(client.seen))
	}
	// The turn's result carries the last status line, for the log's summary.
	if !strings.HasPrefix(res.StatusLine, "[status · calls 2: 2 ok, 0 failed · undeclared") {
		t.Fatalf("the result's status line: %q", res.StatusLine)
	}
	first := lastToolMessage(t, client.seen[1])
	if !strings.HasPrefix(first, "ok\n[status · calls 1: 1 ok, 0 failed · undeclared · tokens ") || !strings.Contains(first, " of 60000 · ") || !strings.HasSuffix(first, " of 5m0s]") {
		t.Fatalf("the first result's status line: %q", first)
	}
	if strings.Contains(first, "Reflect (METHOD.md") {
		t.Fatalf("a reflection asked before the cadence: %q", first)
	}
	second := lastToolMessage(t, client.seen[2])
	if !strings.Contains(second, "[status · calls 2: 2 ok, 0 failed · undeclared") || !strings.HasSuffix(second, "Reflect (METHOD.md, step 3): one line on the pattern of the last 2 steps, before the next call.") {
		t.Fatalf("the second result's status line, at the cadence: %q", second)
	}
	// A declaration is read against the calls spent since it; a failure
	// counts as one.
	client = &seeingLLM{scriptLLM: scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		resp("", toolCall("c2", "fail", `{}`)),
		textResp("done"),
	}}}
	declared = 3
	tools = &fakeTools{results: map[string]string{"note": "ok", "fail": "Error: no"}}
	tools.failing = map[string]bool{"fail": true}
	loop = New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{PredictedThisTurn: func() (int, int) { return declared, 0 }}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	second = lastToolMessage(t, client.seen[2])
	if !strings.Contains(second, "[status · calls 2: 1 ok, 1 failed · declared 3, 2 spent since, 1 remain · tokens ") {
		t.Fatalf("the declared line: %q", second)
	}
}

// SPENT SINCE A DECLARATION COUNTS COMPLETED CALLS (a review, 2026-11-06,
// finding 6): a batch that carried the declaration mid-way shows no
// steps spent until a call after the declaring one completes.
func TestStepsSpentAreTheCallsCompletedAfterTheDeclaration(t *testing.T) {
	client := &seeingLLM{scriptLLM: scriptLLM{script: []llm.Response{
		resp("", toolCall("a", "note", `{}`), toolCall("d", "note", `{}`), toolCall("c", "note", `{}`)),
		resp("", toolCall("e", "note", `{}`)),
		textResp("done"),
	}}}
	tools := &fakeTools{results: map[string]string{"note": "ok"}}
	// Declared at ordinal 2: the batch's second call.
	loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{PredictedThisTurn: func() (int, int) { return 3, 2 }}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	batch := toolMessages(client.seen[1])
	if len(batch) != 3 {
		t.Fatalf("tool messages after the batch: %d", len(batch))
	}
	for i, want := range []string{"declared 3, 0 spent since, 3 remain", "declared 3, 0 spent since, 3 remain", "declared 3, 1 spent since, 2 remain"} {
		if !strings.Contains(batch[i], want) {
			t.Fatalf("result %d of the batch: %q, want %q", i+1, batch[i], want)
		}
	}
	if serial := lastToolMessage(t, client.seen[2]); !strings.Contains(serial, "declared 3, 2 spent since, 1 remain") {
		t.Fatalf("the serial call after the batch: %q", serial)
	}
}

// THE STATUS LINE'S SIZE IS RESERVED in the result's allowance, as the
// host's note is: a result at the window is cut to make room for it.
func TestTheStatusLineIsReservedInTheResultsAllowance(t *testing.T) {
	client := &seeingLLM{scriptLLM: scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		textResp("done"),
	}}}
	tools := &fakeTools{results: map[string]string{"note": strings.Repeat("x", 1000)}}
	loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{MaxToolResultChars: 300}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	msg := lastToolMessage(t, client.seen[1])
	body, status, ok := strings.Cut(msg, "\n[status · ")
	if !ok {
		t.Fatalf("no status line: %q", msg)
	}
	line := utf8.RuneCountInString("[status · " + status)
	_, tail, _ := strings.Cut(body, "]\n") // after the truncation note
	if kept := utf8.RuneCountInString(tail); kept > 300-line-1 {
		t.Fatalf("%d result characters kept beside a status line of %d in a window of 300: %q", kept, line, msg)
	}
}
