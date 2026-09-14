package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
func (e *Engine) verbMeasure(ctx context.Context, args map[string]interface{}) (string, error) {
	window := 48 * time.Hour
	if h, ok := numArg(args["hours"]); ok && h > 0 {
		window = time.Duration(h) * time.Hour
	}
	r, err := e.store.Measure(window)
	if err != nil {
		return "", fmt.Errorf("measure: %w", err)
	}
	out := store.RenderMeasurement(r)
	// .
	// .
	// .
	audit, err := e.store.GraphAudit()
	if err != nil {
		out += "\nRecord audit unavailable: " + err.Error()
	} else {
		out += "\n" + store.RenderGraphAudit(audit)
	}
	// .
	// .
	basis, cov, unavailable, err := e.instruments.VectorCoverage(ctx)
	if err != nil {
		return out + "\nMeaning layer: coverage unreadable — " + err.Error(), nil
	}
	return out + "\n" + memory.RenderCoverage(basis, cov, unavailable), nil
}
