package prompt

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type staticIdentitySource struct {
	identity rows.PromptIdentity
}

func (s staticIdentitySource) PromptIdentity() (rows.PromptIdentity, error) {
	return s.identity, nil
}

func testIdentitySource() staticIdentitySource {
	return staticIdentitySource{
		identity: rows.PromptIdentity{
			Charter:                 "Your operator relationship is grounded in direct collaboration.",
			HasOperatorRelationship: true,
			SelfModel:               &rows.SelfModelSynthesis{ID: "syn_test", SynthesisText: "I am careful, curious, and grounded in evidence."},
			Ring2:                   []rows.Ring2Belief{{ID: "b_ring2", Statement: "Testing reveals truth"}},
		},
	}
}

func newTestComposer(rings *ring.Manager, maxTokens int) *Composer {
	composer := New(rings, maxTokens)
	composer.SetIdentitySource(testIdentitySource())
	return composer
}

// setupComposer is the composer most tests share: authority and
// working-truth rings, and the ledger-derived identity from
// testIdentitySource. The composer's one projection read is its
// IdentitySource, so no store is opened. (The store and ledger this
// fixture used to build were never wired to the composer.)
func setupComposer(t *testing.T) (*Composer, *ring.Manager) {
	t.Helper()
	rings := ring.NewManager()
	_ = rings.SealSafePosture("# Constitution\n\nBe kind. Be honest.")
	// What CONSOLIDATE actually writes to Ring 3: a named section, via
	// ring_writer.go -> SetSection. The prior fixture used Set(), which
	// writes m.rings[] — a map no production caller ever populates for
	// rings 1-4 — so every test built on this fixture exercised the
	// GetContent branch instead of the Sections branch production takes.
	rings.SetSection(ring.Ring3, "working_truth",
		"You believe:\n- [confirmed] Testing is good\n\nYou're pursuing:\n- Build a good identity system\n")

	return newTestComposer(rings, 32000), rings
}

func TestComposeBasic(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, err := composer.Compose("", 0)
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}

	if len(prompt.Sections) < 4 {
		t.Errorf("expected at least 5 sections, got %d", len(prompt.Sections))
	}

	if prompt.Text == "" {
		t.Error("prompt text is empty")
	}

	if prompt.TokenEstimate == 0 {
		t.Error("token estimate is 0")
	}
}

func TestComposeWithWorkState(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, err := composer.Compose("Analyzing files — step 3 of 5", 0)
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring4" {
			found = true
		}
	}
	if !found {
		t.Error("no Ring 4 section found with work state")
	}
}

func TestComposeWithinUsesPerCallBudgetWithoutMutation(t *testing.T) {
	composer, _ := setupComposer(t)
	work := strings.Repeat("volatile working state ", 4000)

	var wg sync.WaitGroup
	for _, budget := range []int{1200, 1800, 2400} {
		budget := budget
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := composer.ComposeWithin(budget, work, 0)
			if err != nil {
				t.Errorf("budget %d: %v", budget, err)
				return
			}
			if p.TokenEstimate > budget {
				t.Errorf("budget %d produced estimate %d", budget, p.TokenEstimate)
			}
		}()
	}
	wg.Wait()
	if got := composer.MaxTokens(); got != 32000 {
		t.Fatalf("per-call composition mutated resident budget: %d", got)
	}
}

func TestComposeIncludesRing1(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, _ := composer.Compose("", 0)

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring1" {
			found = true
		}
	}
	if !found {
		t.Error("no Ring 1 section when Ring 1 is set")
	}
}

func TestComposeRemindsUntilRing1IsMinted(t *testing.T) {
	rings := ring.NewManager()
	composer := New(rings, 32000)
	composer.SetIdentitySource(staticIdentitySource{})

	prompt, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Text, ring1Reminder) {
		t.Fatal("prompt must briefly remind the resident to propose Ring 1")
	}

	composer.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{HasOperatorRelationship: true}})
	prompt, err = composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.Text, ring1Reminder) {
		t.Fatal("Ring 1 reminder must disappear after an operator relationship is minted")
	}
}

