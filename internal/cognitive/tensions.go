package cognitive

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/store"
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
type TensionsSource interface {
	TensionsView() ([]store.TensionPair, error)
	TensionEnds(ids []string) (map[string]store.TensionEnd, error)
}

// .
// .
// .
const defaultTensionsMaxChars = 4000

// .
// .
// .
// .
// .
const tensionExcerptChars = 240

// .
// .
// .
// .
// .
// .
// .
func renderTensions(src TensionsSource, listed map[string]bool, maxChars int) (string, error) {
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
		maxChars = defaultTensionsMaxChars
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
			describeTensionEnd(ends[p.LeftID], p.LeftID, listed), describeTensionEnd(ends[p.RightID], p.RightID, listed)))
	}
	// .
	// .
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

// .
// .
// .
func describeTensionEnd(end store.TensionEnd, id string, listed map[string]bool) string {
	switch {
	case end.Kind == "belief" && listed[id] && !end.Retired:
		return fmt.Sprintf("[%s]", id)
	case end.Kind == "belief" && end.Retired:
		return fmt.Sprintf("[%s] a belief since retired: %q", id, excerpt(end.Text))
	case end.Kind == "belief":
		return fmt.Sprintf("[%s] %q", id, excerpt(end.Text))
	case end.Kind == "experience" && end.Sealed:
		return fmt.Sprintf("[%s] a private note (sealed: its content is not shown)", id)
	case end.Kind == "experience":
		return fmt.Sprintf("[%s] %s: %q", id, whoseWords(end.Provenance), excerpt(end.Text))
	default:
		return fmt.Sprintf("[%s] (resolves to nothing in the record)", id)
	}
}

func whoseWords(provenance string) string {
	switch provenance {
	case "operator":
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

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= tensionExcerptChars {
		return s
	}
	r := []rune(s)
	return string(r[:tensionExcerptChars]) + "…"
}

// .
// .
// .
// .
func (d *DreamFacility) TensionsWired() bool { return d.tensions != nil }

// .
func (c *ConsolidateFacility) TensionsWired() bool { return c.tensions != nil }
