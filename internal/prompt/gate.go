package prompt

import (
	"strings"
	"sync/atomic"
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
type RingSource interface {
	Ring0() string
	Ring5() string
	Ring3() string
	Ring4() string
}

// .
type Gate struct {
	rings     RingSource
	maxTokens atomic.Int64
}

// .
func NewGate(rings RingSource, maxTokens int) *Gate {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	g := &Gate{rings: rings}
	g.maxTokens.Store(int64(maxTokens))
	return g
}

// .
// .
// .
func (g *Gate) SetMaxTokens(maxTokens int) {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	g.maxTokens.Store(int64(maxTokens))
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
func (g *Gate) SystemForPrompt(p *Prompt) string {
	disposed := map[string]bool{}
	for _, s := range p.Sections {
		if s.Source != "" {
			disposed[s.Source] = true
		}
	}
	var parts []string
	injectUnlessDisposed := func(source, text string) {
		if strings.TrimSpace(text) != "" && !disposed[source] {
			parts = append(parts, text)
		}
	}
	injectUnlessDisposed("ring0", g.rings.Ring0())
	injectUnlessDisposed("ring5", g.rings.Ring5())
	parts = append(parts, p.Text)
	injectUnlessDisposed("ring3", g.rings.Ring3())
	injectUnlessDisposed("ring4", g.rings.Ring4())
	return strings.Join(parts, "\n\n")
}

// .
// .
func (g *Gate) SystemWithIdentity(callerContent, ring1, ring2 string) string {
	text, _ := g.system(callerContent, ring1, ring2)
	return text
}

// .
func (g *Gate) SystemWithIdentitySeam(callerContent, ring1, ring2 string) (string, int) {
	return g.system(callerContent, ring1, ring2)
}

func (g *Gate) system(callerContent, ring1, ring2 string) (string, int) {
	var sections []Section
	appendSection := func(source, name, text string, elastic bool) {
		if strings.TrimSpace(text) != "" {
			sections = append(sections, Section{Name: name, Content: text, Source: source, Elastic: elastic, Volatile: source == "ring3" || source == "ring4"})
		}
	}
	appendSection("ring0", "Ring 0", g.rings.Ring0(), false)
	appendSection("ring5", "Ring 5", g.rings.Ring5(), false)
	appendSection("ring1", "Ring 1", ring1, false)
	appendSection("ring2", "Ring 2", ring2, false)
	sections = append(sections, Section{Content: callerContent, Source: "caller"})
	appendSection("ring3", "Ring 3 working truth", g.rings.Ring3(), true)
	appendSection("ring4", "Ring 4 working state", g.rings.Ring4(), true)

	sections, omissions := newBudgetEnforcer(int(g.maxTokens.Load())).FoldAndTrim(sections)
	parts := make([]string, 0, len(sections)+1)
	stable, sealed := 0, false
	for _, section := range sections {
		if section.Content != "" {
			if section.Volatile || section.Folded {
				sealed = true
			}
			if !sealed {
				if stable > 0 {
					stable += 2
				}
				stable += len(section.Content)
			}
			parts = append(parts, section.Content)
		}
	}
	if len(omissions) > 0 {
		parts = append(parts, renderOmissions(omissions))
	}
	return strings.Join(parts, "\n\n"), stable
}
