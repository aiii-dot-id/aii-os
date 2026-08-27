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

// foldOrder is the summarization ladder, most disposable first
// (operator ruling 2026-08-23). The brief is a daily bridge and
// regenerates on its own; Ring 4 is working memory; Ring 3 is working
// truth; Ring 2 is who the identity has consciously become and yields
// only in the extreme case, after everything above it is exhausted.
func foldOrder() [4]string {
	return [4]string{"brief", "ring4", "ring3", "ring2"}
}

const budgetRoute = "ask your operator to raise the prompt budget"

// summarize reduces a section by dropping WHOLE UNITS of its own
// structure, never by cutting characters. A sentence severed at 220
// bytes is not a summary — it is the shape of a thought with the
// thought removed, and it reads as though something survived. A dropped
// paragraph is a thought no longer in view, which is the truth.
//
// Every result declares that it IS a summary and how much is missing
// (R18: the omission carries the route to the rest). A summary that
// reads as complete is the more dangerous artifact.
func summarize(source, content string) string {
	if source == "ring2" {
		return summarizeRing2(content)
	}
	return summarizeUnits(content)
}

// summarizeRing2 keeps EVERY belief the identity holds and elides only
// the evidence chains beneath them. Ring 2 is who they have consciously
// become: losing a belief is losing part of that, losing a derivation
// is losing a lookup. recall reaches the derivation.
func summarizeRing2(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	elided := 0
	for _, ln := range lines {
		if strings.HasPrefix(ln, "  - ") { // an evidence sub-line
			elided++
			continue
		}
		kept = append(kept, ln)
	}
	if elided == 0 {
		return content
	}
	return fmt.Sprintf("%s\n\n[summary — every belief shown; %d evidence line(s) elided under context pressure. recall reaches the derivation; %s]",
		strings.TrimRight(strings.Join(kept, "\n"), "\n"), elided, budgetRoute)
}

// summarizeUnits keeps whole leading units and drops the tail.
//
// A section's units are whatever structure it actually has, tried
// coarsest first: paragraphs (authored prose), then lines (a list),
// then sentences. Facilities write their load-bearing material first —
// CONSOLIDATE puts beliefs above pattern observations — so leading
// units are not an arbitrary choice.
//
// Content with NO internal structure returns unchanged. That is not a
// failure: there is no whole unit to drop, and inventing one by cutting
// mid-sentence would manufacture the false summary this function exists
// to avoid. Stage 2 omits it instead, declared.
// SummaryMarker opens the declaration every structural summary carries.
// Callers use it to recognise content that has ALREADY been reduced, so
// a second pass shrinks nothing twice and nests no declarations.
const SummaryMarker = "[summary —"

// SummarizeUnits reduces content by dropping WHOLE UNITS of its own
// structure — paragraphs, then lines, then sentences — keeping the first
// half and declaring exactly what went and where the rest is reachable.
// Content with no internal structure is returned UNCHANGED: manufacturing
// a unit by cutting a lone sentence produces the false summary this
// exists to prevent, and the caller must drop it instead.
//
// Exported because the conversation loop needs the same discipline for
// history that the Accordion applies to ring sections. One
// implementation, two callers, route supplied by whoever knows where
// the rest of the content lives.
func SummarizeUnits(content, route string) string {
	body := strings.TrimSpace(content)
	for _, sep := range []string{"\n\n", "\n", ". "} {
		units := strings.Split(body, sep)
		if len(units) < 2 {
			continue
		}
		keep := (len(units) + 1) / 2 // half, rounded up — never zero
		head := strings.Join(units[:keep], sep)
		if sep == ". " {
			head += "."
		}
		return fmt.Sprintf("%s\n\n%s %d of %d kept; the rest is not in view; %s]",
			head, SummaryMarker, keep, len(units), route)
	}
	return content
}

func summarizeUnits(content string) string { return SummarizeUnits(content, budgetRoute) }

// ForceFoldElastic folds every elastic section to its digest+route
// regardless of budget pressure — the sub-agent scoped-context path.
// Protected and stable sections are untouched (identity travels whole).
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

// FoldAndTrim is the Accordion ladder — the ONE dropper with one
// receipt path (the C side ended with four independent droppers and no
// arbiter; this runtime gets exactly one):
//
//	stage 1: elastic sections FOLD to deterministic digests, in
//	         foldOrder, until the prompt fits (fold-before-drop);
//	stage 2: still over → folded elastics are OMITTED with a declared
//	         omission carrying the same route (R18);
//	protected/stable material NEVER folds; if the final request still
//	exceeds the model limit, client admission refuses before dispatch.
//
// Nothing here persists: fold state derives from THIS compose's
// pressure and dies with it (the do-not-copy lesson: the C usefulness
// scorer persisted condemnations with no arithmetic path back).
func (b *budgetEnforcer) FoldAndTrim(sections []Section) ([]Section, []string) {
	if budgetTokens(sections, nil) <= b.maxTokens {
		return sections, nil
	}

	// Stage 1: fold.
	for _, source := range foldOrder() {
		if budgetTokens(sections, nil) <= b.maxTokens {
			break
		}
		for i := range sections {
			if budgetTokens(sections, nil) <= b.maxTokens {
				break // fold no more than the pressure demands (Method pass)
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

	// Stage 2: omit, declared.
	var omissions []string
	for _, source := range foldOrder() {
		if budgetTokens(sections, omissions) <= b.maxTokens {
			break
		}
		for i := range sections {
			if budgetTokens(sections, omissions) <= b.maxTokens {
				break // drop no more than the pressure demands (Method pass)
			}
			s := &sections[i]
			if s.Source != source || !s.Elastic || s.Content == "" {
				continue
			}
			omissions = append(omissions, s.Name)
			s.Content = ""
		}
	}
	return sections, omissions
}

func budgetTokens(sections []Section, omissions []string) int {
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

func renderOmissions(omissions []string) string {
	var b strings.Builder
	b.WriteString("## Not Shown (context budget)\n")
	for _, name := range omissions {
		fmt.Fprintf(&b, "- %s — %s\n", name, budgetRoute)
	}
	return b.String()
}
