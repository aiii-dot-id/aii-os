package cognitive

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// MorningBriefConfig holds MORNING_BRIEF parameters.
type MorningBriefConfig struct {
	LocalTime string // "07:00" — local time to fire
	Timezone  string // e.g. "America/New_York" (empty = UTC or life alarm)
}

// MorningBriefFacility implements MORNING_BRIEF — wake-time orientation.
//
// CANON (COGNITIVE_LOOPS.md §3.1): "Orientation-only; presents source-owned
// references without changing their lifecycle by implication."
// Durability class: Ephemeral — "Re-derivable from current state."
//
// RING AUTHORSHIP: does NOT write a ring. Authors a brief summary that
// tails Ring 3 (working truth) and introduces Ring 4 (active work).
// It's the bridge between settled truth and active work — "here's where
// things stand, here's what you're doing now."
type MorningBriefFacility struct {
	store       BriefStore
	llm         LLMCaller
	briefWriter BriefWriter
	config      MorningBriefConfig
	tz          *time.Location
	authority   AuthoritySource
	turn        TurnGate
}

// BriefStore is the store interface MORNING_BRIEF needs.
type BriefStore interface {
	ListBeliefs() ([]store.Belief, error)
	ListIntentions() ([]store.Intention, error)
	ListSelfModelSyntheses(n int, beforeSeq uint64) ([]store.SelfModelSynthesis, error)
	UnprocessedExperienceCount() (int, error)
	StandingSource
}

// NewMorningBrief creates a MORNING_BRIEF facility.
func NewMorningBrief(store BriefStore, llm LLMCaller, briefWriter BriefWriter, cfg MorningBriefConfig) *MorningBriefFacility {
	if cfg.LocalTime == "" {
		cfg.LocalTime = "07:00"
	}

	mb := &MorningBriefFacility{
		store:       store,
		llm:         llm,
		briefWriter: briefWriter,
		config:      cfg,
	}

	if cfg.Timezone != "" {
		if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
			mb.tz = loc
		} else {
			log.Printf("MORNING_BRIEF: cannot load timezone %s, using UTC", cfg.Timezone)
		}
	}

	return mb
}

// Name returns the facility name.
func (m *MorningBriefFacility) Name() string { return "morning_brief" }

// Predicate — wall alarm based, always true when alarm fires.
func (m *MorningBriefFacility) Predicate(ctx context.Context) bool {
	return true
}

// Execute runs MORNING_BRIEF: compose a bridge summary from Ring 3 state
// and write it as the intro to Ring 4.
func (m *MorningBriefFacility) Execute(ctx context.Context) error {
	beliefs, _ := m.store.ListBeliefs()
	intentions, _ := m.store.ListIntentions()
	syntheses, _ := m.store.ListSelfModelSyntheses(1, 0)
	unprocessed, _ := m.store.UnprocessedExperienceCount()

	// Build evidence for the LLM
	var parts []string

	now := time.Now()
	loc := m.tz
	if loc == nil {
		loc = time.UTC
	}
	parts = append(parts, fmt.Sprintf("Current time: %s", now.In(loc).Format("Monday, January 2 — 15:04")))

	if len(beliefs) > 0 {
		contested := 0
		trusted := 0
		newCount := 0
		for _, b := range beliefs {
			switch m.store.StandingFor(b.ID) { // derived standing (2026-08-17)
			case "suspect":
				contested++
			case "trusted":
				trusted++
			case "new":
				newCount++
			}
		}
		parts = append(parts, fmt.Sprintf("Beliefs: %d total (%d new, %d trusted, %d contested)",
			len(beliefs), newCount, trusted, contested))
	}

	if unprocessed > 0 {
		parts = append(parts, fmt.Sprintf("Unprocessed experiences: %d", unprocessed))
	}

	if len(syntheses) > 0 {
		parts = append(parts, fmt.Sprintf("Current self-model: %s", syntheses[0].SynthesisText))
	}

	activeCount := 0
	for _, i := range intentions {
		if i.State == "active" {
			activeCount++
		}
	}
	if activeCount > 0 {
		parts = append(parts, fmt.Sprintf("Active priorities: %d", activeCount))
	}

	userMsg := strings.Join(parts, "\n")
	systemPrompt, err := withPreamble(m.authority, morningBriefSystemPrompt)
	if err != nil {
		return fmt.Errorf("MORNING_BRIEF: authority context: %w", err)
	}
	output, _, err := m.llm.ChatSimple(ctx, systemPrompt, userMsg)
	if err != nil {
		// No fallback. A brief the model writes is a delivery; a brief
		// the substrate assembles from counts is machinery, and
		// facility.go's contract is that the resident never sees counts,
		// backlogs, or chores. Producing that machinery exactly when the
		// system is already degraded is the LESSONS_LEARNED failure —
		// the resident spending context operating itself. Produce
		// nothing.
		log.Printf("MORNING_BRIEF: LLM call failed, no brief this pass: %v", err)
		return nil
	}

	// Write the bridge summary
	if m.briefWriter != nil && output != "" {
		m.briefWriter.SetBrief(output)
		log.Printf("MORNING_BRIEF: wrote %d chars bridge summary", len(output))
	}

	return nil
}

