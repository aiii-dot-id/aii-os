package cognitive

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type MorningBriefConfig struct {
	LocalTime string
	Timezone  string

	MaxChars int

	Spacing time.Duration
}

const (
	DefaultMorningBriefAt = "07:00"
	MorningBriefOff       = "off"
)

const DefaultBriefMaxChars = 1200

func ParseBriefAt(at string) (hour, min int, off bool, err error) {
	if at == MorningBriefOff {
		return 0, 0, true, nil
	}
	t, perr := time.Parse("15:04", at)
	if perr != nil {
		return 0, 0, false, fmt.Errorf("want HH:MM on a 24-hour clock, or %q", MorningBriefOff)
	}
	return t.Hour(), t.Minute(), false, nil
}

type MorningBriefFacility struct {
	store       BriefStore
	llm         LLMCaller
	briefWriter BriefWriter

	config atomic.Pointer[briefSettings]

	briefMu   sync.Mutex
	cleared   uint64
	teller    *Teller
	authority AuthoritySource
	turn      TurnGate

	attention func(ctx context.Context) ([]attention.AttentionItem, error)
}

func (m *MorningBriefFacility) SetAttention(fn func(ctx context.Context) ([]attention.AttentionItem, error)) {
	m.attention = fn
}

type BriefStore interface {
	ListIntentions() ([]rows.Intention, error)
	ListSelfModelSyntheses(n int, beforeSeq uint64) ([]rows.SelfModelSynthesis, error)

	ListExperiencesSince(after time.Time, n int) ([]rows.Experience, error)
}

const (
	briefWindow  = 24 * time.Hour
	briefDiffCap = 30
)

type briefSettings struct {
	MorningBriefConfig
	tz *time.Location
}

func NewMorningBrief(store BriefStore, llm LLMCaller, briefWriter BriefWriter, cfg MorningBriefConfig) *MorningBriefFacility {
	mb := &MorningBriefFacility{
		store:       store,
		llm:         llm,
		briefWriter: briefWriter,
	}
	mb.Reconfigure(cfg)
	return mb
}

func (m *MorningBriefFacility) Reconfigure(cfg MorningBriefConfig) (alarmChanged bool) {
	if cfg.LocalTime == "" {
		cfg.LocalTime = DefaultMorningBriefAt
	}
	if _, _, _, err := ParseBriefAt(cfg.LocalTime); err != nil {
		logsink.Warn("brief.error", "the brief's hour %q cannot be read (%v) — %s is used", cfg.LocalTime, err, DefaultMorningBriefAt)
		cfg.LocalTime = DefaultMorningBriefAt
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = DefaultBriefMaxChars
	}
	if cfg.Spacing <= 0 {
		cfg.Spacing = DefaultSpacing
	}
	next := &briefSettings{MorningBriefConfig: cfg}
	if cfg.Timezone != "" {
		if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
			next.tz = loc
		} else {
			logsink.Warn("brief.error", "cannot load timezone %s, using UTC", cfg.Timezone)
		}
	}
	m.briefMu.Lock()
	defer m.briefMu.Unlock()
	prev := m.config.Swap(next)
	if next.LocalTime == MorningBriefOff {
		m.cleared++
		if m.briefWriter != nil {
			m.briefWriter.SetBrief("")
		}
	}
	return prev == nil || prev.LocalTime != next.LocalTime || prev.Timezone != next.Timezone
}

func (m *MorningBriefFacility) SetTeller(t *Teller) { m.teller = t }

func (m *MorningBriefFacility) clearings() uint64 {
	m.briefMu.Lock()
	defer m.briefMu.Unlock()
	return m.cleared
}

func (m *MorningBriefFacility) Off() bool { return m.config.Load().LocalTime == MorningBriefOff }

func (m *MorningBriefFacility) Name() string { return "morning_brief" }

func (m *MorningBriefFacility) Predicate(ctx context.Context) bool {
	return true
}

type briefCallFailed struct{ again time.Duration }

func (*briefCallFailed) Error() string { return "the brief's model call failed" }

func (m *MorningBriefFacility) Execute(ctx context.Context) error {
	var failed *briefCallFailed
	if err := m.pass(ctx); err != nil && !errors.As(err, &failed) {
		return err
	}
	return nil
}

