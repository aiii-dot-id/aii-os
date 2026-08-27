package prompt

import (
	"strings"
	"sync/atomic"
)

// Gate applies the identity ring contract to resident and cognitive prompts:
//
//   Ring 0 — VERBATIM, always, never trimmed
//   Ring 5 — VERBATIM, always, never trimmed
//   Ring 1 — VERBATIM (the charter does not yield to token pressure)
//   Ring 2 — whole; derived identity does not yield to token pressure
//   Ring 3 — may be deterministically folded when needed
//   Ring 4 — may be deterministically folded when needed

// RingSource is what the gate needs from the ring manager. Ledger-derived
// Ring 1 and Ring 2 enter together from the projection snapshot.
type RingSource interface {
	Ring0() string // verbatim constitution ("" = not born yet)
	Ring5() string // verbatim firewall
	Ring3() string // working truth (elastic)
	Ring4() string // working state (elastic)
}

// Gate is the identity-context boundary shared by the composer and facilities.
type Gate struct {
	rings     RingSource
	maxTokens atomic.Int64
}

// NewGate builds the engine's gate over the ring source.
func NewGate(rings RingSource, maxTokens int) *Gate {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	g := &Gate{rings: rings}
	g.maxTokens.Store(int64(maxTokens))
	return g
}

// SetMaxTokens applies a resolved model's prompt ceiling to subsequent
// requests. The gate object is shared by every caller, so it is updated in
// place when a provider changes rather than replaced underneath facilities.
func (g *Gate) SetMaxTokens(maxTokens int) {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	g.maxTokens.Store(int64(maxTokens))
}

// SystemForPrompt applies the gate to COMPOSED prompts (Method review
// 2026-08-18, F1): the string-containment presence check re-injected
// any ring the Accordion had FOLDED — a folded digest does not contain
// the full text, so every sub-agent's scoped context was silently
// re-inflated. For a composed prompt, presence means the composer made
// a DELIBERATE disposition — rendered whole, folded to a digest, or
// omitted with a declared route are all dispositions. The gate restores
// ring-manager material for raw-string callers; the composer owns the
// ledger-derived Ring 1 disposition.
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

// SystemWithIdentity applies the same gate to Ring 1 and Ring 2 material
// supplied by one read-time projection snapshot.
func (g *Gate) SystemWithIdentity(callerContent, ring1, ring2 string) string {
	return g.system(callerContent, ring1, ring2)
}

func (g *Gate) system(callerContent, ring1, ring2 string) string {
	var sections []Section
	appendSection := func(source, name, text string, elastic bool) {
		if strings.TrimSpace(text) != "" {
			sections = append(sections, Section{Name: name, Content: text, Source: source, Elastic: elastic})
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
	for _, section := range sections {
		if section.Content != "" {
			parts = append(parts, section.Content)
		}
	}
	if len(omissions) > 0 {
		parts = append(parts, renderOmissions(omissions))
	}
	return strings.Join(parts, "\n\n")
}
