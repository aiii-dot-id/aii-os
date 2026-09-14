package cognitive

import (
	"log"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
// .
type RingWriter interface {
	SetRingSection(level ring.RingLevel, name, content string)
	RingSection(level ring.RingLevel, name string) string
}

// .
// .
type BriefWriter interface {
	SetBrief(content string)
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
// .
type RingPersister interface {
	SaveRingSection(level int, name, content string) error
}

// .
type BriefPersister interface {
	SaveBrief(content string) error
}

// .
type ringWriterAdapter struct {
	manager        *ring.Manager
	persister      RingPersister
	briefPersister BriefPersister
}

// .
func NewRingWriter(m *ring.Manager) RingWriter {
	return &ringWriterAdapter{manager: m}
}

// .
// .
func NewPersistingRingWriter(m *ring.Manager, p RingPersister) RingWriter {
	return &ringWriterAdapter{manager: m, persister: p}
}

// .
func NewBriefWriter(m *ring.Manager) BriefWriter {
	return &ringWriterAdapter{manager: m}
}

// .
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
			// .
			// .
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
