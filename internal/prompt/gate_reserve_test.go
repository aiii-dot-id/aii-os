package prompt

import (
	"strings"
	"testing"
)

func TestTheGateFoldsRing2AndLeavesTheCallerItsReserve(t *testing.T) {
	evidence := strings.Repeat("  - SUPPORTS: a long line of the evidence beneath this belief, carried whole while there is room\n", 30)
	ring2 := "## Who You Have Become\n\n- I keep my word.\n" + evidence + "- I say where I learned it.\n" + evidence
	rings := fakeRings{
		r0: "R0-CONSTITUTION-VERBATIM " + strings.Repeat("c ", 300),
		r5: "R5-FIREWALL-VERBATIM",
		r1: "R1-CHARTER-VERBATIM",
		r3: "R3-WORKING\n\n" + strings.Repeat("a paragraph of working truth.\n\n", 200),
		r4: "R4-STATE\n\n" + strings.Repeat("a paragraph of state.\n\n", 200),
	}
	const budget, reserve = 2000, 600
	g := NewGate(rings, budget)

	whole, _ := g.SystemWithin("CALLER", rings.r1, ring2, 0)
	if got := estimateTokens(whole); got > budget+120 { // the declared-omissions banner rides after enforcement
		t.Fatalf("rig: with no reserve the preamble is folded to the budget: %d tokens against %d", got, budget)
	}

	sys, _ := g.SystemWithin("CALLER", rings.r1, ring2, reserve)
	if got := estimateTokens(sys); got > budget-reserve+120 {
		t.Fatalf("THE PREAMBLE TOOK THE FACILITY'S RESERVE: %d tokens of a %d budget, %d reserved for the facility's own material", got, budget, reserve)
	}
	for _, want := range []string{"R0-CONSTITUTION-VERBATIM", "R5-FIREWALL-VERBATIM", "R1-CHARTER-VERBATIM", "CALLER", "- I keep my word.", "- I say where I learned it."} {
		if !strings.Contains(sys, want) {
			t.Fatalf("%q must survive any reserve: the verbatim rings, the caller and every belief of Ring 2 stay", want)
		}
	}
	if strings.Contains(sys, "a long line of the evidence") {
		t.Fatal("Ring 2's evidence lines did not yield under pressure: the gate must give Ring 2 the composer's ladder (every belief, evidence elided, declared)")
	}
	if !strings.Contains(sys, "evidence line(s) elided") || !strings.Contains(sys, "Not Shown") {
		t.Fatalf("what yielded is not declared:\n%.2000s", sys)
	}
	// The route to more room is the facilities' own. Their budget is the
	// model's window, which the prompt budget does not govern: a fold and
	// an omission that said "raise the prompt budget" named a setting that
	// changes nothing here.
	if strings.Contains(sys, budgetRoute) || strings.Count(sys, windowRoute) < 2 {
		t.Fatalf("a facility's fold and its omissions must each name the wider window, never the prompt budget:\n%.2000s", sys)
	}
	// Unless no window is known: the budget is then the conversation's
	// ceiling, and a wider model would change nothing — what widens it is
	// the window declared, or that ceiling raised.
	g.SetWindowKnown(false)
	if sys, _ = g.SystemWithin("CALLER", rings.r1, ring2, reserve); strings.Contains(sys, windowRoute) || strings.Count(sys, ceilingRoute) < 2 {
		t.Fatalf("with no window known a fold and an omission must each name what widens the conversation's ceiling, never a wider model:\n%.2000s", sys)
	}
}
