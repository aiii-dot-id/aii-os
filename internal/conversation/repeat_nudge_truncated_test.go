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
// .
// .
// .
// .
func TestARepeatedTruncatedResultIsNotCalledComplete(t *testing.T) {
	const cap = 400
	huge := "diff: " + strings.Repeat("x", cap*4)

	rounds := 5
	script := make([]llm.Response, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		script = append(script, resp("", toolCall("c", "bash", `{"n":`+strings.Repeat("9", i+1)+`}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	loop := New(client, &fakeTools{results: map[string]string{"bash": huge}}, &fakeDefs{}, nil, nil,
		Config{HeuristicNudges: true, MaxIterations: rounds + 2, MaxToolResultChars: cap})
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}

	final := client.seen[len(client.seen)-1]
	if got := nudgeCount(final); got != 1 {
		t.Fatalf("an oversized repeated result must still draw exactly one nudge, got %d", got)
	}
	var carrier llm.Message
	for _, m := range final {
		if strings.Contains(m.Content, "[loop note — repeated result") {
			carrier = m
		}
	}
	if !strings.Contains(carrier.Content, "truncated") {
		t.Fatalf("the carrier is a truncated delivery and the note does not say so: %.200q", carrier.Content)
	}
	if strings.Contains(carrier.Content, "COMPLETE as delivered above, not truncated") {
		t.Fatalf("THE NOTE CLAIMED COMPLETENESS ABOUT A TRUNCATED MESSAGE — the model is told to stop "+
			"re-fetching exactly when the head never reached it: %.200q", carrier.Content)
	}
	// .
	if !strings.Contains(carrier.Content, "same truncated bytes") {
		t.Fatalf("the note must still say re-fetching cannot recover the omitted part: %.200q", carrier.Content)
	}
}

// .
func TestARepeatedWholeResultIsStillCalledComplete(t *testing.T) {
	client := runRepeatTurn(t, 5, map[string]string{"bash": bigResult("diff")}, true)
	final := client.seen[len(client.seen)-1]
	var carrier llm.Message
	for _, m := range final {
		if strings.Contains(m.Content, "[loop note — repeated result") {
			carrier = m
		}
	}
	if !strings.Contains(carrier.Content, "COMPLETE as delivered above") {
		t.Fatalf("an untruncated repeat must still be called complete: %.200q", carrier.Content)
	}
}
