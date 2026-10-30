package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestTheLoopsNotesRideBesideTheReplyAndTheRoundLimitIsTheSubstrates(t *testing.T) {
	client := &captureLLM{script: []llm.Response{resp("", toolCall("c1", "note", `{}`)), textResp("what I found")}}
	res, err := New(client, &fakeTools{results: map[string]string{"note": "ok"}}, &fakeDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(1)})).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Spoken != "what I found" {
		t.Fatalf("the reply holds more than the identity's words: %q", res.Spoken)
	}
	if len(res.Notes) != 1 || !strings.HasPrefix(res.Notes[0], "[This turn reached its limit of 1 rounds") {
		t.Fatalf("the round limit's note is not beside the reply: %q", res.Notes)
	}
	last := client.seen[len(client.seen)-1]
	ask := last[len(last)-1].Content
	if ask != "The tool rounds for this turn are used up. Answer now with what you have." {
		t.Fatalf("the last round's ask = %q", ask)
	}
	if strings.Contains(ask, " me ") {
		t.Fatalf("THE SUBSTRATE SPOKE AS THE OPERATOR: %q", ask)
	}

	cut := textResp("half an ans")
	cut.Choices[0].FinishReason = "length"
	res, err = New(&scriptLLM{script: []llm.Response{cut}}, &fakeTools{}, &fakeDefs{}, nil, nil,
		testBounds(Config{MaxIterations: limits.Of(4)})).Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Spoken != "half an ans" || len(res.Notes) != 1 || res.Notes[0] != outputCutNote {
		t.Fatalf("a cut-off reply: spoken %q, notes %q", res.Spoken, res.Notes)
	}
}
