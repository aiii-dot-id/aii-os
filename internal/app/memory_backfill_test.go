package app

import (
	"strings"
	"testing"
)

// .
// .
func TestMemoryEmbedderBasisIsTheProviderAndItsModel(t *testing.T) {
	a := &App{}
	em := memoryEmbedder{a}
	a.activeProvider = providerEntry{Name: "zAI"}
	if _, err := em.Basis(); err == nil || !strings.Contains(err.Error(), "embeddings_model") {
		t.Fatalf("no model must refuse with the remedy: %v", err)
	}
	if _, _, err := em.Embed(t.Context(), []string{"x"}); err == nil {
		t.Fatal("embed without a model must refuse")
	}
	a.activeProvider = providerEntry{Name: "zAI", EmbeddingsModel: "embedding-3"}
	basis, err := em.Basis()
	if err != nil || basis != "zAI/embedding-3" {
		t.Fatalf("basis = %q %v", basis, err)
	}
	if _, _, err := em.Embed(t.Context(), []string{"x"}); err == nil || !strings.Contains(err.Error(), "no model client") {
		t.Fatalf("embed without a client must say so: %v", err)
	}
}
