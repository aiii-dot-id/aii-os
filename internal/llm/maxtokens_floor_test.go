package llm

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .
// .

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