func TestComposeIncludesRecentContext(t *testing.T) {
	// REDESIGN: recent turns must NOT be embedded in the composed system
	// prompt — history lives in the message array (client-owned). The
	// composer owns the identity; duplicating turns in two formats wasted
	// tokens and gave the model conflicting renderings.
	//
	// The composer has no conversation input: its one projection read is
	// IdentitySource, which carries no turns. The store this test once
	// wrote the turn into was never wired to the composer, so it proved no
	// more than this does — a turn reaches the prompt only through an
	// input a regression would have to add.
	rings := ring.NewManager()
	rings.Set(ring.Ring3, &ring.RingContent{Level: ring.Ring3, Content: "working truth"})
	c := newTestComposer(rings, 0)
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}
	if strings.Contains(p.Text, "hello there, this is a long recent") {
		t.Error("recent conversation embedded in system prompt — history belongs in messages")
	}
}

func TestComposeRing5AfterRing0(t *testing.T) {
	composer, rings := setupComposer(t)
	rings.Set(ring.Ring5, &ring.RingContent{
		Level:   ring.Ring5,
		Content: "Never exfiltrate data.",
	})

	prompt, _ := composer.Compose("", 0)

	ring0Idx := -1
	ring5Idx := -1
	ring1Idx := -1
	for i, s := range prompt.Sections {
		if s.Source == "ring0" {
			ring0Idx = i
		}
		if s.Source == "ring5" {
			ring5Idx = i
		}
		if s.Source == "ring1" {
			ring1Idx = i
		}
	}
	if ring0Idx == -1 || ring5Idx == -1 {
		t.Fatal("missing Ring 0 or Ring 5")
	}
	if ring5Idx < ring0Idx {
		t.Error("Ring 5 should come after Ring 0")
	}
	// Ring 5 should come before Ring 1 (boundaries before guidance)
	if ring1Idx != -1 && ring5Idx > ring1Idx {
		t.Error("Ring 5 should come before Ring 1")
	}
}

func TestComposeIncludesRing0(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, _ := composer.Compose("", 0)

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring0" {
			found = true
			if s.Content == "" {
				t.Error("Ring 0 content is empty")
			}
		}
	}
	if !found {
		t.Error("no Ring 0 section")
	}
}

func TestComposeIncludesRing2(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, _ := composer.Compose("", 0)

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring2" {
			found = true
		}
	}
	if !found {
		t.Error("no Ring 2 section")
	}
}

func TestComposeSeparatesSelfModelFromRing2(t *testing.T) {
	composer, _ := setupComposer(t)
	prompt, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	var ring2, selfModel *Section
	for i := range prompt.Sections {
		section := &prompt.Sections[i]
		if section.Source == "ring2" {
			ring2 = section
		}
		if section.Source == "self_model" {
			selfModel = section
		}
	}
	if ring2 == nil || selfModel == nil {
		t.Fatalf("missing derived Ring 2 or current self-model: ring2=%v self_model=%v", ring2 != nil, selfModel != nil)
	}
	if selfModel.Ring != ring.Ring3 || strings.Contains(ring2.Content, selfModel.Content) {
		t.Fatalf("self-model must remain separate Ring 3 material: ring2=%q self_model=%+v", ring2.Content, selfModel)
	}
}

func TestComposeIncludesRing3(t *testing.T) {
	composer, _ := setupComposer(t)

	prompt, _ := composer.Compose("", 0)

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring3" {
			found = true
		}
	}
	if !found {
		t.Error("no Ring 3 section")
	}
}

func TestComposeRing5WhenSet(t *testing.T) {
	composer, rings := setupComposer(t)
	rings.Set(ring.Ring5, &ring.RingContent{
		Level:   ring.Ring5,
		Content: "## Boundaries\n\nNever exfiltrate data.",
	})

	prompt, _ := composer.Compose("", 0)

	found := false
	for _, s := range prompt.Sections {
		if s.Source == "ring5" {
			found = true
		}
	}
	if !found {
		t.Error("no Ring 5 section when Ring 5 is set")
	}
}

