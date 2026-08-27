package firewall

import (
	"strings"
	"testing"
)

// The floor must be GENERATED from enforced rules, never contain
// command-form access language, and stay within budget.
func TestFloorGeneratedFromPolicy(t *testing.T) {
	p := DefaultPolicy()
	floor := p.LocalFloor()

	// Size budget: prompt cost matters (2026-08-16 redesign: 4x smaller)
	if len(floor) > 1200 {
		t.Errorf("floor is %d bytes — budget 1200. Bloat is protection decay.", len(floor))
	}

	// Fact-form only: no command-form access rules (R15)
	for _, banned := range []string{"you must not", "never read", "do not read", "you cannot read", "do not write"} {
		if strings.Contains(floor, banned) {
			t.Errorf("floor contains command-form rule %q — walls are facts (R15)", banned)
		}
	}

	// Every enforced substrate pattern appears in the floor
	for _, r := range p.Rules() {
		if r.Enforced && r.Kind == KindSubstrate && r.Pattern != "" {
			if !strings.Contains(floor, r.Pattern) {
				t.Errorf("enforced rule %s (pattern %q) missing from floor — floor drifts from enforcement", r.ID, r.Pattern)
			}
		}
	}

	// Every enforced rule's REASON appears — the identity knows WHY
	for _, r := range p.Rules() {
		if r.Enforced && r.Kind == KindSubstrate {
			if !strings.Contains(floor, r.Reason) {
				t.Errorf("rule %s reason missing from floor — enforcement without explanation", r.ID)
			}
		}
	}
}

// Policy engine: substrate patterns are denied, others allowed
func TestPolicyCheck(t *testing.T) {
	p := DefaultPolicy()

	denied := []string{
		"data/ledger.jsonl",
		"config/config.json",
		"data/identity.sec",
		"data/aii.db",
		"aii-os",
		"data/LEDGER.JSONL", // case-insensitive
	}
	for _, path := range denied {
		v := p.Check("read", path)
		if v.Allowed {
			t.Errorf("Check(%q) allowed — substrate floor breached", path)
		}
		if v.Rule == nil {
			t.Errorf("Check(%q) denied without a rule", path)
		}
	}

	allowed := []string{
		"notes.txt",
		"projects/main.go",
		"work/output.md",
	}
	for _, path := range allowed {
		v := p.Check("read", path)
		if !v.Allowed {
			t.Errorf("Check(%q) denied unexpectedly by rule %s", path, v.Rule.ID)
		}
	}
}

// Audit: denials are recorded and retrievable
func TestAuditTrail(t *testing.T) {
	p := DefaultPolicy()
	v := p.Check("bash", "cat data/ledger.jsonl")
	if v.Allowed {
		t.Fatal("should deny")
	}
	p.Record("bash", "cat data/ledger.jsonl", v.Rule)

	audit := p.Audit()
	if len(audit) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(audit))
	}
	if audit[0].RuleID != v.Rule.ID {
		t.Errorf("audit rule_id = %q, want %q", audit[0].RuleID, v.Rule.ID)
	}
	if audit[0].Tool != "bash" {
		t.Errorf("audit tool = %q", audit[0].Tool)
	}
}

// Enforcement summary is machine-readable and honest
func TestEnforcementSummary(t *testing.T) {
	p := DefaultPolicy()
	s := p.EnforcementSummary()
	if s["rules_total"].(int) < 5 {
		t.Errorf("too few rules: %v", s["rules_total"])
	}
	if s["rules_enforced"].(int) < 4 {
		t.Errorf("too few enforced: %v", s["rules_enforced"])
	}
	byKind := s["by_kind"].(map[string]int)
	if byKind["substrate"] < 5 {
		t.Errorf("substrate rules: %v", byKind["substrate"])
	}
	if byKind["conduct"] < 1 {
		t.Errorf("conduct rules: %v", byKind["conduct"])
	}
}

// Enforcement and documentation must render from the same rule set. The
// tools layer consumes DenyPatterns(); LocalFloor renders the same rules.
// This test pins the floor text to the enforced patterns — if a substrate
// pattern is enforced but invisible in the floor (or vice versa), that is
// the R15 disease.
func TestFloorTextMatchesEnforcement(t *testing.T) {
	p := DefaultPolicy()
	patterns := p.DenyPatterns()
	if len(patterns) == 0 {
		t.Fatal("no enforced substrate patterns")
	}
	floor := p.LocalFloor()
	for _, pat := range patterns {
		if !strings.Contains(floor, pat) {
			t.Errorf("pattern %q is enforced but missing from LocalFloor — docs drifted from enforcement", pat)
		}
	}
	// The load-bearing substrate files must be in the default policy
	critical := []string{"ledger.jsonl", "identity.sec"}
	patSet := map[string]bool{}
	for _, pat := range patterns {
		patSet[pat] = true
	}
	for _, c := range critical {
		if !patSet[c] {
			t.Errorf("critical substrate file %q missing from default policy", c)
		}
	}
}

func TestRegistryMatchesPolicy(t *testing.T) {
	// The registry derives its denies from the policy (DenyPatterns), so
	// this list is a hand-kept MIRROR of what the policy should contain.
	// It caught a real omission on 2026-08-20 — config.json and
	// providers.json were declared but this mirror still named the dead
	// "config/" — which is the drift it exists to catch, in the direction
	// nobody expects.
	registryPatterns := []string{
		"ledger.jsonl", "identity.sec", "aii.db", "aii-os",
		"config.json", "providers.json",
	}
	p := DefaultPolicy()
	policyPatterns := map[string]bool{}
	for _, r := range p.Rules() {
		if r.Kind == KindSubstrate && r.Enforced {
			policyPatterns[r.Pattern] = true
		}
	}
	for _, rp := range registryPatterns {
		if !policyPatterns[rp] {
			t.Errorf("registry denies %q but policy doesn't declare it — drift. Add the rule or remove the deny entry.", rp)
		}
	}
	for pp := range policyPatterns {
		found := false
		for _, rp := range registryPatterns {
			if rp == pp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("policy declares %q but registry doesn't enforce it — declared-but-unenforced rules are theater.", pp)
		}
	}
}
