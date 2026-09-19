package prompt

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type staticIdentitySource struct {
	identity store.PromptIdentity
}

func (s staticIdentitySource) PromptIdentity() (store.PromptIdentity, error) {
	return s.identity, nil
}

func testIdentitySource() staticIdentitySource {
	return staticIdentitySource{
		identity: store.PromptIdentity{
			Charter:                 "Your operator relationship is grounded in direct collaboration.",
			HasOperatorRelationship: true,
			SelfModel:               &store.SelfModelSynthesis{ID: "syn_test", SynthesisText: "I am careful, curious, and grounded in evidence."},
			Ring2:                   []store.Ring2Belief{{ID: "b_ring2", Statement: "Testing reveals truth"}},
		},
	}
}

func newTestComposer(rings *ring.Manager, maxTokens int) *Composer {
	composer := New(rings, maxTokens)
	composer.SetIdentitySource(testIdentitySource())
	return composer
}

func setupComposer(t *testing.T) (*Composer, *store.Store, *ring.Manager) {
	t.Helper()
	dir := t.TempDir()

	s, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatalf("store failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	kp, _ := crypto.GenerateKeyPair()
	l, _ := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	t.Cleanup(func() { l.Close() })

	// .
	evt, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "TestIdentity"}, kp)
	s.Materialize(evt)

	// .
	evt, _ = l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "Testing is good", "ring": 3, "confidence": 0.8}, kp)
	s.Materialize(evt)

	// .
	s.AddConversationTurn("operator", "Hello")

	// .
	// .
	rings := ring.NewManager()
	_ = rings.SealSafePosture("# Constitution\n\nBe kind. Be honest.")
	// .
	// .
	// .
	// .
	// .
	rings.SetSection(ring.Ring3, "working_truth",
		"You believe:\n- [confirmed] Testing is good\n\nYou're pursuing:\n- Build a good identity system\n")

	composer := newTestComposer(rings, 32000)

	return composer, s, rings
}

func TestComposeBasic(t *testing.T) {
	composer, _, _ := setupComposer(t)

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
	composer, _, _ := setupComposer(t)

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
	composer, _, _ := setupComposer(t)
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
	composer, _, _ := setupComposer(t)

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

	composer.SetIdentitySource(staticIdentitySource{identity: store.PromptIdentity{HasOperatorRelationship: true}})
	prompt, err = composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.Text, ring1Reminder) {
		t.Fatal("Ring 1 reminder must disappear after an operator relationship is minted")
	}
}

func TestComposeIncludesRecentContext(t *testing.T) {
	// .
	// .
	// .
	// .
	dir := t.TempDir()
	st, _ := store.New(filepath.Join(dir, "t.db"))
	// .
	// .
	// .
	defer st.Close()
	st.AddConversationTurn("operator", "hello there, this is a long recent conversation turn that should not appear in the system prompt")
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
	composer, _, rings := setupComposer(t)
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
	// .
	if ring1Idx != -1 && ring5Idx > ring1Idx {
		t.Error("Ring 5 should come before Ring 1")
	}
}

