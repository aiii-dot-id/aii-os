package prompt

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sort"
	"strings"
)

type budgetEnforcer struct {
	maxTokens int

	turn map[string]bool

	room string
}

func newBudgetEnforcer(maxTokens int, turn map[string]bool) *budgetEnforcer {
	return &budgetEnforcer{maxTokens: maxTokens, turn: turn}
}

func foldOrder() [5]string {
	return [5]string{"brief", "turn", "ring4", "ring3", "ring2"}
}

func omitOrder() [4]string {
	return [4]string{"brief", "turn", "ring4", "ring3"}
}

const budgetRoute = "ask your operator to raise the prompt budget"

const subagentRoom = "folded for a sub-agent; spawn with context=full to carry it whole"

const (
	windowRoute  = "ask your operator for a model with a wider window"
	ceilingRoute = "ask your operator to declare this model's context length, or to raise the prompt budget"
)

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

func routeFor(source, room string) string {
	if room == "" {
		room = budgetRoute
	}
	if r := sectionRoute(source); r != "" {
		return r + "; " + room
	}
	return room
}

func summarize(source, content, room string) string {
	switch source {
	case "ring2":
		return summarizeRing2(content, room)
	case "ring3":
		return summarizeRing3(content, room)
	}
	return SummarizeUnits(content, routeFor(source, room))
}

var ring3YieldOrder = []string{"surfacing", "operator", "working_truth"}

func summarizeRing3(content, room string) string {
	type span struct {
		name, header string
		start, end   int
	}
	var spans []span
	for _, p := range ring3Parts {

		i, twice := headerAt(content, p.header)
		if twice {
			return SummarizeUnits(content, routeFor("ring3", room))
		}
		if i >= 0 {
			spans = append(spans, span{name: p.name, header: p.header, start: i, end: len(content)})
		}
	}
	if len(spans) < 2 {
		return SummarizeUnits(content, routeFor("ring3", room))
	}

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
	route := routeFor("ring3", room)
	if drop["operator"] {

		route += "; the operator model is not searchable and returns with the next consolidation"
	}
	return fmt.Sprintf("%s\n\n%s %d of %d parts kept; %s not in view; %s]",
		b.String(), SummaryMarker, len(spans)-len(dropped), len(spans),
		strings.Join(dropped, " and "), route)
}

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

func summarizeRing2(content, room string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	elided := 0
	for _, ln := range lines {
		if strings.HasPrefix(ln, "  - ") {

			if strings.TrimSpace(ln) == "- (no resolved evidence)" {
				kept = append(kept, ln)
				continue
			}
			elided++
			continue
		}
		kept = append(kept, ln)
	}
	if elided == 0 {
		return content
	}
	return fmt.Sprintf("%s\n\n[summary — every belief shown; %d evidence line(s) elided under context pressure; %s]",
		strings.TrimRight(strings.Join(kept, "\n"), "\n"), elided, routeFor("ring2", room))
}

const SummaryMarker = "[summary —"

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

func (b *budgetEnforcer) ForceFoldElastic(sections []Section) {
	for _, source := range foldOrder() {
		if source == "ring2" {
			continue
		}
		for i := range sections {
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Folded || s.Content == "" {
				continue
			}
			folded := summarize(s.Source, s.Content, subagentRoom)
			if estimateTokens(folded) >= estimateTokens(s.Content) {
				continue
			}
			s.Content = folded
			s.Folded = true
		}
	}
}

type Omission struct{ Name, Source string }

func (b *budgetEnforcer) FoldAndTrim(sections []Section) ([]Section, []Omission) {
	before := b.budgetTokens(sections, nil)
	if before <= b.maxTokens {
		return sections, nil
	}

	for _, source := range foldOrder() {
		if b.budgetTokens(sections, nil) <= b.maxTokens {
			break
		}
		for i := range sections {
			if b.budgetTokens(sections, nil) <= b.maxTokens {
				break
			}
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Folded || s.Content == "" {
				continue
			}
			folded := summarize(s.Source, s.Content, b.room)
			if estimateTokens(folded) >= estimateTokens(s.Content) {
				continue
			}
			s.Content = folded
			s.Folded = true
		}
	}

	var omissions []Omission
	for _, source := range omitOrder() {
		if b.budgetTokens(sections, omissions) <= b.maxTokens {
			break
		}
		for i := range sections {
			if b.budgetTokens(sections, omissions) <= b.maxTokens {
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
	observeFold(b.maxTokens, before, b.budgetTokens(sections, omissions), sections, omissions)
	return sections, omissions
}

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

func (b *budgetEnforcer) budgetTokens(sections []Section, omissions []Omission) int {
	text, turn := renderPrompt(sections, omissions, b.turn, b.room)
	return estimateTokens(text) + estimateTokens(turn)
}

func assemble(sections []Section, omissions []Omission, turn map[string]bool, room string) (text, block string, stableLen int) {
	sealed := false
	for _, s := range sections {
		if s.Content == "" || s.Turn || s.Source == "contract" {
			continue
		}
		if s.Volatile || s.Folded {
			sealed = true
		}
		if !sealed {
			if stableLen > 0 {
				stableLen += 2
			}
			stableLen += len(s.Content)
		}
	}
	text, block = renderPrompt(sections, omissions, turn, room)
	return text, block, stableLen
}

func renderPrompt(sections []Section, omissions []Omission, turn map[string]bool, room string) (text, block string) {
	var parts, turnParts []string
	closing := ""
	for _, s := range sections {
		if s.Content == "" {
			continue
		}
		if s.Turn {
			turnParts = append(turnParts, s.Content)
			continue
		}
		if s.Source == "contract" {
			closing = s.Content
			continue
		}
		parts = append(parts, s.Content)
	}
	var systemOmissions, turnOmissions []Omission
	for _, o := range omissions {
		if turn[o.Source] {
			turnOmissions = append(turnOmissions, o)
		} else {
			systemOmissions = append(systemOmissions, o)
		}
	}
	if len(systemOmissions) > 0 {
		parts = append(parts, renderOmissions(systemOmissions, room))
	}
	if len(turnOmissions) > 0 {
		turnParts = append(turnParts, strings.TrimSpace(renderOmissions(turnOmissions, room)))
	}

	return strings.Join(parts, "\n\n") + closing, strings.Join(turnParts, "\n\n")
}

func renderOmissions(omissions []Omission, room string) string {
	var b strings.Builder
	b.WriteString("# Not Shown (context budget)\n")
	for _, o := range omissions {
		fmt.Fprintf(&b, "- %s — %s\n", o.Name, routeFor(o.Source, room))
	}
	return b.String()
}