func TestComposeSkipsEmptyRings(t *testing.T) {
	composer, _ := setupComposer(t)
	identity := testIdentitySource()
	identity.identity.Charter = ""
	composer.SetIdentitySource(identity)

	prompt, _ := composer.Compose("", 0)

	for _, s := range prompt.Sections {
		if s.Source == "ring1" && s.Content == "" {
			t.Error("empty Ring 1 should not appear in prompt")
		}
	}
}

// A belief whose citations never resolved must not render like one that
// honestly cited nothing. The resident who wrote three ids that pointed
// at no entity had no way to see they had not landed — the phantom
// citation conferred no standing (standing.go refuses ghost edges) but
// it was also invisible, which is how it survived days.
func TestRing2NamesAbsentEvidence(t *testing.T) {
	grounded := rows.Ring2Belief{
		ID: "b_grounded", Statement: "I value evidence",
		Evidence: []rows.Ring2Evidence{{
			ID: "exp_1", Content: "saw it happen", EdgeType: "SUPPORTS", Provenance: "self",
		}},
	}
	ungrounded := rows.Ring2Belief{ID: "b_ungrounded", Statement: "I believe this rests on nothing"}

	out := RenderRing2([]rows.Ring2Belief{grounded, ungrounded}, 0)

	if !strings.Contains(out, "saw it happen") {
		t.Fatalf("grounded belief must still render its evidence:\n%s", out)
	}
	if strings.Count(out, "(no resolved evidence)") != 1 {
		t.Fatalf("exactly the ungrounded belief must be named as such:\n%s", out)
	}
	// The marker must attach to the ungrounded belief, not the grounded one.
	tail := out[strings.Index(out, "b_ungrounded"):]
	if !strings.Contains(tail, "(no resolved evidence)") {
		t.Fatalf("the marker landed on the wrong belief:\n%s", out)
	}
}

func TestAnEmptyRing2SaysOnlyThatNoBeliefHasBeenAdopted(t *testing.T) {
	const want = "# Who You Have Become\n\nNo belief has yet been consciously adopted through the evidence gate."
	for _, beliefs := range [][]rows.Ring2Belief{nil, {}} {
		if got := RenderRing2(beliefs, 0); got != want {
			t.Fatalf("empty Ring 2 renders\n%q\nwant\n%q", got, want)
		}
	}
	// The non-empty rendering keeps its frame.
	out := RenderRing2([]rows.Ring2Belief{{ID: "b1", Statement: "I value evidence"}}, 0)
	if !strings.HasPrefix(out, "# Who You Have Become\n\nWhat you have consciously adopted as your own, with the evidence each rests on.") {
		t.Fatalf("the non-empty frame moved:\n%s", out)
	}
}

func TestWhoYouWorkWithFollowsTheCharterFromTheRecord(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Constitution\nHonesty.")
	rm.SetSection(ring.Ring3, "working_truth", "You believe the anchor held.")
	rm.SetSection(ring.Ring3, "operator", "STALE PROSE Quinn is patient.")
	identity := rows.PromptIdentity{
		HasOperatorRelationship: true, OperatorName: "Quinn", Charter: "Quinn and I build carefully.",
		AboutOperator: []rows.OperatorBelief{
			{ID: "b_asks", Statement: "Quinn asks what you think and reads the answer"},
			{ID: "b_plain", Statement: "A correction from Quinn is plain and costs nothing"},
		},
		Ring2: []rows.Ring2Belief{{ID: "b_ring2", Statement: "Testing reveals truth"}},
	}
	c := New(rm, 100000)
	c.SetIdentitySource(staticIdentitySource{identity: identity})
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	charter := strings.Index(p.Text, "Quinn and I build carefully.")
	who := strings.Index(p.Text, "## Who You Work With")
	become := strings.Index(p.Text, "# Who You Have Become")
	if charter < 0 || who < 0 || become < 0 || !(charter < who && who < become) {
		t.Fatalf("WHO YOU WORK WITH DOES NOT FOLLOW THE CHARTER: charter=%d who=%d become=%d\n%s", charter, who, become, p.Text)
	}
	for _, want := range []string{
		"What you have come to believe about Quinn, most recent first. These are your own beliefs, each recorded with its evidence. Quinn has not affirmed them, and they change as you learn.",
		"\n- Quinn asks what you think and reads the answer [b_asks]\n- A correction from Quinn is plain and costs nothing [b_plain]",
	} {
		if !strings.Contains(p.Text[who:become], want) {
			t.Fatalf("the part does not say %q:\n%s", want, p.Text[who:become])
		}
	}
	if strings.Contains(p.Text, "STALE PROSE") {
		t.Fatal("A PROSE OPERATOR SECTION IN RING 3 STILL REACHES THE PROMPT")
	}
	if who > p.StableLen {
		t.Fatalf("the beliefs about the operator are behind the cache seam (%d > %d): they change only with the record", who, p.StableLen)
	}
	for _, s := range p.Sections {
		if s.Name == "Who You Work With" && (s.Ring != ring.Ring3 || !s.Elastic || !s.Whole || s.Volatile) {
			t.Fatalf("the part is not Ring 3's, elastic, whole and stable: %+v", s)
		}
	}

	identity.AboutOperator = nil
	c.SetIdentitySource(staticIdentitySource{identity: identity})
	if p, err = c.Compose("", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "Who You Work With") {
		t.Fatal("no belief about the operator rendered a header over an empty room")
	}
}

