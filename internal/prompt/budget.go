package prompt

import (
	"fmt"
	"strings"
)

type budgetEnforcer struct {
	maxTokens int
}

func newBudgetEnforcer(maxTokens int) *budgetEnforcer {
	return &budgetEnforcer{maxTokens: maxTokens}
}

// .
// .
// .
// .
// .
func foldOrder() [4]string {
	return [4]string{"brief", "ring4", "ring3", "ring2"}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func omitOrder() [3]string {
	return [3]string{"brief", "ring4", "ring3"}
}

const budgetRoute = "ask your operator to raise the prompt budget"

// .
// .
// .
// .
// .
// .
func sectionRoute(source string) string {
	switch source {
	case "ring2":
		return "recall (source=ledger) reaches the derivation"
	case "ring3":
		return "recall (source=experiences) reaches what was surfaced and recorded, recall (source=ledger) reaches your beliefs"
	case "ring4":
		return "work status lists your live sessions"
	case "brief":
		return "recall (source=experiences) reaches the day"
	}
	return ""
}

// .
func routeFor(source string) string {
	if r := sectionRoute(source); r != "" {
		return r + "; " + budgetRoute
	}
	return budgetRoute
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func summarize(source, content string) string {
	if source == "ring2" {
		return summarizeRing2(content)
	}
	return SummarizeUnits(content, routeFor(source))
}

// .
// .
// .
// .
func summarizeRing2(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	elided := 0
	for _, ln := range lines {
		if strings.HasPrefix(ln, "  - ") {
			elided++
			continue
		}
		kept = append(kept, ln)
	}
	if elided == 0 {
		return content
	}
	return fmt.Sprintf("%s\n\n[summary — every belief shown; %d evidence line(s) elided under context pressure; %s]",
		strings.TrimRight(strings.Join(kept, "\n"), "\n"), elided, routeFor("ring2"))
}

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
const SummaryMarker = "[summary —"

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
func SummarizeUnits(content, route string) string {
	body := strings.TrimSpace(content)
	for _, sep := range []string{"\n\n", "\n", ". "} {
		units := strings.Split(body, sep)
		if len(units) < 2 {
			continue
		}
		keep := (len(units) + 1) / 2
		head := strings.Join(units[:keep], sep)
		if sep == ". " {
			head += "."
		}
		return fmt.Sprintf("%s\n\n%s %d of %d kept; the rest is not in view; %s]",
			head, SummaryMarker, keep, len(units), route)
	}
	return content
}

// .
// .
// .
func (b *budgetEnforcer) ForceFoldElastic(sections []Section) {
	for _, source := range foldOrder() {
		for i := range sections {
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Folded || s.Content == "" {
				continue
			}
			folded := summarize(s.Source, s.Content)
			if estimateTokens(folded) >= estimateTokens(s.Content) {
				continue
			}
			s.Content = folded
			s.Folded = true
		}
	}
}

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
// .
// .
type Omission struct{ Name, Source string }

func (b *budgetEnforcer) FoldAndTrim(sections []Section) ([]Section, []Omission) {
	if budgetTokens(sections, nil) <= b.maxTokens {
		return sections, nil
	}

	// .
	for _, source := range foldOrder() {
		if budgetTokens(sections, nil) <= b.maxTokens {
			break
		}
		for i := range sections {
			if budgetTokens(sections, nil) <= b.maxTokens {
				break
			}
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Folded || s.Content == "" {
				continue
			}
			folded := summarize(s.Source, s.Content)
			if estimateTokens(folded) >= estimateTokens(s.Content) {
				continue
			}
			s.Content = folded
			s.Folded = true
		}
	}

	// .
	var omissions []Omission
	for _, source := range omitOrder() {
		if budgetTokens(sections, omissions) <= b.maxTokens {
			break
		}
		for i := range sections {
			if budgetTokens(sections, omissions) <= b.maxTokens {
				break
			}
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Content == "" {
				continue
			}
			omissions = append(omissions, Omission{Name: s.Name, Source: s.Source})
			s.Content = ""
		}
	}
	return sections, omissions
}

func budgetTokens(sections []Section, omissions []Omission) int {
	parts := make([]string, 0, len(sections)+1)
	for _, section := range sections {
		if section.Content != "" {
			parts = append(parts, section.Content)
		}
	}
	if len(omissions) > 0 {
		parts = append(parts, renderOmissions(omissions))
	}
	return estimateTokens(strings.Join(parts, "\n\n"))
}

func renderOmissions(omissions []Omission) string {
	var b strings.Builder
	b.WriteString("# Not Shown (context budget)\n")
	for _, o := range omissions {
		fmt.Fprintf(&b, "- %s — %s\n", o.Name, routeFor(o.Source))
	}
	return b.String()
}
