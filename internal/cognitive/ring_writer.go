package cognitive

import (
	"log"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// RingWriter is the interface facilities use to write their output into
// ring content — and to READ BACK their own prior sections (the
// output-becomes-input loop, 2026-08-17: the unconscious remembers
// itself in the presence of new material; the predicates guarantee the
// material, the read-back enables the continuity).
type RingWriter interface {
	SetRingSection(level ring.RingLevel, name, content string)
	RingSection(level ring.RingLevel, name string) string // "" when absent
}

// BriefWriter is the interface for MORNING_BRIEF's bridge summary.
// Not a ring — a bridge between Ring 3 and Ring 4.
type BriefWriter interface {
	SetBrief(content string)
}

// RingPersister makes facility-authored ring content survive restarts.
// Ring 3 sections, Ring 4 priorities, and the brief are DISPLAY CACHES
// of ledger truth, not truth (H6/#4 fix, 2026-08-20): CONSOLIDATE's
// distillations mint as belief.* events and DREAM's surfacing mints as
// an experience.create — the snapshot only spares the next boot a
// re-render (the projections rebuild from the chain; the next facility
// pass re-renders the prose).
//
// THE GUARANTEE HOLDS BECAUSE THE VIEW IS ALWAYS BACKED. A consolidation
// envelope carries two outputs and only one is truth: operations become
// belief.* events, while ring3_view is prose the tool schema asks for
// freely. Nothing bound the second to the first, so a pass could mint
// NOTHING, write a whole working truth, and consume every input for it —
// leaving that synthesis only in ring_snapshots, which replay does not
// rebuild (found in review, ChatGPT Sol 5.6, 2026-08-24).
//
// CONSOLIDATE now keeps the model's view only when the pass actually
// minted, and otherwise renders Ring 3 deterministically from the store.
// So the section is always either words about beliefs that just landed
// or a render of beliefs already there — and losing it costs a re-render,
// which is what this comment claims.
type RingPersister interface {
	SaveRingSection(level int, name, content string) error
}

// BriefPersister makes the morning brief survive restarts.
type BriefPersister interface {
	SaveBrief(content string) error
}

// ringWriterAdapter adapts a ring.Manager to both RingWriter and BriefWriter.
type ringWriterAdapter struct {
	manager        *ring.Manager
	persister      RingPersister  // nil = memory-only (tests)
	briefPersister BriefPersister // nil = memory-only (tests)
}

// NewRingWriter creates a RingWriter backed by the given ring manager.
func NewRingWriter(m *ring.Manager) RingWriter {
	return &ringWriterAdapter{manager: m}
}

// NewPersistingRingWriter creates a RingWriter whose section writes land
// in both the live manager and the runtime snapshot store.
func NewPersistingRingWriter(m *ring.Manager, p RingPersister) RingWriter {
	return &ringWriterAdapter{manager: m, persister: p}
}

// NewBriefWriter creates a BriefWriter backed by the given ring manager.
func NewBriefWriter(m *ring.Manager) BriefWriter {
	return &ringWriterAdapter{manager: m}
}

// NewPersistingBriefWriter creates a BriefWriter that also persists.
func NewPersistingBriefWriter(m *ring.Manager, p BriefPersister) BriefWriter {
	return &ringWriterAdapter{manager: m, briefPersister: p}
}

func (r *ringWriterAdapter) RingSection(level ring.RingLevel, name string) string {
	return r.manager.Section(level, name)
}

func (r *ringWriterAdapter) SetRingSection(level ring.RingLevel, name, content string) {
	r.manager.SetSection(level, name, content)
	if r.persister != nil {
		if err := r.persister.SaveRingSection(int(level), name, content); err != nil {
			// Memory view stays current; durability is best-effort. The
			// next facility pass re-writes the section.
			persistLog("ring section persist failed: %v", err)
		}
	}
}

func (r *ringWriterAdapter) SetBrief(content string) {
	r.manager.SetBrief(content)
	if r.briefPersister != nil {
		if err := r.briefPersister.SaveBrief(content); err != nil {
			persistLog("brief persist failed: %v", err)
		}
	}
}

func persistLog(format string, args ...interface{}) {
	log.Printf("RINGWRITER: "+format, args...)
}
