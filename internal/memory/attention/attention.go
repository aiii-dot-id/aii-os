package attention

import (
	"strings"
	"time"
)

const (
	AttentionDecayAlert    = "decay_alert"
	AttentionContradiction = "contradiction"
	AttentionFollowup      = "followup"
	AttentionContinuity    = "continuity"
)

const (
	CostLow    = "low"
	CostMedium = "medium"
	CostHigh   = "high"
)

type AttentionItem struct {
	Kind     string
	Cost     string
	Priority float64
	Store    string
	ID       string
	Text     string
	Since    time.Time
}

func OfCost(items []AttentionItem, cost string) []AttentionItem {
	var out []AttentionItem
	for _, it := range items {
		if it.Cost == cost {
			out = append(out, it)
		}
	}
	return out
}

func RenderAttention(items []AttentionItem) string {
	var lines []string
	for _, it := range items {
		lines = append(lines, "- "+it.Text)
	}
	return strings.Join(lines, "\n")
}
