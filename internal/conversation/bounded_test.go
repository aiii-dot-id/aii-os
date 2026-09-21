package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestATurnIsBoundedPerToolCall(t *testing.T) {
	cap := logsink.CaptureForTest(t)

	const calls = 6
	script := make([]llm.Response, 0, calls+1)
	for i := 0; i < calls; i++ {
		// .
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
	loop := New(&scriptLLM{script: script}, tools, &fakeDefs{}, nil, nil, Config{})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}

	lines := cap.Lines()
	if len(lines) == 0 {
		t.Fatal("the turn wrote nothing at all — this test would pass while checking nothing")
	}
	// .
	if bound := 3*calls + 3; len(lines) > bound {
		t.Fatalf("a %d-call turn wrote %d lines, over the %d bound:\n%s",
			calls, len(lines), bound, strings.Join(lines, "\n"))
	}

	// .
	// .
	// .
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
