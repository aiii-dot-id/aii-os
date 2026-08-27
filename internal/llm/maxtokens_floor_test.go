package llm

import (
	"context"
	"testing"
)

// The openai dialect used to send max_tokens only when the entry
// declared one ("0 = server default") — and the server default is
// unbounded in practice: the 2026-08-26 collapse emitted a 37KB
// degenerate reply through exactly this omission. The dialect now
// floors at DefaultMaxOutputTokens, the rule the anthropic dialect has
// always applied; an entry value overrides.

func TestOpenAIMaxTokensFloorsAtDefault(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if v, ok := (*got)["max_tokens"].(float64); !ok || int(v) != DefaultMaxOutputTokens {
		t.Fatalf("max_tokens must floor at %d when the entry declares none, got %v (present %v)",
			DefaultMaxOutputTokens, (*got)["max_tokens"], ok)
	}
}

func TestOpenAIMaxTokensEntryValueWins(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", MaxOutputTokens: 32768})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if v, ok := (*got)["max_tokens"].(float64); !ok || int(v) != 32768 {
		t.Fatalf("the entry's max_output_tokens must ride the wire, got %v", (*got)["max_tokens"])
	}
}
