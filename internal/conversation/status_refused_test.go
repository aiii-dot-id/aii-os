package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// REFUSALS BEFORE RUNNING ARE NAMED IN THE STATUS LINE AND COUNTED FOR
// THE SUMMARY: a call to a tool the request does not
// offer is refused before it runs; the line names it among the failed,
// and the result carries the refused count beside the calls done.
func TestTheStatusLineNamesTheCallsRefusedBeforeRunning(t *testing.T) {
	client := &seeingLLM{scriptLLM: scriptLLM{script: []llm.Response{
		resp("", toolCall("c1", "note", `{}`)),
		resp("", toolCall("c2", "nope", `{}`)),
		textResp("done"),
	}}}
	tools := &fakeTools{results: map[string]string{"note": "ok", "nope": "Error: nope is not offered"}, refused: map[string]bool{"nope": true}}
	loop := New(client, tools, &fakeDefs{}, nil, nil, testBounds(Config{
		TurnTokenBudget: limits.Of(60000),
		TurnTime:        300 * time.Second,
	}))
	res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.CallsDone != 2 || res.RefusedCalls != 1 {
		t.Fatalf("done %d refused %d, want 2 and 1: status %q", res.CallsDone, res.RefusedCalls, res.StatusLine)
	}
	if !strings.HasPrefix(res.StatusLine, "[status · calls 2: 1 ok, 1 failed, 1 refused before running · undeclared") {
		t.Fatalf("the status line: %q", res.StatusLine)
	}
}
