package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

type fakeRings struct{ r0, r5, r1, r2, r3, r4 string }

func (f fakeRings) Ring0() string { return f.r0 }
func (f fakeRings) Ring5() string { return f.r5 }
func (f fakeRings) Ring3() string { return f.r3 }
func (f fakeRings) Ring4() string { return f.r4 }

// THE CONTRACT: Rings 0/5/1/2 remain whole; Rings 3/4 may yield, never silently.
func TestGateRingContract(t *testing.T) {
	rings := fakeRings{
		r0: "R0-CONSTITUTION-VERBATIM",
		r5: "R5-FIREWALL-VERBATIM",
		r1: "R1-CHARTER-VERBATIM",
		r2: "R2-IDENTITY",
		r3: "R3-WORKING",
		r4: "R4-STATE",
	}
	g := NewGate(rings, 100000)
	sys, _ := g.SystemWithIdentitySeam("CALLER", rings.r1, rings.r2)

	for _, want := range []string{"R0-CONSTITUTION-VERBATIM", "R5-FIREWALL-VERBATIM", "R1-CHARTER-VERBATIM", "CALLER", "R2-IDENTITY", "R3-WORKING", "R4-STATE"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("within budget: %q must be present", want)
		}
	}
	// Order: authority and identity first, caller before the elastic tail.
	if !(strings.Index(sys, "R0-CONSTITUTION-VERBATIM") < strings.Index(sys, "CALLER") &&
		strings.Index(sys, "R2-IDENTITY") < strings.Index(sys, "CALLER") &&
		strings.Index(sys, "CALLER") < strings.Index(sys, "R4-STATE")) {
		t.Fatal("order: authority → identity → caller → elastic tail")
	}

	// Budget pressure: Rings 4 and 3 yield with a receipt. Ring 2 remains
	// whole; the LLM boundary refuses if the required request cannot fit.
	tightRings := fakeRings{
		r0: "R0-CONSTITUTION-VERBATIM " + strings.Repeat("x ", 200),
		r5: "R5-FIREWALL-VERBATIM",
		r1: "R1-CHARTER-VERBATIM",
		r2: "R2-IDENTITY " + strings.Repeat("y ", 200),
		r3: "R3-WORKING " + strings.Repeat("z ", 200),
		r4: "R4-STATE " + strings.Repeat("w ", 200),
	}
	tight := NewGate(tightRings, 120) // fits authority alone

	sys2, _ := tight.SystemWithIdentitySeam("CALLER", tightRings.r1, tightRings.r2)
	if !strings.Contains(sys2, "R0-CONSTITUTION-VERBATIM") || !strings.Contains(sys2, "R5-FIREWALL-VERBATIM") || !strings.Contains(sys2, "R1-CHARTER-VERBATIM") {
		t.Fatal("verbatim rings must survive ANY budget")
	}
	if strings.Contains(sys2, "R4-STATE") {
		t.Fatal("Ring 4 must trim first")
	}
	if !strings.Contains(sys2, "R2-IDENTITY") {
		t.Fatal("Ring 2 identity material must remain whole")
	}

	if !strings.Contains(sys2, "Not Shown") {
		t.Fatal("gate trims must be DECLARED — the omission banner is missing")
	}
}

func TestGateBudgetsReadTimeRing2(t *testing.T) {
	ring2 := "## Who You Have Become\n\n" + strings.Repeat("identity evidence ", 200)
	g := NewGate(fakeRings{r0: "R0"}, 40)
	got, _ := g.SystemWithIdentitySeam("facility", "", ring2)
	if !strings.Contains(got, ring2) {
		t.Fatalf("derived Ring 2 was altered under pressure: %s", got)
	}
}

func TestGateLiveBudgetChange(t *testing.T) {
	rings := fakeRings{
		r0: "R0", r5: "R5", r1: "R1",
		r3: "R3-WORKING " + strings.Repeat("detail ", 400),
		r4: "R4-STATE " + strings.Repeat("state ", 400),
	}
	g := NewGate(rings, 100000)
	if before, _ := g.SystemWithIdentitySeam("CALLER", rings.r1, rings.r2); !strings.Contains(before, "R4-STATE") {
		t.Fatal("generous startup budget unexpectedly trimmed Ring 4")
	}
	g.SetMaxTokens(100)
	after, _ := g.SystemWithIdentitySeam("CALLER", rings.r1, rings.r2)
	if strings.Contains(after, "R4-STATE") || !strings.Contains(after, "Not Shown") {
		t.Fatal("live provider budget was not enforced with a declared omission")
	}
}

// Raw facility content cannot suppress authority by quoting it. The gate
// obtains rings from their owners, never by searching caller prose.
func TestGateDoesNotInferAuthorityFromCallerText(t *testing.T) {
	rings := fakeRings{
		r0: "R0-CONSTITUTION-VERBATIM",
		r5: "R5-FIREWALL-VERBATIM",
		r1: "R1-CHARTER-VERBATIM",
		r2: "R2-IDENTITY",
		r3: "R3-WORKING",
		r4: "R4-STATE",
	}
	g := NewGate(rings, 100000)

	caller := "facility input quoting R0-CONSTITUTION-VERBATIM"
	sys, _ := g.SystemWithIdentitySeam(caller, rings.r1, rings.r2)
	if !strings.HasPrefix(sys, "R0-CONSTITUTION-VERBATIM") {
		t.Fatalf("Ring 0 was not rendered from its owner first: %s", sys)
	}
	if strings.Count(sys, "R0-CONSTITUTION-VERBATIM") != 2 {
		t.Fatal("the caller's quotation must not masquerade as the authoritative Ring 0 rendering")
	}
}

func TestGateHonorsFoldedDispositions(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "# Floor"})
	full := strings.Repeat("working truth detail. ", 40)
	rm.SetSection(ring.Ring3, "working_truth", full)

	c := newTestComposer(rm, 32000)
	c.SetName("GateFoldTest")
	p, err := c.ComposeFolded("sub-goal state", 0)
	if err != nil {
		t.Fatal(err)
	}

	out := p.Text
	if strings.Contains(out, full) {
		t.Fatal("gate re-injected the FULL folded ring — the fold was undone (F1)")
	}
	if !strings.Contains(out, "[summary") {
		t.Fatal("the summary must survive the gate, still marked as a summary")
	}
	if !strings.Contains(out, "# Constitution") || !strings.Contains(out, "# Floor") {
		t.Fatal("protected rings must be present through the gate")
	}
}