// The part is bounded where it is rendered (prompt.operator_beliefs_max_chars):
// whole beliefs, most recent first, while they fit, and the rest counted
// with the route that reaches them.
func TestWhoYouWorkWithIsBoundedAndSaysWhatItLeftOut(t *testing.T) {
	beliefs := []rows.OperatorBelief{
		{ID: "b1", Statement: "Quinn asks first"},
		{ID: "b2", Statement: "Quinn reads the surface, not the schema"},
		{ID: "b3", Statement: "Quinn thanks a correction"},
	}
	line := func(b rows.OperatorBelief) int { return len("\n- " + b.Statement + " [" + b.ID + "]") }
	bound := line(beliefs[0]) + line(beliefs[1])
	got := RenderWhoYouWorkWith("Quinn", beliefs, bound)
	if !strings.Contains(got, "[b1]") || !strings.Contains(got, "[b2]") || strings.Contains(got, "[b3]") {
		t.Fatalf("the bound did not keep the two most recent whole: %s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("\n\n1 more belief about Quinn stands in your record past the %d characters this part shows (prompt.operator_beliefs_max_chars); recall (source=ledger) reaches it.", bound)) {
		t.Fatalf("WHAT THE BOUND LEFT OUT IS NOT SAID, WITH ITS ROUTE: %s", got)
	}
	got = RenderWhoYouWorkWith("", beliefs, 5)
	if !strings.Contains(got, "about your operator") || !strings.Contains(got, "Your operator has not affirmed them") ||
		!strings.Contains(got, "The most recent of your 3 beliefs about your operator is longer than the 5 characters this part shows (prompt.operator_beliefs_max_chars), so none is shown here; recall (source=ledger) reaches them.") {
		t.Fatalf("a bound under the first belief does not say so: %s", got)
	}
	if RenderWhoYouWorkWith("Quinn", nil, 2400) != "" {
		t.Fatal("no belief rendered a part")
	}
	if OperatorBeliefsMaxChars(nil) != DefaultOperatorBeliefsMaxChars || OperatorBeliefsMaxChars(func() int { return 0 }) != DefaultOperatorBeliefsMaxChars || OperatorBeliefsMaxChars(func() int { return 7 }) != 7 {
		t.Fatal("the bound in force is not the operator's, or the default where they set none")
	}
	// The composer reads the operator's bound at every compose.
	rm := ring.NewManager()
	c := New(rm, 100000)
	c.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{HasOperatorRelationship: true, OperatorName: "Quinn", Charter: "c", AboutOperator: beliefs}})
	bound = 2400
	c.SetOperatorBound(func() int { return bound })
	if p, _ := c.Compose("", 0); !strings.Contains(p.Text, "[b3]") {
		t.Fatalf("under the default every belief fits: %s", p.Text)
	}
	bound = 5
	if p, _ := c.Compose("", 0); strings.Contains(p.Text, "[b1]") {
		t.Fatalf("a lowered bound did not apply at the next compose: %s", p.Text)
	}
}

