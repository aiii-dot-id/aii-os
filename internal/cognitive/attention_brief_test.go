package cognitive

import (
	"context"
	"errors"
	"fmt"
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

	// THE BRIEF SAYS WHEN ITS LIST WAS CUT, so "12 beliefs at 0.15" does not
	// stand with no way to see past them. Thirteen fading
	// beliefs: twelve shown and the sentence; twelve: no sentence.
	fading := func(n int, priority float64) []attention.AttentionItem {
		var items []attention.AttentionItem
		for i := 1; i <= n; i++ {
			items = append(items, attention.AttentionItem{Kind: attention.AttentionDecayAlert, Cost: attention.CostLow, Priority: priority - float64(i)/100, Text: fmt.Sprintf("a belief is fading: belief %d", i)})
		}
		return items
	}
	followups := func(n int, priority float64) []attention.AttentionItem {
		var items []attention.AttentionItem
		for i := 1; i <= n; i++ {
			items = append(items, attention.AttentionItem{Kind: attention.AttentionFollowup, Cost: attention.CostLow, Priority: priority, Text: fmt.Sprintf("an intention untouched: intention %d", i)})
		}
		return items
	}
	for _, c := range []struct {
		name  string
		items []attention.AttentionItem
		shown int    // fading beliefs shown
		said  string // the sentence, "" for none
	}{
		{"thirteen fading", fading(13, 1), 12, "These are the 12 most faded; more are fading."},
		{"twelve fading", fading(12, 1), 12, ""},
		// a review, 2026-11-06: the list mixes kinds; the sentence counts the
		// fading beliefs it shows, and is said only when some were left out.
		{"thirteen intentions and no fading belief", followups(13, 30), 0, ""},
		{"ten intentions above five fading beliefs", append(followups(10, 30), fading(5, 1)...), 2, "These are the 2 most faded; more are fading."},
		{"ten intentions above two fading beliefs", append(followups(10, 30), fading(2, 1)...), 2, ""},
	} {
		items := c.items
		brief.SetAttention(func(context.Context) ([]attention.AttentionItem, error) { return items, nil })
		m.lastUser = ""
		if err := brief.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		shown := strings.Count(m.lastUser, "a belief is fading: belief ")
		said := strings.Contains(m.lastUser, "most faded; more are fading.")
		if shown != c.shown || said != (c.said != "") || (c.said != "" && !strings.Contains(m.lastUser, c.said)) {
			t.Fatalf("%s: %d fading shown, sentence %v:\n%s", c.name, shown, said, m.lastUser)
		}
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
