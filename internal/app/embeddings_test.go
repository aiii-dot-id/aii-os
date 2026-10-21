package app

import (
	"context"
	"strings"
	"testing"
)

// The seam refuses by name when the identity's provider names no
// embeddings model, and when no client is active, before any text
// could leave.
func TestProviderEmbedderRefusesByName(t *testing.T) {
	a := New(&Config{})
	_, _, err := providerEmbedder{a}.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "embeddings_model") {
		t.Fatalf("no model named must refuse by name, got %v", err)
	}
	a.activateLLMRuntime(nil, providerEntry{Name: "test", EmbeddingsModel: "embed-1"}, Config{Prompt: PromptConfig{MaxTokens: 0}})
	model, _, err := providerEmbedder{a}.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "no model client") || model != "embed-1" {
		t.Fatalf("no client must refuse and still name the model, got %q %v", model, err)
	}
}