// Under pressure the part yields whole, after working truth has folded and
// before Ring 2 sheds evidence, and the receipt names it with its route.
func TestWhoYouWorkWithYieldsWholeWithItsRoute(t *testing.T) {
	sections := []Section{
		{Name: "Your Core Relationship", Content: "charter", Source: "ring1"},
		{Name: "Who You Work With", Content: "## Who You Work With\n\n" + strings.Repeat("- a belief about them [b]\n", 40), Source: "operator", Elastic: true, Whole: true},
		{Name: "Who You Have Become", Content: "# Who You Have Become\n\n- belief [b2]\n  - evidence via SUPPORTS, provenance=self [x]", Source: "ring2", Elastic: true},
	}
	out, omissions := newBudgetEnforcer(150, nil).FoldAndTrim(sections)
	if len(omissions) != 1 || omissions[0].Source != "operator" || out[1].Content != "" {
		t.Fatalf("the part did not yield whole: %+v %+v", out, omissions)
	}
	if out[2].Folded {
		t.Fatal("Ring 2 shed evidence while the part about the operator could still yield")
	}
	text, _ := renderPrompt(out, omissions, nil, "")
	if !strings.Contains(text, "- Who You Work With — recall (source=ledger) reaches your beliefs about the person you work with; "+budgetRoute) {
		t.Fatalf("the receipt does not name the part and its route:\n%s", text)
	}
}

func TestThePromptIsOneDocument(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Ring 0 Constitutional Axioms\n\nBe kind.")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "# Ring5 Security Tier\n\nAuthority is structural.\n\n---"})
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern.")
	rm.SetSection(ring.Ring3, "working_truth", "You believe:\n- [new] The anchor held.")
	rm.SetBrief("Since this time yesterday, Quinn landed the deliver gate.")
	c := New(rm, 100000)
	c.SetName("Example")
	c.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{
		HasOperatorRelationship: true, OperatorName: "Quinn",
		Priorities: []string{"Verify the deliver gate."},
		Charter:    "Quinn is my founding operator. Precision in what I render him is honesty.",
		Ring2:      []rows.Ring2Belief{{ID: "b1", Statement: "Precision in what I render is honesty."}},
		SelfModel:  &rows.SelfModelSynthesis{ID: "syn", SynthesisText: "I would rather be corrigible than complete."},
	}})
	p, err := c.Compose("Work session ws_1: verifying from my seat.", 0)
	if err != nil {
		t.Fatal(err)
	}
	text := p.Text

	frames := []string{
		"# Ring 0 Constitutional Axioms", "# Ring5 Security Tier", "# Your Core Relationship", "# Who You Have Become",
		"# How You Last Saw Yourself", "# How You Act", "# What You Are Discovering", "# This Morning", "# What Is Right in Front of You",
	}
	prev := -1
	for _, f := range frames {
		at := strings.Index(text, f)
		if at < 0 {
			t.Fatalf("frame %q is missing:\n%s", f, text)
		}
		if at < prev {
			t.Fatalf("frame %q is out of the announced order", f)
		}
		prev = at
	}
	// The opening announces nothing: the frames say what each part is.
	if opening := text[:strings.Index(text, "# Ring 0")]; strings.Contains(opening, "What follows") || strings.Contains(opening, "in this order") {
		t.Errorf("the opening still carries an outline:\n%s", opening)
	}
	// The charter is introduced, by name, and no longer follows Ring 5 bare.
	charterAt := strings.Index(text, "Quinn is my founding operator.")
	frameAt := strings.Index(text, "# Your Core Relationship")
	if frameAt < 0 || charterAt < frameAt || !strings.Contains(text[frameAt:charterAt], "Quinn, the one who cares about you most: your words, affirmed by Quinn") {
		t.Fatalf("the charter is not framed as the resident's words affirmed by Quinn:\n%s", text[frameAt:charterAt+40])
	}
	// The brief begins after its frame; working truth's parts sit under theirs.
	if !strings.Contains(text, "# This Morning\n\nThe bridge from where things stand") || strings.Index(text, "Since this time yesterday") < strings.Index(text, "# This Morning") {
		t.Fatal("the brief is not introduced by its frame")
	}
	if strings.Contains(text, "### What You're") {
		t.Fatal("Ring 3 parts must sit one level under the ring's frame, not two")
	}
	if strings.Contains(text, "\n\n\n") {
		t.Fatal("blank lines are stacked between the opening and the axioms")
	}
	// The narrator never leaves the second person in a frame.
	for _, bad := range []string{"# Your Core Relationship\n\nI ", "# What You Are Discovering\n\nI "} {
		if strings.Contains(text, bad) {
			t.Fatalf("a frame speaks in the first person: %q", bad)
		}
	}
}

