package prompt

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

func accordionRings(t *testing.T) *ring.Manager {
	t.Helper()
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Constitution\nHonesty.")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "# Floor\nProtect the substrate."})
	rm.SetSection(ring.Ring3, "working_truth", "The anchor held through the outage.")
	return rm
}

// The seam contract (Accordion, kept from the C program's one proven
// cache test shape): two composes differing ONLY in runtime truth must
// produce a BYTE-IDENTICAL stable prefix — that is what provider prefix
// caching hits. No semantic reordering happened for it: the protected
// material already led the prompt.
func TestStablePrefixByteIdenticalAcrossTurns(t *testing.T) {
	rm := accordionRings(t)
	c := newTestComposer(rm, 32000)
	c.SetName("SeamTest")

	p1, err := c.Compose("turn one: drafting the reply", 0)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.Compose("turn two: completely different work state", 0)
	if err != nil {
		t.Fatal(err)
	}

	if p1.StableLen == 0 || p2.StableLen == 0 {
		t.Fatal("stable prefix must exist")
	}
	if p1.Text[:p1.StableLen] != p2.Text[:p2.StableLen] {
		t.Fatal("stable prefix must be byte-identical across turns with unchanged ring state")
	}
	if p1.Text[p1.StableLen:] == p2.Text[p2.StableLen:] {
		t.Fatal("runtime truth differed — the volatile suffix must differ")
	}
	// The seam sits after tools: stable side carries identity + tools,
	// volatile side carries the working state.
	if !strings.Contains(p1.Text[:p1.StableLen], "You are SeamTest") {
		t.Fatal("opening belongs to the stable prefix")
	}
	if strings.Contains(p1.Text[:p1.StableLen], "turn one: drafting") {
		t.Fatal("per-turn work state leaked into the stable prefix")
	}
}

// The Accordion ladder: under pressure, elastic sections FOLD to
// deterministic digests carrying a route (fold-before-drop); protected
// material never folds; under more pressure the fold becomes a DECLARED
// omission with the same route. One dropper, one receipt. Nothing
// persists between composes.
func TestAccordionFoldLadder(t *testing.T) {
	rm := accordionRings(t)
	big := strings.Repeat("Working truth accumulates in long sentences about the day. ", 300)
	rm.SetSection(ring.Ring3, "working_truth", big)

	// The budget forces folding but fits the protected core: 1,100, not
	// a round 1,000, because sections are counted as the wire carries
	// them (tokenestimate.EstimateWire) and this fixture's protected core
	// plus its omission notes sit just above 1,000 on that count.
	c := newTestComposer(rm, 1100)
	c.SetName("FoldTest")
	p, err := c.Compose(strings.Repeat("live work state line. ", 100), 0)
	if err != nil {
		t.Fatal(err)
	}

	var foldedRing3 bool
	for _, s := range p.Sections {
		if s.Source == "ring3" && s.Folded {
			foldedRing3 = true
		}
		if !s.Elastic && s.Folded {
			t.Fatalf("identity section %s folded — never", s.Name)
		}
	}
	if !foldedRing3 && !strings.Contains(p.Text, "folded under context pressure") {
		// ring3 either folded (marker present) or was omitted declared —
		// both must leave a visible trace with a route.
		if !strings.Contains(p.Text, "Not Shown (context budget)") {
			t.Fatal("elastic content vanished with neither fold marker nor declared omission")
		}
	}
	if !strings.Contains(p.Text, "You are FoldTest") {
		t.Fatal("the identity core must survive any budget")
	}
	if !strings.Contains(p.Text, budgetRoute) {
		t.Fatal("every fold/omission carries a route back")
	}

	// The enforcement must actually WORK: post-ladder estimate fits the
	// budget (protected core is small here, so fitting is achievable).
	if p.TokenEstimate > 1100 {
		t.Fatalf("after the fold ladder the prompt must fit: %d tokens > 1100 budget", p.TokenEstimate)
	}

	// Determinism: same inputs, same bytes — fold state never persists.
	p2, err := c.Compose(strings.Repeat("live work state line. ", 100), 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Text != p2.Text {
		t.Fatal("folding must be deterministic and stateless across composes")
	}
}

func TestComposerLiveBudgetChange(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("long working truth. ", 500))
	c := newTestComposer(rm, 100000)
	c.SetName("LiveBudget")
	before, err := c.Compose(strings.Repeat("work state. ", 200), 0)
	if err != nil {
		t.Fatal(err)
	}
	c.SetMaxTokens(1100) // the wire count, as above
	after, err := c.Compose(strings.Repeat("work state. ", 200), 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.TokenEstimate > 1100 {
		t.Fatalf("live provider budget was not applied: %d > 1100", after.TokenEstimate)
	}
	if after.Text == before.Text {
		t.Fatal("composition did not change after the live model budget narrowed")
	}
}

func TestPressureFoldNeverExpandsSection(t *testing.T) {
	short := "brief state"
	long := strings.Repeat("working truth ", 200)
	sections := []Section{
		{Content: "identity"},
		{Content: short, Source: "ring4", Elastic: true},
		{Content: long, Source: "ring3", Elastic: true},
	}
	want := append([]Section(nil), sections...)
	want[2].Content = summarize("ring3", long, budgetRoute)
	want[2].Folded = true

	got, _ := newBudgetEnforcer((&budgetEnforcer{}).budgetTokens(want, nil), nil).FoldAndTrim(sections)
	if got[1].Content != short || got[1].Folded {
		t.Fatalf("short section expanded under pressure: %+v", got[1])
	}

	forced := append([]Section(nil), sections...)
	newBudgetEnforcer(1, nil).ForceFoldElastic(forced)
	if forced[1].Content != short || forced[1].Folded {
		t.Fatalf("short section expanded under forced folding: %+v", forced[1])
	}
}

func TestComposerBudgetIncludesReserveAndOmissionReceipt(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("working truth. ", 500))

	c := newTestComposer(rm, 1400)
	p, err := c.Compose(strings.Repeat("live state. ", 300), 200)
	if err != nil {
		t.Fatal(err)
	}
	if p.TokenEstimate+200 > 1400 {
		t.Fatalf("composed request uses %d prompt + 200 reserved tokens", p.TokenEstimate)
	}
}

