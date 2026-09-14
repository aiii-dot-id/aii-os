package project

import (
	"fmt"
	"strings"
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
const (
	AcceptanceOpen        = "open"
	AcceptanceSupported   = "supported"
	AcceptanceVerified    = "verified"
	AcceptanceWaived      = "waived"
	AcceptanceUnsupported = "unsupported"
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
type AcceptanceObservation struct {
	Item     int    `json:"item"`
	ItemText string `json:"item_text"`
	Class    string `json:"class"`
	Ref      string `json:"ref,omitempty"`
	Note     string `json:"note,omitempty"`
	By       string `json:"by,omitempty"`
	At       string `json:"at,omitempty"`
}

// .
const ObservationWaived = "waived"

// .
// .
// .
func isVerifiedClass(class string) bool {
	return class == "locally_verified" || class == "host_receipted"
}

// .
// .
// .
func stateForClass(class string) string {
	switch class {
	case ObservationWaived:
		return AcceptanceWaived
	case "locally_verified", "host_receipted":
		return AcceptanceVerified
	case "completed_locally", "worker_report_only":
		return AcceptanceSupported
	default:
		// .
		// .
		// .
		return AcceptanceUnsupported
	}
}

// .
type ItemProgress struct {
	Index int
	Text  string
	State string
	Class string
	Ref   string
	Note  string
	Stale bool
}

// .
// .
// .
type ContractProgress struct {
	HasCriteria    bool
	Items          []ItemProgress
	NextIndex      int
	Counts         map[string]int
	ClosureAllowed bool
	ClosureReason  string
}

func norm(s string) string { return strings.TrimSpace(s) }

// .
// .
// .
func validateAcceptance(c *Contract) error {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, a := range c.Acceptance {
		k := norm(a)
		if k == "" {
			continue
		}
		if seen[k] {
			return fmt.Errorf("duplicate acceptance item %q — give each acceptance claim distinct wording so evidence binds unambiguously", a)
		}
		seen[k] = true
	}
	return nil
}

// .
// .
// .
// .
func isKnownEvidenceClass(class string) bool {
	switch class {
	case "not_run", "rejected_before_effect", "completed_locally", "partial_or_mixed",
		"external_effect_unknown", "worker_report_only", "locally_verified", "host_receipted":
		return true
	}
	return false
}

// .
// .
// .
// .
func DeriveContractProgress(c Contract, obs []AcceptanceObservation) ContractProgress {
	prog := ContractProgress{
		NextIndex: -1,
		Counts: map[string]int{
			AcceptanceOpen: 0, AcceptanceSupported: 0, AcceptanceVerified: 0,
			AcceptanceWaived: 0, AcceptanceUnsupported: 0,
		},
	}
	prog.HasCriteria = len(c.Acceptance) > 0
	for i, text := range c.Acceptance {
		ip := ItemProgress{Index: i, Text: text, State: AcceptanceOpen}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		var decided, staleHint bool
		for j := len(obs) - 1; j >= 0; j-- {
			o := obs[j]
			if norm(o.ItemText) == norm(text) {
				if !decided {
					ip.State = stateForClass(o.Class)
					ip.Class = o.Class
					ip.Ref = o.Ref
					ip.Note = o.Note
					decided = true
				}
				continue
			}
			if o.Item == i {
				staleHint = true
			}
		}
		if !decided && staleHint {
			ip.Stale = true
		}
		prog.Counts[ip.State]++
		prog.Items = append(prog.Items, ip)
		// .
		// .
		// .
		if prog.NextIndex == -1 && ip.State != AcceptanceVerified && ip.State != AcceptanceWaived {
			prog.NextIndex = i
		}
	}
	// .
	// .
	// .
	if !prog.HasCriteria {
		prog.ClosureAllowed = true
	} else if prog.NextIndex == -1 {
		prog.ClosureAllowed = true
	} else {
		it := prog.Items[prog.NextIndex]
		prog.ClosureAllowed = false
		prog.ClosureReason = fmt.Sprintf("acceptance item %d is %s: %q — verify it or record an explicit waiver before closing", it.Index+1, it.State, it.Text)
	}
	return prog
}

// .
// .
// .
// .
// .
func RenderProjectCard(name, focus string, c Contract, prog ContractProgress) string {
	var b strings.Builder
	b.WriteString("### Project: " + name)
	if c.Outcome != "" {
		b.WriteString("\nPursuing: " + c.Outcome)
	}
	if norm(focus) != "" {
		b.WriteString("\nFocus: " + focus)
	}
	if !prog.HasCriteria {
		b.WriteString("\nAcceptance: no acceptance criteria")
	} else {
		b.WriteString(fmt.Sprintf("\nAcceptance: %d verified, %d supported, %d open, %d unsupported, %d waived",
			prog.Counts[AcceptanceVerified], prog.Counts[AcceptanceSupported],
			prog.Counts[AcceptanceOpen], prog.Counts[AcceptanceUnsupported], prog.Counts[AcceptanceWaived]))
		if prog.NextIndex >= 0 {
			it := prog.Items[prog.NextIndex]
			b.WriteString(fmt.Sprintf("\nNext test: [%s] %s", it.State, it.Text))
			switch {
			case it.Stale:
				b.WriteString("\nEvidence: a prior reference is stale (the item text changed) — re-check and re-bind it")
			case it.Class != "":
				scope := "\nEvidence: " + it.Class
				if it.Ref != "" {
					scope += " (" + it.Ref + ")"
				}
				b.WriteString(scope)
			default:
				b.WriteString("\nEvidence: none recorded yet")
			}
		} else {
			b.WriteString("\nNext test: none — every item is verified or waived")
		}
	}
	if len(c.Constraints) > 0 {
		b.WriteString("\nBounded by: " + strings.Join(c.Constraints, "; "))
	}
	if prog.HasCriteria && !prog.ClosureAllowed {
		b.WriteString("\nDecision owed: close is blocked — " + prog.ClosureReason)
	}
	return b.String()
}