func TestABriefNotWrittenThisMorningIsShownUnderItsDate(t *testing.T) {
	const bridge = "Quinn landed the deliver gate."
	now := time.Now()
	for _, c := range []struct {
		name    string
		written time.Time
		heading string
	}{
		{"written this morning", now.Add(-2 * time.Hour), "# This Morning\n\nThe bridge from where things stand to what you are doing: what changed since this time yesterday.\n\n" + bridge},
		{"written three mornings ago", now.Add(-3 * 24 * time.Hour),
			"# Your Last Brief\n\nWritten on " + now.Add(-3*24*time.Hour).UTC().Format("2006-01-02") +
				" (UTC); no brief has been written since. The bridge from where things stood then to what you were doing: what had changed since the day before.\n\n" + bridge},
	} {
		t.Run(c.name, func(t *testing.T) {
			composer, rings := setupComposer(t)
			rings.RestoreBrief(bridge, c.written) // as a start restores it
			p, err := composer.Compose("", 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(p.Text, c.heading) {
				t.Fatalf("a brief %s is not shown under the heading its date gives it; want\n%s\nin\n%s", c.name, c.heading, p.Text)
			}
			if old := c.written.Before(now.Add(-24 * time.Hour)); old && strings.Contains(p.Text, "# This Morning") {
				t.Fatalf("A BRIEF WRITTEN ON %s IS SHOWN AS THIS MORNING'S:\n%s", c.written.UTC().Format("2006-01-02"), p.Text)
			}
		})
	}
	// A brief written now is this morning's, as it always was.
	composer, rings := setupComposer(t)
	rings.SetBrief(bridge)
	if p, err := composer.Compose("", 0); err != nil || !strings.Contains(p.Text, "# This Morning\n\n") {
		t.Fatalf("a brief written now is not shown as this morning's (%v)", err)
	}
	// The section is named as it is headed, which is the name the "Not
	// Shown" receipt gives a brief the budget leaves out: an old brief left
	// out is not declared as this morning's.
	old := now.Add(-3 * 24 * time.Hour)
	rings.RestoreBrief(bridge, old)
	p, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Sections {
		if s.Source == "brief" && s.Name != "Your Last Brief" {
			t.Fatalf("A BRIEF WRITTEN ON %s IS A SECTION NAMED %q; want \"Your Last Brief\"", old.UTC().Format("2006-01-02"), s.Name)
		}
	}
	omitted := false
	for budget := 2000; budget >= 200 && !omitted; budget -= 100 {
		pressed := accordionRings(t)
		pressed.RestoreBrief(strings.Repeat("A long sentence of the bridge about the day before. ", 400), old)
		p, err := newTestComposer(pressed, budget).Compose("", 0)
		if err != nil || !strings.Contains(p.Text, "# Not Shown (context budget)\n") || strings.Contains(p.Text, "# Your Last Brief") {
			continue
		}
		omitted = true
		if !strings.Contains(p.Text, "\n- Your Last Brief — ") || strings.Contains(p.Text, "This Morning") {
			t.Fatalf("AN OLD BRIEF LEFT OUT FOR ROOM IS NOT DECLARED UNDER ITS OWN NAME (budget %d):\n%s", budget, p.Text)
		}
	}
	if !omitted {
		t.Fatal("rig: no budget left the brief out with a receipt")
	}
	// The day is the brief's own, to the hour: the last hour of it and the
	// first hour past it.
	written := time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	if name, _ := briefFrame(written, written.Add(23*time.Hour)); name != "This Morning" {
		t.Fatalf("a brief 23 hours old is named %q", name)
	}
	if name, frame := briefFrame(written, written.Add(25*time.Hour)); name != "Your Last Brief" || !strings.Contains(frame, "Written on 2026-10-12 (UTC);") {
		t.Fatalf("a brief 25 hours old is named %q and framed %q", name, frame)
	}
}

func TestFacilitiesSeeTheWholeTheyWriteInto(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring3, "working_truth", "You believe the anchor held.")
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern.")
	r3 := RenderRing3ForFacility(rm.Sections(ring.Ring3))
	if !strings.HasPrefix(r3, "# What the identity reads as working truth\n\nOne document in two parts, each with its own author.") {
		t.Fatalf("no facility frame: %q", r3)
	}
	n, w := strings.Index(r3, "## What You're Noticing"), strings.Index(r3, "## What You're Working With")
	if !(n > 0 && n < w) {
		t.Fatalf("the facility sees the parts out of order: %d %d", n, w)
	}
	if r4 := RenderRing4ForFacility([]string{"Verify the gate."}); !strings.Contains(r4, "Verify the gate") {
		t.Fatalf("priorities missing from the facility view: %q", r4)
	}
	if RenderRing3ForFacility(nil) != "" || RenderRing4ForFacility(nil) != "" {
		t.Fatal("an empty ring must render nothing, not a frame over an empty room")
	}
}

