package conversation

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"strings"
	"testing"
)

func iterateThinkingResponse(id string) llm.Response {
	r := resp("", toolCall(id, "read", "{}"))
	r.Choices[0].Message.Role = "assistant"
	r.Choices[0].Message.Thinking = []llm.ThinkingBlock{{Kind: "thinking", Signature: "synthetic-" + id}}
	return r
}
func TestCachePressureWarningPreservesSystemPrefix(t *testing.T) {
	system := llm.Message{Role: "system", Content: "stable identity", StableLen: len("stable identity")}
	current := llm.Message{Role: "user", Content: strings.Repeat("a long question the operator asked in some detail ", 260)}
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "read"}}}}
	sent := system
	sent.Content += systemAdditions()
	used, err := llm.EstimateInputTokens([]llm.Message{sent, current}, defs.defs)
	if err != nil {
		t.Fatal(err)
	}
	budget := used + 600
	if used*100 < budget*contextTightPercent {
		t.Fatal("fixture is not under pressure")
	}
	c := &scriptLLM{script: []llm.Response{iterateThinkingResponse("c1"), iterateThinkingResponse("c2"), textResp("done")}}
	l := New(c, &fakeTools{results: map[string]string{"read": "ok"}}, defs, nil, nil, Config{MaxIterations: 10, ContextBudgetTokens: budget})
	if _, err := l.RunSystem(context.Background(), system, []llm.Message{current}, 0); err != nil {
		t.Fatal(err)
	}
	if len(c.requests) != 3 {
		t.Fatalf("got %d requests", len(c.requests))
	}
	first, second := c.requests[0][0], c.requests[1][0]
	if len(c.requests[1][2].Thinking) == 0 {
		t.Fatal("fixture did not replay thinking")
	}
	if first.Content != second.Content {
		t.Fatalf("system changed while thinking replayed: first warning=%v, second warning=%v, StableLen %d->%d; no history was evicted", strings.Contains(first.Content, contextTightNote), strings.Contains(second.Content, contextTightNote), first.StableLen, second.StableLen)
	}
}
func TestCacheFoldingDoesNotRetainInvalidatedThinking(t *testing.T) {
	msgs := []llm.Message{{Role: "system", Content: "stable", StableLen: 6}, {Role: "user", Content: "go"}, iterateThinkingResponse("c1").Choices[0].Message, {Role: "tool", ToolCallID: "c1", Content: strings.Repeat("old tool evidence ", 600)}, iterateThinkingResponse("c2").Choices[0].Message, {Role: "tool", ToolCallID: "c2", Content: "ok"}}
	used, err := llm.EstimateInputTokens(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := msgs[3].Content
	st := fitState{current: 1}
	if err := fitRequest(&msgs, &st, "stable", nil, used-1000, nil); err != nil {
		t.Fatal(err)
	}
	if msgs[3].Content == before {
		t.Fatal("fixture did not fold")
	}
	if len(msgs[4].Thinking) > 0 {
		t.Fatalf("earlier tool result replaced (%d->%d bytes), later assistant still carries %d thinking block(s) bound to its old prefix", len(before), len(msgs[3].Content), len(msgs[4].Thinking))
	}
}

func TestCacheWarningSurvivesHistoryCompaction(t *testing.T) {
	messages := []llm.Message{{Role: "system", Content: "stable" + contextTightNote, StableLen: 6}, {Role: "user", Content: strings.Repeat("old evidence ", 1000)}, {Role: "assistant", Content: "old answer"}, {Role: "user", Content: "current"}}
	st := fitState{current: 3, warned: true}
	if err := fitRequest(&messages, &st, "stable", nil, 300, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages[0].Content, contextTightNote) {
		t.Fatal("compaction removed the persistent pressure warning")
	}
}