// The old head-truncation had to count runes to avoid splitting one.
// The unit-dropper cannot reach that failure at all: it drops whole
// paragraphs and never cuts inside text. Pinned with multi-byte content
// that actually exercises the paragraph path.

// The old head-truncation had to count runes to avoid splitting
// one. The unit-dropper cannot reach that failure at all: it drops
// whole paragraphs and never cuts inside text. Pinned with
// multi-byte content that actually exercises the paragraph path.
func TestSummaryPreservesUTF8(t *testing.T) {
	got := summarize("ring3", strings.TrimSpace(strings.Repeat("界界界界界。\n\n", 10)), budgetRoute)
	if !utf8.ValidString(got) {
		t.Fatal("summary split a UTF-8 rune")
	}
	if !strings.Contains(got, "5 of 10 kept") {
		t.Fatalf("multi-byte paragraphs must drop as whole units: %q", got)
	}
}

func TestOpeningIsALifeNotForensics(t *testing.T) {
	rm := accordionRings(t)
	c := newTestComposer(rm, 0)
	c.SetName("LifeTest")
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	opening := p.Sections[0].Content
	for _, forensic := range []string{"post-quantum", "stateless forward passes", "immutable ledger"} {
		if strings.Contains(opening, forensic) {
			t.Fatalf("opening carries substrate forensics %q — the machinery is recall-reachable, not first-breath", forensic)
		}
	}
	if !strings.Contains(opening, "recall reaches all of it") {
		t.Fatal("the route to the record must remain declared")
	}
}