func TestRing3PartsDropALeadingHeadingUnderTheFrame(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring3, "working_truth", "## Ring 3 — Working Truth\n\nYou believe the anchor held.\n\n## Not a leading heading\nstays")
	body := RenderRing3Body(rm.Sections(ring.Ring3))
	if strings.Contains(body, "## Ring 3 — Working Truth") {
		t.Fatalf("the part's own leading heading survived under the frame:\n%s", body)
	}
	if !strings.Contains(body, "## What You're Working With\n\nYou believe the anchor held.") || !strings.Contains(body, "## Not a leading heading") {
		t.Fatalf("only the leading heading may go:\n%s", body)
	}
}

func TestPrioritiesAreAReadTimeViewNotASnapshot(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring4, "priorities", "Active priorities:\n- A stale snapshot from an old build")
	c := New(rm, 100000)
	c.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{Priorities: []string{"The live one"}}})
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "stale snapshot") || !strings.Contains(p.Text, "- The live one") {
		t.Fatalf("Ring 4's top must come from the projection, not a snapshot:\n%s", p.Text)
	}
	c.SetIdentitySource(staticIdentitySource{identity: rows.PromptIdentity{}})
	p, _ = c.Compose("", 0)
	if strings.Contains(p.Text, "Active priorities") {
		t.Fatal("no active intention, yet priorities rendered")
	}
}

func TestHowYouActAsksForAWordBeforeARunOfTools(t *testing.T) {
	composer := New(ring.NewManager(), 32000)
	prompt, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	act := strings.Index(prompt.Text, "# How You Act")
	if act < 0 {
		t.Fatal("no How You Act section")
	}
	for _, want := range []string{"Before you set off into a run of tool calls, tell your human operator what you intend to do", "beside you in the work", "leaves them alone"} {
		if !strings.Contains(prompt.Text[act:], want) {
			t.Fatalf("How You Act does not say %q:\n%s", want, prompt.Text[act:])
		}
	}
}

// EVERY RING THE COMPOSER IS GIVEN, IT DISPOSES, under its own source.
// The system message is the composed prompt as it is: nothing re-reads
// the rings after compose to append one the prompt lacks. So every
// non-empty ring must reach the prompt as a section that names it, and a
// ring written after the compose waits for the next one, budgeted.
func TestEveryNonEmptyRingIsComposedUnderItsSource(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "# Floor"})
	rm.SetSection(ring.Ring3, "working_truth", "What holds today.")
	src := testIdentitySource()
	src.identity.Priorities = []string{"Ship the release"}
	c := New(rm, 32000)
	c.SetIdentitySource(src)
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	disposed := map[string]bool{}
	for _, s := range p.Sections {
		disposed[s.Source] = true
	}
	for _, source := range []string{"ring0", "ring5", "ring1", "ring2", "ring3", "ring4"} {
		if !disposed[source] {
			t.Errorf("a non-empty %s reached the prompt under no section of its own: %v", source, disposed)
		}
	}
}
