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

	// Birth + materialize
	evt, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "TestIdentity"}, kp)
	s.Materialize(evt)

	// Add a belief
	evt, _ = l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "Testing is good", "ring": 3, "confidence": 0.8}, kp)
	s.Materialize(evt)

	// Add a conversation turn (store-only, not a ledger event)
	s.AddConversationTurn("operator", "Hello")

	// Set up authority and working-truth rings. Ring 2 and the current
	// self-model derive independently from the identity source.
	rings := ring.NewManager()
	rings.Set(ring.Ring0, &ring.RingContent{
		Level:   ring.Ring0,
		Content: "# Constitution\n\nBe kind. Be honest.",
	})
	// What CONSOLIDATE actually writes to Ring 3: a named section, via
	// ring_writer.go -> SetSection. The prior fixture used Set(), which
	// writes m.rings[] — a map no production caller ever populates for
	// rings 1-4 — so every test built on this fixture exercised the
	// GetContent branch instead of the Sections branch production takes.
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
	// REDESIGN: recent turns must NOT be embedded in the composed system
	// prompt — history lives in the message array (client-owned). The
	// composer owns the identity; duplicating turns in two formats wasted
	// tokens and gave the model conflicting renderings.
	dir := t.TempDir()
	st, _ := store.New(filepath.Join(dir, "t.db"))
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
	// Ring 5 should come before Ring 1 (boundaries before guidance)
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

// A belief whose citations never resolved must not render like one that
// honestly cited nothing. The resident who wrote three ids that pointed
// at no entity had no way to see they had not landed — the phantom
// citation conferred no standing (standing.go refuses ghost edges) but
// it was also invisible, which is how it survived days.
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
	// The marker must attach to the ungrounded belief, not the grounded one.
	tail := out[strings.Index(out, "b_ungrounded"):]
	if !strings.Contains(tail, "(no resolved evidence)") {
		t.Fatalf("the marker landed on the wrong belief:\n%s", out)
	}
}
