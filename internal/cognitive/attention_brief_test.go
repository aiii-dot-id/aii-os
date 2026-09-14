package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// .
// .
// .
// .
func TestMorningBriefCarriesLowCostAttention(t *testing.T) {
	m := &mockLLM{}
	bw := &mockBriefWriter{}
	brief := NewMorningBrief(&mockStore{}, m, bw, MorningBriefConfig{LocalTime: "07:00"})
	brief.SetAttention(func(context.Context) ([]memory.AttentionItem, error) {
		return []memory.AttentionItem{
			{Kind: memory.AttentionContradiction, Cost: memory.CostMedium, Text: "an open tension: A contradicts B"},
			{Kind: memory.AttentionDecayAlert, Cost: memory.CostLow, Text: "a belief is fading: the old lighthouse is red"},
			{Kind: memory.AttentionFollowup, Cost: memory.CostLow, Text: "an intention untouched for 40 days: learn the tides"},
			{Kind: memory.AttentionConsolidation, Cost: memory.CostSilent, Text: "3 experiences await the unconscious"},
		}, nil
	})
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.lastUser, "old lighthouse is red") || !strings.Contains(m.lastUser, "learn the tides") {
		t.Fatalf("the low-cost items must reach the brief's material:\n%s", m.lastUser)
	}
	if strings.Contains(m.lastUser, "contradicts") || strings.Contains(m.lastUser, "await the unconscious") {
		t.Fatalf("medium and silent items must not reach the brief:\n%s", m.lastUser)
	}
	if bw.brief == "" {
		t.Fatal("the brief must still be written")
	}

	brief.SetAttention(func(context.Context) ([]memory.AttentionItem, error) { return nil, errors.New("store closed") })
	m.lastUser = ""
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatalf("a failed attention read must not fail the brief: %v", err)
	}
	if strings.Contains(m.lastUser, "holding") {
		t.Fatalf("nothing held must render nothing:\n%s", m.lastUser)
	}
}
