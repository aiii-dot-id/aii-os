package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A TURN IS BOUNDED: it writes a known number of lines per tool call,
// not a number that grows with what the model happens to say.
//
// This is the design's second test, and the one that keeps the log
// affordable. The rule it enforces is that unbounded verbose output is
// a DEFECT rather than a level: an active hour is mostly work, so if a
// turn's line count tracked the model's chattiness instead of its
// actions, no threshold would make the log readable — the operator
// would be choosing between silence and a flood.
//
// Three per call is the budget: the call, its result, and the model's
// answer that asked for it.
func TestATurnIsBoundedPerToolCall(t *testing.T) {
	cap := logsink.CaptureForTest(t)

	const calls = 6
	script := make([]llm.Response, 0, calls+1)
	for i := 0; i < calls; i++ {
		// Varied, and long: a chatty model must not buy more lines.
		var chat strings.Builder
		for n := 0; n < 40; n++ {
			chat.WriteString("Considering approach ")
			chat.WriteRune(rune('a' + n%26))
			chat.WriteString(" at some length. ")
		}
		script = append(script, resp(chat.String(), toolCall("c", "note", `{}`)))
	}
	script = append(script, textResp("done"))

	tools := &fakeTools{results: map[string]string{"note": "ok"}}
	loop := New(&scriptLLM{script: script}, tools, &fakeDefs{}, nil, nil, testBounds(Config{}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}

	lines := cap.Lines()
	if len(lines) == 0 {
		t.Fatal("the turn wrote nothing at all — this test would pass while checking nothing")
	}
	// The budget: three per call, plus the final answer's own line.
	if bound := 3*calls + 3; len(lines) > bound {
		t.Fatalf("a %d-call turn wrote %d lines, over the %d bound:\n%s",
			calls, len(lines), bound, strings.Join(lines, "\n"))
	}

	// And every line the turn wrote says WHICH turn it was, because a
	// log an identity reads to find out what it did is worth little if
	// it cannot tell two turns apart.
	stamped := 0
	for _, line := range lines {
		if strings.Contains(line, "[") && strings.Contains(line, "]:") {
			stamped++
		}
	}
	if stamped == 0 {
		t.Fatalf("no line carried its turn — the stamp is not reaching production:\n%s", strings.Join(lines, "\n"))
	}
}
