package project

import (
	"fmt"
	"strings"
)

// .
// .
// .
// .
func matchAcceptanceItem(acc []string, q string) (int, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return -1, fmt.Errorf("name the acceptance item (its text) to bind evidence to")
	}
	for i, a := range acc {
		if strings.TrimSpace(a) == q {
			return i, nil
		}
	}
	lq := strings.ToLower(q)
	hit := -1
	for i, a := range acc {
		if strings.Contains(strings.ToLower(a), lq) {
			if hit != -1 {
				return -1, fmt.Errorf("%q matches more than one acceptance item — use the exact text", q)
			}
			hit = i
		}
	}
	if hit == -1 {
		return -1, fmt.Errorf("no acceptance item matches %q", q)
	}
	return hit, nil
}

// .
// .
// .
// .
// .
// .
func checkEvidenceClass(class string) error {
	class = strings.TrimSpace(class)
	if class == "" {
		return fmt.Errorf("observation needs an evidence class")
	}
	if class == ObservationWaived {
		return fmt.Errorf("record a waiver through WaiveItem (it requires a reason) — the evidence path does not accept %q", ObservationWaived)
	}
	if !isKnownEvidenceClass(class) {
		return fmt.Errorf("unknown evidence class %q", class)
	}
	return nil
}

func (m *Manager) RecordObservationByText(id, itemText, class, ref, note, by string) (*Project, error) {
	if err := checkEvidenceClass(class); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	idx, err := matchAcceptanceItem(p.Contract.Acceptance, itemText)
	if err != nil {
		return nil, err
	}
	return m.recordLocked(id, idx, strings.TrimSpace(class), ref, note, by)
}

// .
func (m *Manager) WaiveItemByText(id, itemText, by, reason string) (*Project, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("a waiver needs a reason — waivers are explicit and attributable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	idx, err := matchAcceptanceItem(p.Contract.Acceptance, itemText)
	if err != nil {
		return nil, err
	}
	return m.recordLocked(id, idx, ObservationWaived, "", reason, by)
}

// .
// .
func RenderProgressLine(prog ContractProgress) string {
	if !prog.HasCriteria {
		return "progress: no acceptance criteria"
	}
	s := fmt.Sprintf("progress: %d verified, %d supported, %d open, %d unsupported, %d waived",
		prog.Counts[AcceptanceVerified], prog.Counts[AcceptanceSupported],
		prog.Counts[AcceptanceOpen], prog.Counts[AcceptanceUnsupported], prog.Counts[AcceptanceWaived])
	if prog.NextIndex >= 0 {
		s += " — next: " + prog.Items[prog.NextIndex].Text
	} else {
		s += " — all verified or waived"
	}
	return s
}
