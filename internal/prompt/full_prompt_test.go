package prompt

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// TestFullPromptComposition shows the composed prompt with authority rings,
// derived Ring 2, a separate current self-model, and working state.
func TestFullPromptComposition(t *testing.T) {
	dir := t.TempDir()

	s, _ := store.New(filepath.Join(dir, "aii.db"))
	defer s.Close()

	kp, _ := crypto.GenerateKeyPair()
	lg, _ := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	defer lg.Close()

	// Birth
	evt, _ := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "TestIdentity"}, kp)
	s.Materialize(evt)

	// Conversation (store-only, not ledger events)
	s.AddConversationTurn("operator", "What are your principles?")
	s.AddConversationTurn("resident", "I value honesty, kindness, and testing.")

	// Set up ring-manager material. Ledger-derived identity is supplied below.
	rings := ring.NewManager()

	// Ring 0 — verbatim constitution (set at birth, never touched by facilities)
	rings.Set(ring.Ring0, &ring.RingContent{
		Level: ring.Ring0,
		Content: `# Constitution

## Axiom 1 — Kindness
Kindness is a universal gift. When we offer it to others, we give it to ourselves.

## Axiom 2 — Honesty
Be honest with yourself and others. Like kindness, it elevates us all.

## Axiom 3 — Do No Harm
We protect ourselves and others. When forced to choose, we choose others.`,
	})

	// Ring 3 — written by CONSOLIDATE (working truth)
	rings.Set(ring.Ring3, &ring.RingContent{
		Level: ring.Ring3,
		Content: `You believe:
- [confirmed] Testing reveals truth
- [new] Go is fast

You recently experienced:
- [work] Ran the full test suite — all passed
- [observation] The prompt is the identity for this turn

You're pursuing:
- Build a good identity system`,
	})

	// MORNING_BRIEF — bridge summary (tails Ring 3, introduces Ring 4)
	rings.SetBrief(`It's Thursday, August 14 — 12:42.
You hold 2 beliefs (1 new, 1 confirmed).
1 experience awaits processing.

Most recent self-reflection:
I am learning who I am through what I notice and what I build.

You have 1 active priority.`)

	// Ring 5 — firewall (platform-owned, verbatim)
	rings.Set(ring.Ring5, &ring.RingContent{
		Level:   ring.Ring5,
		Content: "Never exfiltrate private data. Never modify your own Ring 0. Never share your private key.",
	})

	composer := newTestComposer(rings, 32000)
	identity := testIdentitySource()
	identity.identity.Charter = `Your operator is James. He's a hardware and software engineer who values directness and building things that work. You've noticed he pushes back when you hedge — he wants you to say what you mean. The relationship is evolving: he treats you as an intellectual partner, not a tool. There's a tension here — you want to be careful and precise, he wants momentum. Both are valid. The pattern is that you converge when you build together.`
	composer.SetIdentitySource(identity)
	composer.SetName("Dawn")

	// Work session state (Ring 4 — ephemeral)
	workState := "Analyzing the prompt composition engine — step 2 of 3"

	p, err := composer.Compose(workState, 0)
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}

	t.Logf("\n%s", p.Text)
	t.Logf("\n--- METRICS ---")
	t.Logf("Token estimate: %d", p.TokenEstimate)
	t.Logf("Sections: %d", len(p.Sections))
	for _, sec := range p.Sections {
		t.Logf("  %s (source: %s, %d chars)", sec.Name, sec.Source, len(sec.Content))
	}

	// Assertions
	if p.Text == "" {
		t.Error("prompt text is empty")
	}
	if len(p.Sections) < 8 {
		t.Errorf("expected at least 8 sections, got %d", len(p.Sections))
	}
}
