package prompt

import (
	"strings"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

type RingSource interface {
	Ring0() string
	Ring5() string
	Ring3() string
	Ring4() string
}

type Gate struct {
	rings     RingSource
	maxTokens atomic.Int64

	noWindow atomic.Bool
}

func NewGate(rings RingSource, maxTokens int) *Gate {
	if maxTokens == 0 {
		maxTokens = wire.DefaultContextBudgetTokens
	}
	g := &Gate{rings: rings}
	g.maxTokens.Store(int64(maxTokens))
	return g
}

func (g *Gate) SetMaxTokens(maxTokens int) {
	if maxTokens == 0 {
		maxTokens = wire.DefaultContextBudgetTokens
	}
	g.maxTokens.Store(int64(maxTokens))
}

func (g *Gate) SetWindowKnown(known bool) { g.noWindow.Store(!known) }

func (g *Gate) SystemWithIdentitySeam(callerContent, ring1, ring2 string) (string, int) {
	return g.SystemWithin(callerContent, ring1, ring2, 0)
}

func (g *Gate) SystemWithin(callerContent, ring1, ring2 string, reserve int) (string, int) {
	var sections []Section
	appendSection := func(source, name, text string, elastic bool) {
		if strings.TrimSpace(text) != "" {
			sections = append(sections, Section{Name: name, Content: text, Source: source, Elastic: elastic, Volatile: source == "ring3" || source == "ring4"})
		}
	}
	appendSection("ring0", "Ring 0", g.rings.Ring0(), false)
	appendSection("ring5", "Ring 5", g.rings.Ring5(), false)
	appendSection("ring1", "Ring 1", ring1, false)

	appendSection("ring2", "Ring 2", ring2, true)
	sections = append(sections, Section{Content: callerContent, Source: "caller"})
	appendSection("ring3", "Ring 3 working truth", g.rings.Ring3(), true)
	appendSection("ring4", "Ring 4 working state", g.rings.Ring4(), true)

	budget := int(g.maxTokens.Load()) - max(reserve, 0)
	if budget < 1 {
		budget = 1
	}
	enforcer := newBudgetEnforcer(budget, nil)
	enforcer.room = windowRoute
	if g.noWindow.Load() {
		enforcer.room = ceilingRoute
	}
	sections, omissions := enforcer.FoldAndTrim(sections)
	text, _, stable := assemble(sections, omissions, nil, enforcer.room)
	return text, stable
}