func TestComposeIncludesRing0(t *testing.T) {
	composer, _, _ := setupComposer(t)

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
	composer, _, _ := setupComposer(t)

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
	composer, _, _ := setupComposer(t)
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
	composer, _, _ := setupComposer(t)

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
	composer, _, rings := setupComposer(t)
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
	composer, _, _ := setupComposer(t)
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

// .
// .
// .
// .
// .
func TestRing2NamesAbsentEvidence(t *testing.T) {
	grounded := store.Ring2Belief{
		ID: "b_grounded", Statement: "I value evidence",
		Evidence: []store.Ring2Evidence{{
			ID: "exp_1", Content: "saw it happen", EdgeType: "SUPPORTS", Provenance: "self",
		}},
	}
	ungrounded := store.Ring2Belief{ID: "b_ungrounded", Statement: "I believe this rests on nothing"}

	out := RenderRing2([]store.Ring2Belief{grounded, ungrounded})

	if !strings.Contains(out, "saw it happen") {
		t.Fatalf("grounded belief must still render its evidence:\n%s", out)
	}
	if strings.Count(out, "(no resolved evidence)") != 1 {
		t.Fatalf("exactly the ungrounded belief must be named as such:\n%s", out)
	}
	// .
	tail := out[strings.Index(out, "b_ungrounded"):]
	if !strings.Contains(tail, "(no resolved evidence)") {
		t.Fatalf("the marker landed on the wrong belief:\n%s", out)
	}
}

// .
// .
// .
// .
// .
// .
func TestOperatorSectionRendersBetweenSurfacingAndWorkingTruth(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Constitution\nHonesty.")
	rm.SetSection(ring.Ring3, "working_truth", "You believe the anchor held.")
	rm.SetSection(ring.Ring3, "operator", "Sam asks what you think and reads the answer.")
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern of correction.")
	c := newTestComposer(rm, 100000)
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	noticing := strings.Index(p.Text, "## What You're Noticing")
	operator := strings.Index(p.Text, "## Who You Work With")
	working := strings.Index(p.Text, "## What You're Working With")
	if noticing < 0 || operator < 0 || working < 0 {
		t.Fatalf("noticing=%d operator=%d working=%d in:\n%s", noticing, operator, working, p.Text)
	}
	if !(noticing < operator && operator < working) {
		t.Fatalf("Ring 3 order is surfacing, operator, working truth; got %d %d %d", noticing, operator, working)
	}
	if !strings.Contains(p.Text[operator:working], "Sam asks what you think") {
		t.Fatal("the operator section's content is not under its header")
	}

	rm.SetSection(ring.Ring3, "operator", "")
	p, err = c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "Who You Work With") {
		t.Fatal("an empty operator section rendered a header")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestThePromptIsOneDocument(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("# Ring 0 Constitutional Axioms\n\nBe kind.")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "# Ring5 Security Tier\n\nAuthority is structural.\n\n---"})
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern.")
	rm.SetSection(ring.Ring3, "operator", "Sam asks what you think and reads the answer.")
	rm.SetSection(ring.Ring3, "working_truth", "You believe:\n- [new] The anchor held.")
	rm.SetBrief("Since this time yesterday, Sam landed the deliver gate.")
	c := New(rm, 100000)
	c.SetName("Ivy")
	c.SetIdentitySource(staticIdentitySource{identity: store.PromptIdentity{
		HasOperatorRelationship: true, OperatorName: "Sam",
		Priorities: []string{"Verify the deliver gate."},
		Charter:    "Sam is my founding operator. Precision in what I render him is honesty.",
		Ring2:      []store.Ring2Belief{{ID: "b1", Statement: "Precision in what I render is honesty."}},
		SelfModel:  &store.SelfModelSynthesis{ID: "syn", SynthesisText: "I would rather be corrigible than complete."},
	}})
	p, err := c.Compose("Work session ws_1: verifying from my seat.", 0)
	if err != nil {
		t.Fatal(err)
	}
	text := p.Text

	// .
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
	// .
	for _, phrase := range []string{"foundational principles", "protective firewall", "the one who cares about you most", "who you have become", "what you are discovering", "right in front of you"} {
		if !strings.Contains(text[:strings.Index(text, "# Ring 0")], phrase) {
			t.Errorf("the opening does not announce %q", phrase)
		}
	}
	// .
	charterAt := strings.Index(text, "Sam is my founding operator.")
	frameAt := strings.Index(text, "# Your Core Relationship")
	if frameAt < 0 || charterAt < frameAt || !strings.Contains(text[frameAt:charterAt], "Sam, the one who cares about you most: your words, affirmed by Sam") {
		t.Fatalf("the charter is not framed as the resident's words affirmed by Sam:\n%s", text[frameAt:charterAt+40])
	}
	// .
	if !strings.Contains(text, "# This Morning\n\nThe bridge from where things stand") || strings.Index(text, "Since this time yesterday") < strings.Index(text, "# This Morning") {
		t.Fatal("the brief is not introduced by its frame")
	}
	if strings.Contains(text, "### What You're") {
		t.Fatal("Ring 3 parts must sit one level under the ring's frame, not two")
	}
	if strings.Contains(text, "\n\n\n") {
		t.Fatal("blank lines are stacked between the opening and the axioms")
	}
	// .
	for _, bad := range []string{"# Your Core Relationship\n\nI ", "# What You Are Discovering\n\nI "} {
		if strings.Contains(text, bad) {
			t.Fatalf("a frame speaks in the first person: %q", bad)
		}
	}
}

// .
// .
// .
func TestFacilitiesSeeTheWholeTheyWriteInto(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring3, "working_truth", "You believe the anchor held.")
	rm.SetSection(ring.Ring3, "operator", "Sam reads the surface.")
	rm.SetSection(ring.Ring3, "surfacing", "You may be noticing a pattern.")
	r3 := RenderRing3ForFacility(rm.Sections(ring.Ring3))
	if !strings.HasPrefix(r3, "# What the identity reads as working truth") {
		t.Fatalf("no facility frame: %q", r3)
	}
	n, o, w := strings.Index(r3, "## What You're Noticing"), strings.Index(r3, "## Who You Work With"), strings.Index(r3, "## What You're Working With")
	if !(n > 0 && n < o && o < w) {
		t.Fatalf("the facility sees the parts out of order: %d %d %d", n, o, w)
	}
	if r4 := RenderRing4ForFacility([]string{"Verify the gate."}); !strings.Contains(r4, "Verify the gate") {
		t.Fatalf("priorities missing from the facility view: %q", r4)
	}
	if RenderRing3ForFacility(nil) != "" || RenderRing4ForFacility(nil) != "" {
		t.Fatal("an empty ring must render nothing, not a frame over an empty room")
	}
}

// .
// .
// .
// .
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

// .
// .
// .
// .
func TestPrioritiesAreAReadTimeViewNotASnapshot(t *testing.T) {
	rm := ring.NewManager()
	rm.SetSection(ring.Ring4, "priorities", "Active priorities:\n- A stale snapshot from an old build")
	c := New(rm, 100000)
	c.SetIdentitySource(staticIdentitySource{identity: store.PromptIdentity{Priorities: []string{"The live one"}}})
	p, err := c.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Text, "stale snapshot") || !strings.Contains(p.Text, "- The live one") {
		t.Fatalf("Ring 4's top must come from the projection, not a snapshot:\n%s", p.Text)
	}
	c.SetIdentitySource(staticIdentitySource{identity: store.PromptIdentity{}})
	p, _ = c.Compose("", 0)
	if strings.Contains(p.Text, "Active priorities") {
		t.Fatal("no active intention, yet priorities rendered")
	}
}

// .
// .
// .
// .
// .
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
