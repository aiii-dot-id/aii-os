package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// reasoning_capture_test.go — the model's raw reasoning, as an
// OpenAI-compatible server returns it in reasoning_content (oMLX, vLLM,
// SGLang, DeepSeek: the model's own tokens), is kept on the choice it came
// with as the record, and goes on the message — and so back to the server —
// only when the client's provider entry replays it. A server's normalized
// "reasoning" field alone may carry a provider's summary and is not taken
// for raw; when both arrive they are the same text, taken once.

func TestStreamedReasoningContentIsKeptOnce(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"First, the sum: ","reasoning":"First, the sum: "},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"reasoning_content":"2 plus 2 is 4.","reasoning":"2 plus 2 is 4."},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"FINAL: 4"},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "2+2?"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Reasoning; got != "First, the sum: 2 plus 2 is 4." {
		t.Fatalf("raw reasoning not kept whole and once: %q", got)
	}
	m := resp.Choices[0].Message
	if m.Content != "FINAL: 4" {
		t.Fatalf("the reply changed: %q", m.Content)
	}
	if m.Reasoning != "" {
		t.Fatalf("a client that does not replay put reasoning on the message: %q", m.Reasoning)
	}
}

func TestANormalizedReasoningFieldAloneIsNotTakenForRaw(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"a summary of the thinking"},"finish_reason":null}]}`)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Reasoning; got != "" {
		t.Fatalf("a normalized reasoning field was taken for raw reasoning: %q", got)
	}
}

func TestUnstreamedReasoningContentIsKept(t *testing.T) {
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u","choices":[{"index":0,"message":{"role":"assistant","content":"FINAL: 4","reasoning_content":"because two and two make four"},"finish_reason":"stop"}]}`))
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "2+2?"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Reasoning; got != "because two and two make four" {
		t.Fatalf("raw reasoning of an unstreamed completion not kept: %q", got)
	}
}

// A client whose provider entry replays reasoning puts it on the message
// it came with, streamed or read whole, so the loop sends it back with the
// turn's later calls; one that does not leaves the message without it.
// The record is the same either way.
func TestOnlyAReplayingClientPutsReasoningOnTheMessage(t *testing.T) {
	serve := map[string]func(w http.ResponseWriter){
		"streamed": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"read it first"},"finish_reason":null}]}`)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
			sseChunk(t, w, "[DONE]")
		},
		"read whole": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"u","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"read it first"},"finish_reason":"stop"}]}`))
		},
	}
	for name, write := range serve {
		for _, replay := range []bool{true, false} {
			srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) { write(w) })
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", ReplayReasoning: replay, NoStream: name == "read whole"})
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "go"}}, ChatOptions{})
			if err != nil {
				t.Fatalf("%s replay=%v: %v", name, replay, err)
			}
			want := ""
			if replay {
				want = "read it first"
			}
			if got := resp.Choices[0].Message.Reasoning; got != want {
				t.Fatalf("%s replay=%v: the message carries %q, want %q", name, replay, got, want)
			}
			if got := resp.Choices[0].Reasoning; got != "read it first" {
				t.Fatalf("%s replay=%v: the record is %q", name, replay, got)
			}
		}
	}
}

// A message that carries reasoning sends it back in the field the server
// returned it in, and a message without any carries no such field. The
// client sends what it is given, as the anthropic dialect does with
// thinking blocks: whether a message carries reasoning at all is the
// client's decision on receipt (TestOnlyAReplayingClientPutsReasoningOnTheMessage).
func TestReasoningOnAMessageTravelsAsReasoningContent(t *testing.T) {
	var sent map[string]any
	srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		sent = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		sseChunk(t, w, "[DONE]")
	})
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	history := []Message{
		{Role: "user", Content: "2+2?"},
		{Role: "assistant", Content: "FINAL: 4", Reasoning: "two and two make four"},
		{Role: "user", Content: "and 3+3?"},
	}
	if _, err := c.Chat(context.Background(), history, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := sent["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("sent %d messages, want 3: %v", len(msgs), sent["messages"])
	}
	if got := msgs[1].(map[string]any)["reasoning_content"]; got != "two and two make four" {
		t.Fatalf("the assistant message's reasoning went out as %v", got)
	}
	for _, i := range []int{0, 2} {
		if v, ok := msgs[i].(map[string]any)["reasoning_content"]; ok {
			t.Fatalf("message %d carries no reasoning and sent reasoning_content %v", i, v)
		}
	}
}

// The estimate counts what is sent, so reasoning carried for replay is
// part of the budget the fit enforces.
func TestTheEstimateCountsReasoningCarriedForReplay(t *testing.T) {
	thought := strings.Repeat("the file must be read before the answer. ", 100)
	plain, err := EstimateInputTokens([]Message{{Role: "assistant", Content: "ok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	with, err := EstimateInputTokens([]Message{{Role: "assistant", Content: "ok", Reasoning: thought}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if with-plain < 500 {
		t.Fatalf("%d characters of reasoning added %d tokens to the estimate", len(thought), with-plain)
	}
}

// Reasoning belongs to the context that produced it, like a thinking
// block: when history changes under a turn it goes too, counted so the
// loop declares the reset. The visible text stays. DropReasoning takes
// the reasoning alone and leaves thinking blocks, which a provider may
// require kept.
func TestResetThinkingClearsReplayedReasoning(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "reading", Reasoning: "read the file first"},
		{Role: "tool", ToolCallID: "1", Content: "ok"},
	}
	if n := ResetThinking(messages); n != 1 {
		t.Fatalf("reset count=%d, want 1", n)
	}
	if messages[0].Reasoning != "" || messages[0].Content != "reading" {
		t.Fatalf("reset left %+v", messages[0])
	}
	if n := ResetThinking(messages); n != 0 {
		t.Fatalf("an already-reset message counted again: %d", n)
	}
	withThinking := []Message{{Role: "assistant", Content: "x", Reasoning: "plan",
		Thinking: []ThinkingBlock{{Kind: "thinking", Text: "kept"}}}}
	if n := DropReasoning(withThinking); n != 1 || withThinking[0].Reasoning != "" || len(withThinking[0].Thinking) != 1 {
		t.Fatalf("DropReasoning took %d and left %+v", n, withThinking[0])
	}
}

// Without reasoning, a message goes out byte for byte as it always did:
// replay changes nothing for a provider that never asks for it.
func TestAMessageWithoutReasoningEncodesAsBefore(t *testing.T) {
	m := Message{Role: "assistant", Content: "reading", ToolCalls: []ToolCall{{ID: "1", Type: "function"}}}
	before, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(wireMessages([]Message{m}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "[" + string(before) + "]"; string(after) != want {
		t.Fatalf("the wire changed a message without reasoning:\n got %s\nwant %s", after, want)
	}
}
