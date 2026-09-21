package prompt

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestComposeTurnKeepsTheFactsOutOfTheText(t *testing.T) {
	composer, _, _ := setupComposer(t)
	facts := "### Rhythm — last 48h: 12 turns, 80 calls"
	p, err := composer.ComposeTurn("### Standing state (yours)\nwaiting on the review", facts, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Turn != facts {
		t.Fatalf("Prompt.Turn = %q, want the facts whole", p.Turn)
	}
	if strings.Contains(p.Text, "Rhythm") {
		t.Fatal("the turn's facts reached the system text")
	}
	if !strings.Contains(p.Text, "waiting on the review") {
		t.Fatal("the authored working state left the system text")
	}

	// .
	q, err := composer.ComposeTurn("### Standing state (yours)\nwaiting on the review", "### Rhythm — last 48h: 13 turns, 91 calls", 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.Text != p.Text || q.StableLen != p.StableLen {
		t.Fatal("a change in the turn's facts changed the system text")
	}
}

// .
// .
func TestComposeTurnFactsYieldWithAReceiptInTheTurn(t *testing.T) {
	composer, _, _ := setupComposer(t)
	base, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	facts := strings.TrimSpace(strings.Repeat("one arrival that is long enough to matter\n\n", 400))
	// .
	composer.SetMaxTokens(base.TokenEstimate + 60)
	p, err := composer.ComposeTurn("", facts, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Turn == facts {
		t.Fatal("the facts did not yield under pressure")
	}
	if !strings.Contains(p.Turn, SummaryMarker) && !strings.Contains(p.Turn, "Not Shown") {
		t.Fatalf("the facts yielded without a declaration: %q", p.Turn)
	}
	if strings.Contains(p.Text, "This Turn") {
		t.Fatal("the turn's receipt was written into the system text")
	}
}
