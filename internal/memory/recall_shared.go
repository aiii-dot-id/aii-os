package memory

import (
	"math"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type RecallFacts struct {
	Ring int

	EvidenceCount int

	Born time.Time

	Accesses int64

	Archived   bool
	Superseded bool

	Footing store.BeliefFooting
}

type RecallReading struct {
	Strength *float64

	Fading bool

	Judged bool
}

func RecallEligible(f RecallFacts) bool {
	return f.Ring == 3 && !f.Archived && !f.Superseded
}

func BeliefRecall(f RecallFacts, now time.Time) RecallReading {
	judged := RecallEligible(f)
	if f.Born.IsZero() {
		return RecallReading{Judged: judged}
	}
	class := BeliefClass(f.Ring, f.EvidenceCount, f.Footing)
	ageDays := now.Sub(f.Born).Hours() / 24
	if math.IsNaN(ageDays) || math.IsInf(ageDays, 0) {
		return RecallReading{Judged: judged}
	}
	strength, ok := carrd.Strength(class, ageDays, f.Accesses)
	if !ok {
		return RecallReading{Judged: judged}
	}
	return RecallReading{
		Strength: &strength,
		Fading:   judged && !f.Footing.Stands && !f.Footing.Suspect && strength < DecayAlertStrength,
		Judged:   judged,
	}
}

func BeliefClass(ring, evidence int, footing store.BeliefFooting) carrd.Class {
	switch {
	case ring > 0 && ring <= 2:
		return carrd.Constitutional
	case footing.Core:
		return carrd.Core
	case evidence > 0:
		return carrd.Standard
	}
	return carrd.Operational
}
