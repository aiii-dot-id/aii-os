package wire

import (
	"encoding/json"
	"math"
	"testing"
)

// The request-side types' JSON tags are the OpenAI chat-completions wire
// and what the admission estimate measures; the response side decodes a
// completion. Every field marked runtime-only (json:"-") must stay off
// the wire. These literals were written by the types while they were
// still package llm; a moved definition that changes one byte of them — a
// tag, a field order, a runtime field leaking — fails here.
func TestTheWireTypesKeepTheirBytes(t *testing.T) {
	encodes := func(name string, v any, want string) {
		t.Helper()
		if b, err := json.Marshal(v); err != nil || string(b) != want {
			t.Errorf("%s encodes as\n%s (%v), want\n%s", name, b, err, want)
		}
	}
	call := ToolCall{ID: "call_1", Type: "function", EmissionOrdinal: 3}
	call.Function.Name = "recall"
	call.Function.Arguments = `{"q":"tides"}`
	full := Message{Role: "assistant", Content: "hi <b>&", ToolCallID: "c0", ToolCalls: []ToolCall{call},
		StableLen: 2, CacheBefore: true, Abridged: true, Qualifies: true, Reasoning: "r",
		Thinking:      []ThinkingBlock{{Kind: "thinking", Text: "t", Signature: "s"}},
		NativeContent: json.RawMessage(`[1]`), NativeDialect: "anthropic", OutputTokens: 9}
	encodes("a message with every runtime field set", full,
		`{"role":"assistant","content":"hi \u003cb\u003e\u0026","tool_call_id":"c0","tool_calls":[{"id":"call_1","type":"function","function":{"name":"recall","arguments":"{\"q\":\"tides\"}"}}]}`)
	encodes("a thinking block (the estimate marshals it by field name)", ThinkingBlock{Kind: "redacted_thinking", Signature: "s", Data: "d"},
		`{"Kind":"redacted_thinking","Text":"","Signature":"s","Data":"d"}`)
	tool := ToolDefinition{Type: "function", Function: ToolFunction{Name: "recall", Description: "find", Parameters: map[string]any{"type": "object"}}}
	encodes("a tool definition", tool,
		`{"type":"function","function":{"name":"recall","description":"find","parameters":{"type":"object"}}}`)
	encodes("a choice", Choice{Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop", Reasoning: "raw"},
		`{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}`)

	var r Response
	if err := json.Unmarshal([]byte(`{"id":"cmpl-1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ANSWER","tool_calls":[{"id":"c1","type":"function","function":{"name":"recall","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`), &r); err != nil {
		t.Fatal(err)
	}
	wantUsage := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true, CachedPromptTokens: 4, CacheReadReported: true}
	if r.ID != "cmpl-1" || len(r.Choices) != 1 || r.Choices[0].FinishReason != "tool_calls" || r.Choices[0].Message.ToolCalls[0].Function.Name != "recall" || r.Usage != wantUsage {
		t.Errorf("a completion decodes as %+v", r)
	}
	r.ModelID, r.RequestID, r.CallID, r.Duration = "cfg", "req", "call", 7
	encodes("a response with its runtime fields set", r,
		`{"choices":[{"message":{"role":"assistant","content":"ANSWER","tool_calls":[{"id":"c1","type":"function","function":{"name":"recall","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},"id":"cmpl-1"}`)
}

// A missing counter is never a measured zero, and inconsistent counts are
// not a measurement at all: usage a provider omits decodes as missing,
// usage that contradicts itself as invalid — no count kept, and what was
// sent kept only as words for the log — and an explicit zero as the
// measurement it is. What a dialect normalizes itself is held to the same
// rule by Validate, and the sum of counts refuses a negative or an
// overflow rather than wrapping. Written against the unexported validate
// and tokenSum while they were package llm's, and green there.
func TestAMissingCounterIsNeverAMeasuredZero(t *testing.T) {
	for body, want := range map[string]Usage{
		`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`: {PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true},
		`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`:   {Reported: true},
		`{"prompt_tokens":10,"completion_tokens":5}`:                   {Problem: UsageMissing},
		`{}`: {Problem: UsageMissing},
		`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":16}`:                                                                    {Problem: UsageInvalid, Refused: "input=10 output=5 total=16 cache_read=0 cache_write=0 cache_write_5m=0 cache_write_1h=0"},
		`{"prompt_tokens":-1,"completion_tokens":1,"total_tokens":0}`:                                                                     {Problem: UsageInvalid, Refused: "input=-1 output=1 total=0 cache_read=0 cache_write=0 cache_write_5m=0 cache_write_1h=0"},
		`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":11}}`:                       {Problem: UsageInvalid, Refused: "input=10 output=5 total=15 cache_read=11 cache_write=0 cache_write_5m=0 cache_write_1h=0"},
		`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":6}}`: {Problem: UsageInvalid, Refused: "input=10 output=5 total=15 cache_read=5 cache_write=6 cache_write_5m=0 cache_write_1h=0"},
		`{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":6}}`: {PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true,
			CachedPromptTokens: 4, CacheWriteTokens: 6, CacheReadReported: true, CacheWriteReported: true},
	} {
		var got Usage
		if err := json.Unmarshal([]byte(body), &got); err != nil || got != want {
			t.Errorf("%s decodes as %+v (%v), want %+v", body, got, err, want)
		}
	}
	split := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true, CacheWriteTokens: 3, CacheWrite5mTokens: 2, CacheWrite1hTokens: 2}
	split.Validate()
	if split != (Usage{Problem: UsageInvalid, Refused: "input=10 output=5 total=15 cache_read=0 cache_write=3 cache_write_5m=2 cache_write_1h=2"}) {
		t.Errorf("cache writes split into more than were written survived validation: %+v", split)
	}
	whole := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true, CacheWriteTokens: 4, CacheWrite5mTokens: 2, CacheWrite1hTokens: 2}
	kept := whole
	kept.Validate()
	if kept != whole {
		t.Errorf("consistent usage was changed by validation: %+v", kept)
	}
	for _, c := range []struct {
		in    []int
		sum   int
		valid bool
	}{{nil, 0, true}, {[]int{2, 3}, 5, true}, {[]int{-1}, 0, false}, {[]int{math.MaxInt, 1}, 0, false}} {
		if sum, valid := TokenSum(c.in...); sum != c.sum || valid != c.valid {
			t.Errorf("TokenSum(%v) = %d, %v; want %d, %v", c.in, sum, valid, c.sum, c.valid)
		}
	}
}
