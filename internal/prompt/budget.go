package prompt

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sort"
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
// .
// .
// .
// .
// .
func foldOrder() [5]string {
	return [5]string{"brief", "turn", "ring4", "ring3", "ring2"}
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
func omitOrder() [4]string {
	return [4]string{"brief", "turn", "ring4", "ring3"}
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
	case "turn":
		return "work status lists your running and delivered sub-agents, recall (source=alarms) the alarms that fired"
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
	switch source {
	case "ring2":
		return summarizeRing2(content)
	case "ring3":
		return summarizeRing3(content)
	}
	return SummarizeUnits(content, routeFor(source))
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
var ring3YieldOrder = []string{"surfacing", "operator", "working_truth"}

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
// .
// .
func summarizeRing3(content string) string {
	type span struct {
		name, header string
		start, end   int
	}
	var spans []span
	for _, p := range ring3Parts {
		// .
		// .
		// .
		// .
		// .
		i, twice := headerAt(content, p.header)
		if twice {
			return SummarizeUnits(content, routeFor("ring3"))
		}
		if i >= 0 {
			spans = append(spans, span{name: p.name, header: p.header, start: i, end: len(content)})
		}
	}
	if len(spans) < 2 {
		return SummarizeUnits(content, routeFor("ring3"))
	}
	// .
	// .
	// .
	// .
	// .
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for i := 0; i < len(spans)-1; i++ {
		spans[i].end = spans[i+1].start
	}

	present := make(map[string]bool, len(spans))
	for _, s := range spans {
		present[s.name] = true
	}
	keep := (len(spans) + 1) / 2
	drop := make(map[string]bool, len(spans))
	for _, name := range ring3YieldOrder {
		if len(spans)-len(drop) <= keep {
			break
		}
		if present[name] {
			drop[name] = true
		}
	}
	if len(drop) == 0 {
		return content
	}

	// .
	// .
	var b strings.Builder
	b.WriteString(strings.TrimRight(content[:spans[0].start], "\n"))
	var dropped []string
	for _, s := range spans {
		if drop[s.name] {
			dropped = append(dropped, strings.TrimSpace(strings.TrimPrefix(s.header, "## ")))
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(content[s.start:s.end]))
	}
	route := routeFor("ring3")
	if drop["operator"] {
		// .
		// .
		// .
		route += "; the operator model is not searchable and returns with the next consolidation"
	}
	return fmt.Sprintf("%s\n\n%s %d of %d parts kept; %s not in view; %s]",
		b.String(), SummaryMarker, len(spans)-len(dropped), len(spans),
		strings.Join(dropped, " and "), route)
}

// .
// .
func headerAt(content, header string) (int, bool) {
	first := -1
	for from := 0; from <= len(content); {
		i := strings.Index(content[from:], header)
		if i < 0 {
			break
		}
		at := from + i
		if at == 0 || content[at-1] == '\n' {
			if first >= 0 {
				return first, true
			}
			first = at
		}
		from = at + len(header)
	}
	return first, false
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
	before := budgetTokens(sections, nil)
	if before <= b.maxTokens {
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
	observeFold(b.maxTokens, before, budgetTokens(sections, omissions), sections, omissions)
	return sections, omissions
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
// .
// .
// .
func observeFold(budget, before, after int, sections []Section, omissions []Omission) {
	folded := make([]string, 0, len(sections))
	for _, s := range sections {
		if s.Folded {
			folded = append(folded, s.Source)
		}
	}
	dropped := make([]string, 0, len(omissions))
	for _, o := range omissions {
		dropped = append(dropped, o.Source)
	}
	logsink.Info("prompt.budget", "budget=%d in=%d out=%d folded=[%s] omitted=[%s]",
		budget, before, after,
		strings.Join(folded, " "), strings.Join(dropped, " "))
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
