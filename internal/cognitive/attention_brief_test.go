package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
)

// The brief carries the low-cost attention items — a fading belief, an
// untouched intention — as material for the day, and never the medium
// ones (those wait for a turn). An attention read that fails costs the
// brief nothing.
func TestMorningBriefCarriesLowCostAttention(t *testing.T) {
	m := &mockLLM{}
	bw := &mockBriefWriter{}
	brief := NewMorningBrief(&mockStore{}, m, bw, MorningBriefConfig{LocalTime: "07:00"})
	brief.SetAttention(func(context.Context) ([]attention.AttentionItem, error) {
		return []attention.AttentionItem{
			{Kind: attention.AttentionContradiction, Cost: attention.CostMedium, Text: "an open tension: A contradicts B"},
			{Kind: attention.AttentionDecayAlert, Cost: attention.CostLow, Text: "a belief is fading: the old lighthouse is red"},
			{Kind: attention.AttentionFollowup, Cost: attention.CostLow, Text: "an intention untouched for 40 days: learn the tides"},
		}, nil
	})
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.lastUser, "old lighthouse is red") || !strings.Contains(m.lastUser, "learn the tides") {
		t.Fatalf("the low-cost items must reach the brief's material:\n%s", m.lastUser)
	}
	if strings.Contains(m.lastUser, "contradicts") {
		t.Fatalf("medium items must not reach the brief:\n%s", m.lastUser)
	}
	if bw.brief == "" {
		t.Fatal("the brief must still be written")
	}

	brief.SetAttention(func(context.Context) ([]attention.AttentionItem, error) { return nil, errors.New("store closed") })
	m.lastUser = ""
	if err := brief.Execute(context.Background()); err != nil {
		t.Fatalf("a failed attention read must not fail the brief: %v", err)
	}
	if strings.Contains(m.lastUser, "holding") {
		t.Fatalf("nothing held must render nothing:\n%s", m.lastUser)
	}
}