func (m *MorningBriefFacility) pass(ctx context.Context) error {

	m.briefMu.Lock()
	clearedBefore, cfg := m.cleared, m.config.Load()
	m.briefMu.Unlock()
	if cfg.LocalTime == MorningBriefOff {
		logsink.Info("brief.decision", "the morning brief is off (cognition.morning_brief_at) — not written")
		return nil
	}

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

	day := briefDay{experiences: experiences}
	loc := cfg.tz
	if loc == nil {
		loc = time.UTC
	}
	day.now = fmt.Sprintf("Current time: %s", now.In(loc).Format("Monday, January 2 — 15:04"))
	for _, i := range intentions {
		if i.State == "active" {
			day.intentions = append(day.intentions, "- "+i.Statement)
		}
	}

	if m.attention != nil {
		if items, err := m.attention(ctx); err != nil {
			logsink.Warn("brief.error", "attention unreadable, the brief goes without it: %v", err)
		} else if low := attention.OfCost(items, attention.CostLow); len(low) > 0 {
			day.attention = attention.RenderAttention(low)
		}
	}
	if len(syntheses) > 0 {
		day.portrait = syntheses[0].SynthesisText
	}

	check := simpleCheck(m.llm)
	callCtx, systemPrompt, err := fittedPreamble(ctx, m.authority, morningBriefPrompt(cfg.MaxChars), day.render(briefView{}), nil, check)
	if err != nil {
		return fmt.Errorf("MORNING_BRIEF: authority context: %w", err)
	}
	full := briefView{experiences: len(day.experiences), intentions: len(day.intentions)}
	view := full

	fitted := func(after string) (user string, sendable bool, err error) {
		view = full
		if check != nil {
			ask := &admission{check: check, ctx: callCtx, system: systemPrompt}
			admits := func(v briefView) bool { return ask.admits(day.render(v) + after) }
			if !admits(view) {
				k, ok := largestAdmitted(len(day.experiences), func(k int) bool { view.experiences = k; return admits(view) })
				view.experiences = k
				if !ok {
					view.intentions, ok = largestAdmitted(len(day.intentions), func(n int) bool { view.intentions = n; return admits(view) })
				}
				if ask.err != nil {
					return "", false, fmt.Errorf("MORNING_BRIEF: %w", ask.err)
				}
				if !ok {
					m.teller.say(Condition{Facility: "brief", Kind: ConditionNoRoom, Said: "the brief's smallest request does not fit the model's input budget beside what every request carries — no brief this morning; yesterday's stands"})
					return "", false, nil
				}
			}
		}
		return day.render(view) + after, true, nil
	}
	userMsg, sendable, err := fitted("")
	if err != nil || !sendable {
		return err
	}
	output, _, err := m.llm.ChatSimple(wire.WithTapSource(callCtx, "brief"), systemPrompt, userMsg)
	if err != nil {

		if refusedForWhatWasAsked(err) {

			m.teller.say(Condition{Facility: "brief", Kind: ConditionRefused,
				Said: fmt.Sprintf("this morning's brief was refused for what it asked (%v) — no brief this morning; yesterday's stands, and tomorrow's is asked for as this one was", err)})
			return nil
		}
		logsink.Warn("brief.error", "LLM call failed, no brief this pass: %v", err)
		return &briefCallFailed{again: cfg.Spacing}
	}

	if first := utf8.RuneCountInString(output); first > cfg.MaxChars {
		if m.clearings() != clearedBefore {

			logsink.Info("brief.decision", "the morning brief was turned off while this one was being written — it is not kept")
			return nil
		}
		userMsg, sendable, err = fitted(fmt.Sprintf("\n\nThe brief you wrote from this was %d characters, over its bound of %d, and was not kept. Write it again now, shorter: at most %d characters.", first, cfg.MaxChars, cfg.MaxChars))
		if err != nil || !sendable {
			return err
		}
		if output, _, err = m.llm.ChatSimple(wire.WithTapSource(callCtx, "brief"), systemPrompt, userMsg); err != nil {

			m.teller.say(Condition{Facility: "brief", Kind: ConditionRefused,
				Said: fmt.Sprintf("this morning's brief was %d characters, over the %d-character bound (prompt.brief_max_chars), and the one ask for a shorter one failed (%v) — the brief written was not kept, and yesterday's stands", first, cfg.MaxChars, err)})
			return nil
		}
		if n := utf8.RuneCountInString(output); n > cfg.MaxChars {
			m.teller.say(Condition{Facility: "brief", Kind: ConditionRefused,
				Said: fmt.Sprintf("this morning's brief was %d characters and, asked once more for a shorter one, %d — over the %d-character bound (prompt.brief_max_chars); refused whole, and yesterday's stands", first, n, cfg.MaxChars)})
			return nil
		}
	}

	if m.briefWriter != nil && output != "" {
		m.briefMu.Lock()
		if m.cleared != clearedBefore {

			m.briefMu.Unlock()
			logsink.Info("brief.decision", "the morning brief was turned off while this one was being written — it is not kept")
			return nil
		}
		m.briefWriter.SetBrief(output)
		m.briefMu.Unlock()
		logsink.Info("brief.end", "wrote %d chars bridge summary", len(output))

		if view == full {
			m.teller.say(Condition{Facility: "brief", Kind: ConditionWhole, Said: "this morning's brief was drawn from the whole of the day"})
		} else {
			m.teller.say(Condition{Facility: "brief", Kind: ConditionFitted, Said: fmt.Sprintf("this morning's brief was drawn from %d of %d records of the day and %d of %d active intentions, to fit the model's input budget", view.experiences, len(day.experiences), view.intentions, len(day.intentions))})
		}
	}

	return nil
}