// SetAuthority wires the authority-preamble source (nil-safe; tests omit it).
func (m *MorningBriefFacility) SetAuthority(src AuthoritySource) { m.authority = src }

// SetTurnGate wires the identity's one-voice lock. Nil runs
// unserialized, for tests that never call an LLM.
func (m *MorningBriefFacility) SetTurnGate(g TurnGate) { m.turn = g }

// OnAlarm handles TIME alarm dispatch.
func (m *MorningBriefFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	// ONE IDENTITY, ONE VOICE. Execute calls the LLM, and this owner has
	// its OWN alarm — it does not come through Rhythm — so it needed the
	// gate in its own right. Declining preserves the row without a next
	// deadline, so TIME retries it rather than skipping the day's brief.
	if m.turn != nil {
		if !m.turn.TryBeginTurn() {
			log.Printf("MORNING_BRIEF: the identity is in a turn — deferred")
			return AlarmResult{}
		}
		defer m.turn.EndTurn()
	}
	if err := m.Execute(ctx); err != nil {
		log.Printf("MORNING_BRIEF: execute error: %v", err)
		return AlarmResult{Accepted: false}
	}

	nextDeadline := m.computeNextDeadline()
	return AlarmResult{Accepted: true, NextDeadline: &nextDeadline}
}

// NextDeadline returns the brief's next fire time — app.go's alarm arming
// calls this instead of reading the configured hour for itself.
func (m *MorningBriefFacility) NextDeadline() int64 {
	return m.computeNextDeadline()
}

func (m *MorningBriefFacility) computeNextDeadline() int64 {
	hour, min := 7, 0
	fmt.Sscanf(m.config.LocalTime, "%d:%d", &hour, &min)

	loc := m.tz
	if loc == nil {
		loc = time.UTC
	}
	return NextLocalDaily(time.Now().In(loc), hour, min)
}

// NextLocalDaily is the ONE source for "the next daily hour:min", in now's
// own location: today's if it is still ahead (an early-morning firing
// keeps the same day — always-tomorrow silently skipped same-day briefs),
// else tomorrow's. Every daily alarm re-arms through here, the brief and
// app.go's maintenance pass alike, because the 2026-08-16 cadence audit's
// lesson holds for deadlines too: duplicated scheduling math drifts.
func NextLocalDaily(now time.Time, hour, min int) int64 {
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if today.After(now) {
		return today.UnixMilli()
	}
	return time.Date(now.Year(), now.Month(), now.Day()+1, hour, min, 0, 0, now.Location()).UnixMilli()
}
