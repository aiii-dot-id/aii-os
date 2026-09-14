package app

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// .
// .
// .
// .
// .
// .

const (
	memoryBackfillAlarmID = "memory.backfill"
	memoryOwnerName       = "memory"
	// .
	// .
	// .
	// .
	memoryBackfillFirstDelay = 30 * time.Second
	memoryBackfillEvery      = 10 * time.Minute
)

// .
// .
// .
// .
type memoryEmbedder struct{ a *App }

func (e memoryEmbedder) Basis() (string, error) {
	entry := e.a.currentProvider()
	if entry.EmbeddingsModel == "" {
		return "", fmt.Errorf("provider %q names no embeddings_model (Settings → Providers)", entry.Name)
	}
	return entry.Name + "/" + entry.EmbeddingsModel, nil
}

func (e memoryEmbedder) Embed(ctx context.Context, inputs []string) (string, [][]float32, error) {
	basis, err := e.Basis()
	if err != nil {
		return "", nil, err
	}
	if e.a.llmSwap == nil || e.a.llmSwap.Current() == nil {
		return basis, nil, fmt.Errorf("no model client is active")
	}
	vectors, err := e.a.llmSwap.Current().Embed(ctx, e.a.currentProvider().EmbeddingsModel, inputs)
	return basis, vectors, err
}

// .
// .
// .
// .
type memoryOwner struct{ a *App }

func (o memoryOwner) Name() string { return memoryOwnerName }

func (o memoryOwner) OnAlarm(ctx context.Context, _ string, _ string, _ int64, _ string) cognitive.AlarmResult {
	if _, safe := o.a.SafeMode(); safe || o.a.engine == nil {
		return cognitive.AlarmResult{Accepted: true}
	}
	rep, err := o.a.engine.Instruments().Backfill(ctx, memory.BackfillBudget)
	switch {
	case err != nil:
		log.Printf("MEMORY: meaning backfill stopped — %v (what landed stays; the next pass continues)", err)
	case rep.Unavailable != "":
	case rep.Embedded > 0 || rep.Dropped > 0 || rep.Pruned > 0:
		log.Printf("MEMORY: %s", rep.Line())
	}
	return cognitive.AlarmResult{Accepted: true}
}

// .
// .
// .
func (a *App) armMemoryBackfill() error {
	every := memoryBackfillEvery.Milliseconds()
	first := time.Now().UTC().Add(memoryBackfillFirstDelay).UnixMilli()
	return a.timeFac.SetAlarm(memoryBackfillAlarmID, memoryOwnerName, "wall", first, &every, "")
}