type briefDay struct {
	now         string
	experiences []rows.Experience
	intentions  []string
	attention   string
	portrait    string
}

type briefView struct{ experiences, intentions int }

func (d briefDay) render(v briefView) string {
	parts := []string{d.now}
	switch {
	case len(d.experiences) == 0:
		parts = append(parts, "Since this time yesterday: nothing was recorded.")
	case v.experiences == 0:
		parts = append(parts, fmt.Sprintf("Since this time yesterday: %d records, none in view for want of room — recall reaches them.", len(d.experiences)))
	default:
		parts = append(parts, "Since this time yesterday, newest first:")
		for _, e := range d.experiences[:v.experiences] {
			label := e.Category
			if label == "" {
				label = "observation"
			}
			if e.Provenance != "" && e.Provenance != "self" {
				label += " · " + e.Provenance
			}
			parts = append(parts, fmt.Sprintf("- [%s] %s", label, evidenceText(e)))
		}
		if v.experiences < len(d.experiences) {
			parts = append(parts, fmt.Sprintf("(the %d most recent of %d shown, for want of room; older items of the day are not in view — recall reaches them)", v.experiences, len(d.experiences)))
		} else if len(d.experiences) == briefDiffCap {

			parts = append(parts, fmt.Sprintf("(the %d most recent shown; older items of the day are not in view — recall reaches them)", briefDiffCap))
		}
	}
	if v.intentions > 0 {
		parts = append(parts, "Active intentions:")
		parts = append(parts, d.intentions[:v.intentions]...)
	}
	if v.intentions < len(d.intentions) {
		parts = append(parts, fmt.Sprintf("(%d of %d active intentions shown, for want of room; recall reaches the rest)", v.intentions, len(d.intentions)))
	}
	if d.attention != "" {
		parts = append(parts, "The record is holding these — reconfirm, advance, or let go; none is a chore:")
		parts = append(parts, d.attention)
	}
	if d.portrait != "" {
		parts = append(parts, fmt.Sprintf("Current self-model: %s", d.portrait))
	}
	return strings.Join(parts, "\n")
}

func (m *MorningBriefFacility) SetAuthority(src AuthoritySource) { m.authority = src }

func (m *MorningBriefFacility) SetTurnGate(g TurnGate) { m.turn = g }

func (m *MorningBriefFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {

	if m.turn != nil {
		if !m.turn.TryBeginTurn() {
			logsink.Info("brief.refusal", "the identity is in a turn — deferred")
			return AlarmResult{}
		}
		defer m.turn.EndTurn()
	}
	err := m.pass(ctx)
	nextDeadline := m.computeNextDeadline()
	var failed *briefCallFailed
	if errors.As(err, &failed) {

		if retry := time.Now().Add(failed.again).UnixMilli(); retry < nextDeadline {
			logsink.Info("brief.refusal", "the brief's model call failed — it is asked for again at %s (cognition.consolidate_spacing_seconds), and the brief that stood stands until then", time.UnixMilli(retry).UTC().Format("15:04 UTC"))
			return AlarmResult{Accepted: false, NextDeadline: &retry}
		}
		err = nil
	}
	if err != nil {
		logsink.Warn("brief.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true, NextDeadline: &nextDeadline}
}

const briefHeld = 100 * 365 * 24 * time.Hour

func (m *MorningBriefFacility) NextDeadline() int64 {
	return m.computeNextDeadline()
}

func (m *MorningBriefFacility) computeNextDeadline() int64 {
	cfg := m.config.Load()
	hour, min, off, err := ParseBriefAt(cfg.LocalTime)
	if off {

		return time.Now().Add(briefHeld).UnixMilli()
	}
	if err != nil {
		hour, min, _, _ = ParseBriefAt(DefaultMorningBriefAt)
	}
	loc := cfg.tz
	if loc == nil {
		loc = time.UTC
	}
	return NextLocalDaily(time.Now().In(loc), hour, min)
}

func NextLocalDaily(now time.Time, hour, min int) int64 {
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, min, 0, 0, now.Location())
	if today.After(now) {
		return today.UnixMilli()
	}
	return time.Date(now.Year(), now.Month(), now.Day()+1, hour, min, 0, 0, now.Location()).UnixMilli()
}
