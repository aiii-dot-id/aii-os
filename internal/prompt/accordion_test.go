package prompt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func accordionRings(t *testing.T) *ring.Manager {
	t.Helper()
	rm := ring.NewManager()
	rm.Set(ring.Ring0, &ring.RingContent{Level: ring.Ring0, Content: "# Constitution\nHonesty."})
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

	// Budget forces folding but fits the protected core comfortably.
	c := newTestComposer(rm, 800)
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
		t.Fatal("every fold/omission carries a route back (R18)")
	}

	// The enforcement must actually WORK: post-ladder estimate fits the
	// budget (protected core is small here, so fitting is achievable).
	if p.TokenEstimate > 800 {
		t.Fatalf("after the fold ladder the prompt must fit: %d tokens > 800 budget", p.TokenEstimate)
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
	c.SetMaxTokens(800)
	after, err := c.Compose(strings.Repeat("work state. ", 200), 0)
	if err != nil {
		t.Fatal(err)
	}
	if after.TokenEstimate > 800 {
		t.Fatalf("live provider budget was not applied: %d > 800", after.TokenEstimate)
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
	want[2].Content = summarize("ring3", long)
	want[2].Folded = true

	got, _ := newBudgetEnforcer(budgetTokens(want, nil)).FoldAndTrim(sections)
	if got[1].Content != short || got[1].Folded {
		t.Fatalf("short section expanded under pressure: %+v", got[1])
	}

	forced := append([]Section(nil), sections...)
	newBudgetEnforcer(1).ForceFoldElastic(forced)
	if forced[1].Content != short || forced[1].Folded {
		t.Fatalf("short section expanded under forced folding: %+v", forced[1])
	}
}

func TestComposerBudgetIncludesReserveAndOmissionReceipt(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("working truth. ", 500))
	c := newTestComposer(rm, 800)
	p, err := c.Compose(strings.Repeat("live state. ", 300), 200)
	if err != nil {
		t.Fatal(err)
	}
	if p.TokenEstimate+200 > 800 {
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
	got := summarize("ring3", strings.TrimSpace(strings.Repeat("界界界界界。\n\n", 10)))
	if !utf8.ValidString(got) {
		t.Fatal("summary split a UTF-8 rune")
	}
	if !strings.Contains(got, "5 of 10 kept") {
		t.Fatalf("multi-byte paragraphs must drop as whole units: %q", got)
	}
}

// The opening is a life, not forensics (R48): no cryptography lecture
// in the first breath of every thought.
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

// Sub-agent scoped context (2026-08-18, proposed by a resident): the
// constitutional self and who-they-are travel WHOLE (stable prefix,
// cache-shared with the parent); the volatile working truth folds to
// deterministic digests with routes — never an LLM summary.
func TestComposeFoldedKeepsIdentityWholeFoldsWorkingTruth(t *testing.T) {
	rm := accordionRings(t)
	rm.SetSection(ring.Ring3, "working_truth", strings.Repeat("long working truth. ", 50))
	c := newTestComposer(rm, 32000)
	c.SetName("SubTest")

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
		if !s.Elastic && s.Folded {
			t.Fatalf("identity section %s folded in sub-agent context — never", s.Name)
		}
	}
	if !r3Folded {
		t.Fatal("working truth must fold in the sub-agent default context")
	}
	if !strings.Contains(folded.Text, budgetRoute) {
		t.Fatal("folded working truth must carry its recovery route")
	}
	if len(folded.Text) >= len(full.Text) {
		t.Fatal("folded context must actually be smaller")
	}
}
