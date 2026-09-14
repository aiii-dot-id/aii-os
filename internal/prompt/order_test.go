package prompt

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestPromptKeepsItsDeclaredAuthorityOrder(t *testing.T) {
	composer, _, _ := setupComposer(t)
	p, err := composer.Compose("current work state", 0)
	if err != nil {
		t.Fatal(err)
	}

	// .
	order := []struct{ name, marker string }{
		{"Ring 1 (operator)", "Your operator relationship"},
		{"Ring 2 (adopted beliefs)", "Testing reveals truth"},
		{"Current self-model", "careful, curious, and grounded"},
		{"How you act", "# How You Act"},
		{"Ring 4 (working state)", "What Is Right in Front of You"},
	}
	prev, prevName := -1, ""
	for _, o := range order {
		at := strings.Index(p.Text, o.marker)
		if at < 0 {
			t.Fatalf("%s is absent from the emitted prompt (marker %q)", o.name, o.marker)
		}
		if at < prev {
			t.Errorf("%s appears BEFORE %s — the emitted order is not the declared order", o.name, prevName)
		}
		prev, prevName = at, o.name
	}
}

// .
// .
func TestUnfoldedRing2LivesInsideTheCachePrefix(t *testing.T) {
	composer, _, _ := setupComposer(t)
	p, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.StableLen <= 0 || p.StableLen > len(p.Text) {
		t.Fatalf("StableLen %d is not a position in a %d-byte prompt", p.StableLen, len(p.Text))
	}
	if !strings.Contains(p.Text[:p.StableLen], "Testing reveals truth") {
		t.Error("Ring 2 is unchanged and unfolded, yet falls outside the cacheable prefix")
	}
}

// .
func TestWorkingStateChangesDoNotDisturbTheCachePrefix(t *testing.T) {
	composer, _, _ := setupComposer(t)
	a, err := composer.Compose("first working state", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := composer.Compose("a completely different working state", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.StableLen != b.StableLen || a.Text[:a.StableLen] != b.Text[:b.StableLen] {
		t.Error("changing Ring 4 changed the stable prefix — the seam is not where volatility begins")
	}
}
