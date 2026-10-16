package app

import (
	"context"
	"fmt"
)

type providerEmbedder struct{ a *App }

func (e providerEmbedder) Embed(ctx context.Context, inputs []string) (string, [][]float32, error) {

	entry, client := e.a.activeRuntime()
	if entry.EmbeddingsModel == "" {
		return "", nil, fmt.Errorf("provider %q names no embeddings_model (Settings → Providers)", entry.Name)
	}
	if client == nil {
		return entry.EmbeddingsModel, nil, fmt.Errorf("no model client is active")
	}
	vectors, err := client.Embed(ctx, entry.EmbeddingsModel, inputs)
	return entry.EmbeddingsModel, vectors, err
}