func TestComposeFoldedKeepsIdentityWholeFoldsWorkingTruth(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("long working truth. ", 50))
	c := newTestComposer(rm, 32000)
	c.SetName("SubTest")
	var evidence []rows.Ring2Evidence
	for i := 0; i < 3; i++ {
		evidence = append(evidence, rows.Ring2Evidence{
			ID: fmt.Sprintf("exp_%d", i), EdgeType: "SUPPORTS", Content: "the anchor held through the outage", Provenance: "operator",
		})
	}
	c.SetIdentitySource(ring2HeavyIdentity{[]rows.Ring2Belief{{ID: "b_ring2", Statement: "Testing reveals truth", Evidence: evidence}}})

	full, err := c.Compose("goal state", 0)
	if err != nil {
		t.Fatal(err)
	}
	folded, err := c.ComposeFolded("goal state", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Identity whole: stable prefixes byte-identical.
	if full.Text[:full.StableLen] != folded.Text[:folded.StableLen] {
		t.Fatal("the stable prefix (constitutional self + who-they-are + tools) must be IDENTICAL — that is what cache-shares with the parent")
	}
	// Working truth folded with a route.
	var r3Folded bool
	for _, s := range folded.Sections {
		if s.Source == "ring3" && s.Folded {
			r3Folded = true
		}
		if (!s.Elastic || s.Source == "ring2") && s.Folded {
			t.Fatalf("identity section %s folded in sub-agent context — never", s.Name)
		}
	}
	// Who they have become, whole, and inside what the cache shares: every
	// evidence line, and no word of a pressure that was not there.
	if stable := folded.Text[:folded.StableLen]; strings.Count(stable, "the anchor held through the outage via SUPPORTS") != 3 {
		t.Fatalf("Ring 2 must reach a sub-agent whole, in the stable prefix:\n%s", stable)
	}
	if strings.Contains(folded.Text, "evidence line(s) elided") {
		t.Fatalf("a sub-agent's Ring 2 was folded with no pressure:\n%s", folded.Text)
	}
	if !r3Folded {
		t.Fatal("working truth must fold in the sub-agent default context")
	}
	// Its recovery route, and the way to carry it whole: the spawn's own
	// context=full. No pressure folded it, so no word asks the operator for
	// room (row 9.3).
	if !strings.Contains(folded.Text, "reaches what was surfaced and recorded") || !strings.Contains(folded.Text, "; "+subagentRoom+"]") {
		t.Fatalf("folded working truth must carry its recovery route and close on the sub-agent's way to carry it whole:\n%s", folded.Text)
	}
	if strings.Contains(folded.Text, budgetRoute) {
		t.Fatalf("a sub-agent's default fold asks the operator to raise a prompt budget that did not fold it:\n%s", folded.Text)
	}
	if len(folded.Text) >= len(full.Text) {
		t.Fatal("folded context must actually be smaller")
	}
}

type ring2HeavyIdentity struct{ beliefs []rows.Ring2Belief }

func (s ring2HeavyIdentity) PromptIdentity() (rows.PromptIdentity, error) {
	return rows.PromptIdentity{Charter: "charter text", HasOperatorRelationship: true, Ring2: s.beliefs}, nil
}

// Composed end to end: an impossible budget folds Ring 2 to its
// beliefs and stops there. The section is never listed under "Not
// Shown", and it carries its own name there or anywhere else — it used
// to share the opening's name "Identity", so the one receipt that
// named its omission could not be told from the opening.
func TestComposedRing2SurvivesAnImpossibleBudget(t *testing.T) {
	rm := accordionRings(t)
	var beliefs []rows.Ring2Belief
	for i := 0; i < 40; i++ {
		beliefs = append(beliefs, rows.Ring2Belief{
			ID: fmt.Sprintf("b%d", i), Statement: "constraint internalized supports freedom",
			Evidence: []rows.Ring2Evidence{{ID: "e", EdgeType: "SUPPORTS", Content: strings.Repeat("evidence ", 20), Provenance: "operator"}},
		})
	}
	c := New(rm, 300)
	c.SetName("R2")
	c.SetIdentitySource(ring2HeavyIdentity{beliefs})
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "# Who You Have Become") || !strings.Contains(p.Text, "constraint internalized supports freedom") {
		t.Fatalf("Ring 2 did not survive the budget:\n%s", p.Text)
	}
	if strings.Contains(p.Text, "- Identity — ") || strings.Contains(p.Text, "- Who You Have Become — ") {
		t.Fatalf("Ring 2 was declared omitted:\n%s", p.Text)
	}
	for _, s := range p.Sections {
		if s.Source == "ring2" && s.Name == "Identity" {
			t.Fatal("Ring 2's section shares the opening's name; its receipts are ambiguous")
		}
	}
}
