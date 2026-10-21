package identity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type measureStore interface {
	Measure(window time.Duration) (store.MeasurementReadout, error)
	GraphAudit() (store.GraphAudit, error)
	FamilyAttempts(family string, limit int) (store.FamilyRecord, error)
}

func (e *Engine) verbMeasure(ctx context.Context, args map[string]any) (string, error) {

	if family, ok := args["family"].(string); ok && strings.TrimSpace(family) != "" {
		family = strings.TrimSpace(family)
		limit := 20
		if n, ok := numArg(args["limit"]); ok && n > 0 {
			limit = int(n)
		}
		r, err := e.store.FamilyAttempts(family, limit)
		if err != nil {
			return "", fmt.Errorf("measure family: %w", err)
		}
		return store.RenderFamily(r, "playbooks/"+family+"/SKILL.md in your home"), nil
	}
	window := 48 * time.Hour
	if h, ok := numArg(args["hours"]); ok && h > 0 {
		window = time.Duration(h) * time.Hour
	}
	r, err := e.store.Measure(window)
	if err != nil {
		return "", fmt.Errorf("measure: %w", err)
	}
	out := store.RenderMeasurement(r)

	audit, err := e.store.GraphAudit()
	if err != nil {
		out += "\nRecord audit unavailable: " + err.Error()
	} else {
		out += "\n" + store.RenderGraphAudit(audit)
	}

	basis, cov, unavailable, err := e.instruments.UsableCoverage(ctx)
	if err != nil {
		return out + "\nMeaning layer: coverage unreadable — " + err.Error(), nil
	}
	return out + "\n" + memory.RenderCoverage(basis, cov, unavailable), nil
}
