package cognitive

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type TensionsSource interface {
	TensionsView() ([]rows.TensionPair, error)
	TensionEnds(ids []string) (map[string]rows.TensionEnd, error)
}

const DefaultTensionsMaxChars = 4000

const DefaultExcerptChars = 240

func renderTensions(src TensionsSource, listed map[string]bool, maxChars, excerptChars int) (string, error) {
	if src == nil {
		return "", nil
	}
	pairs, err := src.TensionsView()
	if err != nil {
		return "", fmt.Errorf("tensions view: %w", err)
	}
	if len(pairs) == 0 {
		return "", nil
	}
	if maxChars <= 0 {
		maxChars = DefaultTensionsMaxChars
	}
	ids := make([]string, 0, len(pairs)*2)
	for _, p := range pairs {
		ids = append(ids, p.LeftID, p.RightID)
	}
	ends, err := src.TensionEnds(ids)
	if err != nil {
		return "", fmt.Errorf("tensions view: %w", err)
	}
	lines := make([]string, 0, len(pairs))
	for _, p := range pairs {
		lines = append(lines, fmt.Sprintf("- %s stands against %s",
			describeTensionEndForDreamOrConsolidate(ends[p.LeftID], p.LeftID, listed, excerptChars), describeTensionEndForDreamOrConsolidate(ends[p.RightID], p.RightID, listed, excerptChars)))
	}

	declare := func(left int) string {
		return fmt.Sprintf("- and %d more standing contradiction(s) not shown here (the oldest are above)", left)
	}
	reserve := utf8.RuneCountInString(declare(len(lines))) + 1
	used, kept := 0, 0
	for _, line := range lines {
		n := utf8.RuneCountInString(line) + 1
		budget := maxChars
		if kept+1 < len(lines) {
			budget -= reserve
		}
		if used+n > budget {
			break
		}
		used += n
		kept++
	}
	out := lines[:kept]
	if kept < len(lines) {
		out = append(append([]string{}, out...), declare(len(lines)-kept))
	}
	return strings.Join(out, "\n"), nil
}

const unmetabolizedEnd = "a note kept out of DREAM and CONSOLIDATE (its words are not given to this pass)"

func describeTensionEndForDreamOrConsolidate(end rows.TensionEnd, id string, listed map[string]bool, excerptChars int) string {
	if end.Kind == "experience" && end.Unmetabolized {
		return fmt.Sprintf("[%s] %s", id, unmetabolizedEnd)
	}
	return describeTensionEnd(end, id, listed, excerptChars)
}

func describeTensionEnd(end rows.TensionEnd, id string, listed map[string]bool, excerptChars int) string {
	switch {
	case end.Kind == "belief" && listed[id]:
		return fmt.Sprintf("[%s]", id)
	case end.Kind == "belief" && end.Retired:
		return fmt.Sprintf("[%s] a belief since retired: %q", id, excerpt(end.Text, excerptChars))
	case end.Kind == "belief":
		return fmt.Sprintf("[%s] %q", id, excerpt(end.Text, excerptChars))
	case end.Kind == "experience" && end.Unmetabolized:

		return fmt.Sprintf("[%s] %s, kept out of DREAM and CONSOLIDATE: %q", id, whoseWords(end.Provenance, end.OperatorVerbatim), excerpt(end.Text, excerptChars))
	case end.Kind == "experience":
		return fmt.Sprintf("[%s] %s: %q", id, whoseWords(end.Provenance, end.OperatorVerbatim), excerpt(end.Text, excerptChars))
	default:
		return fmt.Sprintf("[%s] (resolves to nothing in the record)", id)
	}
}

func whoseWords(provenance string, verbatim bool) string {
	switch provenance {
	case "operator":
		if !verbatim {
			return "about what the operator said"
		}
		return "the operator's words"
	case "external":
		return "an outside source"
	case "dream":
		return "a dream note"
	case "system":
		return "a system record"
	default:
		return "a note of yours"
	}
}

func excerpt(s string, n int) string {
	if n <= 0 {
		n = DefaultExcerptChars
	}
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func (d *DreamFacility) TensionsWired() bool { return d.tensions != nil }

func (c *ConsolidateFacility) TensionsWired() bool { return c.tensions != nil }
