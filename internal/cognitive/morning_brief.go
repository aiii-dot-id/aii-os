package cognitive

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type MorningBriefConfig struct {
	LocalTime string
	Timezone  string
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
type MorningBriefFacility struct {
	store       BriefStore
	llm         LLMCaller
	briefWriter BriefWriter
	config      MorningBriefConfig
	tz          *time.Location
	authority   AuthoritySource
	turn        TurnGate
	// .
	// .
	attention func(ctx context.Context) ([]memory.AttentionItem, error)
}

// .
func (m *MorningBriefFacility) SetAttention(fn func(ctx context.Context) ([]memory.AttentionItem, error)) {
	m.attention = fn
}

// .
type BriefStore interface {
	ListIntentions() ([]store.Intention, error)
	ListSelfModelSyntheses(n int, beforeSeq uint64) ([]store.SelfModelSynthesis, error)
	// .
	// .
	ListExperiencesSince(after time.Time, n int) ([]store.Experience, error)
}

// .
// .
// .
const (
	briefWindow  = 24 * time.Hour
	briefDiffCap = 30
)

// .
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
			logsink.Warn("brief.error", "cannot load timezone %s, using UTC", cfg.Timezone)
		}
	}

	return mb
}

// .
func (m *MorningBriefFacility) Name() string { return "morning_brief" }

// .
func (m *MorningBriefFacility) Predicate(ctx context.Context) bool {
	return true
}

// .
// .
func (m *MorningBriefFacility) Execute(ctx context.Context) error {
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
	intentions, err := m.store.ListIntentions()
	if err != nil {
		return fmt.Errorf("morning brief: read intentions: %w", err)
	}
	syntheses, err := m.store.ListSelfModelSyntheses(1, 0)
	if err != nil {
		return fmt.Errorf("morning brief: read self-model syntheses: %w", err)
	}
	now := time.Now()
	since := now.Add(-briefWindow)
	experiences, err := m.store.ListExperiencesSince(since, briefDiffCap)
	if err != nil {
		return fmt.Errorf("morning brief: read experiences since %s: %w", since.UTC().Format(time.RFC3339), err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var parts []string
	loc := m.tz
	if loc == nil {
		loc = time.UTC
	}
	parts = append(parts, fmt.Sprintf("Current time: %s", now.In(loc).Format("Monday, January 2 — 15:04")))

	if len(experiences) == 0 {
		parts = append(parts, "Since this time yesterday: nothing was recorded.")
	} else {
		parts = append(parts, "Since this time yesterday, newest first:")
		for _, e := range experiences {
			label := e.Category
			if label == "" {
				label = "observation"
			}
			if e.Provenance != "" && e.Provenance != "self" {
				label += " · " + e.Provenance
			}
			parts = append(parts, fmt.Sprintf("- [%s] %s", label, evidenceText(e)))
		}
		if len(experiences) == briefDiffCap {
			// .
			parts = append(parts, fmt.Sprintf("(the %d most recent shown; older items of the day are not in view — recall reaches them)", briefDiffCap))
		}
	}

	var active []string
	for _, i := range intentions {
		if i.State == "active" {
			active = append(active, "- "+i.Statement)
		}
	}
	if len(active) > 0 {
		parts = append(parts, "Active intentions:")
		parts = append(parts, active...)
	}

	// .
	// .
	// .
	// .
	// .
	if m.attention != nil {
		if items, err := m.attention(ctx); err != nil {
			logsink.Warn("brief.error", "attention unreadable, the brief goes without it: %v", err)
		} else if low := memory.OfCost(items, memory.CostLow); len(low) > 0 {
			parts = append(parts, "The record is holding these — reconfirm, advance, or let go; none is a chore:")
			parts = append(parts, memory.RenderAttention(low))
		}
	}

	if len(syntheses) > 0 {
		parts = append(parts, fmt.Sprintf("Current self-model: %s", syntheses[0].SynthesisText))
	}

	userMsg := strings.Join(parts, "\n")
	callCtx, systemPrompt, err := withPreamble(ctx, m.authority, morningBriefSystemPrompt)
	if err != nil {
		return fmt.Errorf("MORNING_BRIEF: authority context: %w", err)
	}
	output, _, err := m.llm.ChatSimple(callCtx, systemPrompt, userMsg)
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		logsink.Warn("brief.error", "LLM call failed, no brief this pass: %v", err)
		return nil
	}

	// .
	if m.briefWriter != nil && output != "" {
		m.briefWriter.SetBrief(output)
		logsink.Info("brief.end", "wrote %d chars bridge summary", len(output))
	}

	return nil
}

// .
func (m *MorningBriefFacility) SetAuthority(src AuthoritySource) { m.authority = src }

// .
// .
func (m *MorningBriefFacility) SetTurnGate(g TurnGate) { m.turn = g }

// .
func (m *MorningBriefFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	// .
	// .
	// .
	// .
	if m.turn != nil {
		if !m.turn.TryBeginTurn() {
			logsink.Info("brief.refusal", "the identity is in a turn — deferred")
			return AlarmResult{}
		}
		defer m.turn.EndTurn()
	}
	if err := m.Execute(ctx); err != nil {
		logsink.Warn("brief.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}

	nextDeadline := m.computeNextDeadline()
	return AlarmResult{Accepted: true, NextDeadline: &nextDeadline}
}

// .
// .
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

// .
// .
// .
// .
// .
// .
func NextLocalDaily(now time.Time, hour, min int) int64 {
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if today.After(now) {
		return today.UnixMilli()
	}
	return time.Date(now.Year(), now.Month(), now.Day()+1, hour, min, 0, 0, now.Location()).UnixMilli()
}
