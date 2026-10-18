package prompt

import (
	"strings"
	"testing"
)

// A turn's working state and its facts ride the current message's block,
// budgeted with the rest of the prompt, and never enter the system text:
// what the identity changes as it works must not change the prefix every
// provider cache keys on.
func TestComposeTurnCarriesTheWorkingStateInTheBlock(t *testing.T) {
	composer, _ := setupComposer(t)
	work := "### Standing state (yours)\nwaiting on the review"
	facts := "### Rhythm — last 48h: 12 turns, 80 calls"
	p, err := composer.ComposeTurn(work, facts, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Index(p.Turn, "# What Is Right in Front of You")
	if head != 0 || !strings.Contains(p.Turn, "waiting on the review") || !strings.HasSuffix(p.Turn, facts) {
		t.Fatalf("Prompt.Turn does not carry the working state then the facts:\n%s", p.Turn)
	}
	for _, leaked := range []string{"Rhythm", "What Is Right in Front of You", "waiting on the review"} {
		if strings.Contains(p.Text, leaked) {
			t.Fatalf("%q reached the system text", leaked)
		}
	}

	// A later turn: the identity updated its work and the facts moved on.
	// The system bytes, and so the cache's prefix, are the same.
	q, err := composer.ComposeTurn("### Before you act\nreads first\n\n### Standing state (yours)\nthe review landed", "### Rhythm — last 48h: 13 turns, 91 calls", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if q.Text != p.Text || q.StableLen != p.StableLen {
		t.Fatal("a change in the working state or the facts changed the system text")
	}
	if q.Turn == p.Turn {
		t.Fatal("the block did not follow the working state")
	}
}

// A compose without a block — a sub-agent's, a facility's — has nowhere
// else to put Ring 4: it stays in the system text, as before.
func TestAComposeWithoutABlockKeepsTheWorkingStateInItsText(t *testing.T) {
	composer, _ := setupComposer(t)
	for name, compose := range map[string]func(string, int) (*Prompt, error){
		"Compose": composer.Compose, "ComposeFolded": composer.ComposeFolded,
	} {
		p, err := compose("### Standing state (yours)\nwaiting on the review", 0)
		if err != nil {
			t.Fatal(err)
		}
		if p.Turn != "" {
			t.Fatalf("%s filled a block no caller sends: %q", name, p.Turn)
		}
		if !strings.Contains(p.Text, "What Is Right in Front of You") {
			t.Fatalf("%s left Ring 4 out of the system text", name)
		}
	}
}

// Under pressure the facts yield on Ring 4's rung like the rest of it,
// and their receipt is written where they would have been.
func TestComposeTurnFactsYieldWithAReceiptInTheTurn(t *testing.T) {
	composer, _ := setupComposer(t)
	base, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	facts := strings.TrimSpace(strings.Repeat("one arrival that is long enough to matter\n\n", 400))
	// Room for the system text and a sliver, not for the facts.
	composer.SetMaxTokens(base.TokenEstimate + 60)
	p, err := composer.ComposeTurn("", facts, "", 0)
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

// The working state yields the same way, and its receipt, too, is in the
// block: a receipt in the system text would change the prefix exactly
// when the block's contents did.
func TestComposeTurnWorkingStateYieldsWithItsReceiptInTheBlock(t *testing.T) {
	composer, _ := setupComposer(t)
	base, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	work := strings.TrimSpace(strings.Repeat("a step of the plan that is long enough to matter\n\n", 400))
	composer.SetMaxTokens(base.TokenEstimate + 60)
	p, err := composer.ComposeTurn(work, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Turn, work) {
		t.Fatal("the working state did not yield under pressure")
	}
	if !strings.Contains(p.Turn, SummaryMarker) && !strings.Contains(p.Turn, "Not Shown") {
		t.Fatalf("the working state yielded without a declaration in the block: %q", p.Turn)
	}
	if strings.Contains(p.Text, "What Is Right in Front of You") {
		t.Fatal("the working state's receipt was written into the system text")
	}
}

// THE SAFE SECTION NEVER YIELDS: SAFE remains in every prompt frame.
// Under the pressure that folds or drops the
// working state and the facts, it stays whole, first in the block, and
// out of the system text.
func TestComposeTurnKeepsTheSafeSectionWholeUnderPressure(t *testing.T) {
	composer, _ := setupComposer(t)
	base, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	safe := "# SAFE MODE\n\nYou are in SAFE MODE. " + strings.TrimSpace(strings.Repeat("what happened, in the runtime's words. ", 40))
	work := strings.TrimSpace(strings.Repeat("a step of the plan that is long enough to matter\n\n", 400))
	facts := strings.TrimSpace(strings.Repeat("one arrival that is long enough to matter\n\n", 400))
	composer.SetMaxTokens(base.TokenEstimate + 60)
	p, err := composer.ComposeTurn(work, facts, safe, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Turn, "Not Shown") && !strings.Contains(p.Turn, SummaryMarker) {
		t.Fatalf("rig: nothing yielded, so this was no pressure:\n%s", p.Turn)
	}
	if !strings.HasPrefix(p.Turn, safe) {
		t.Fatalf("the SAFE section is not whole and first in the block:\n%s", p.Turn)
	}
	if strings.Contains(p.Text, "SAFE MODE") {
		t.Fatal("the SAFE section reached the system text")
	}
	for _, s := range p.Sections {
		if s.Source == "safe" && (s.Folded || s.Content != safe) {
			t.Fatalf("the SAFE section yielded: %+v", s)
		}
	}
}
