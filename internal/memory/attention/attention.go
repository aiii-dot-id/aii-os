package attention

import (
	"fmt"
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

const Limit = 12

func Cut(items []AttentionItem, limit int) (shown []AttentionItem, more bool) {
	if limit > 0 && len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

func CutSaid(shown int) string {
	return fmt.Sprintf("These are the %d most faded; more are fading.", shown)
}

func OfKind(items []AttentionItem, kind string) []AttentionItem {
	var out []AttentionItem
	for _, it := range items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}
