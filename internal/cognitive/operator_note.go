package cognitive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type NoteWriter interface {
	SetOperatorNote(content string)
	ClearOperatorNote()
	LastWindowUntil() time.Time
}

type NoteOutcome struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Outcome string    `json:"outcome"`
	Summary string    `json:"summary"`
	At      time.Time `json:"at"`
}

type NoteClaim struct {
	Sentence string   `json:"sentence"`
	Entries  []uint64 `json:"entries"`
	Outcomes []string `json:"outcomes"`
}

type KeptNote struct {
	Note        string      `json:"note"`
	Claims      []NoteClaim `json:"claims"`
	Unchecked   int         `json:"unchecked"`
	WindowFrom  time.Time   `json:"window_from"`
	WindowUntil time.Time   `json:"window_until"`

	Coverage NoteCoverage `json:"coverage"`
}

type NoteCoverage struct {
	Experiences      int  `json:"experiences"`
	ExperiencesShown int  `json:"experiences_shown"`
	Outcomes         int  `json:"outcomes"`
	OutcomesShown    int  `json:"outcomes_shown"`
	OutcomesBeyond   bool `json:"outcomes_beyond,omitempty"`
}

type NoteOutcomesReader func(ctx context.Context, since, until time.Time) (read []NoteOutcome, beyond bool, err error)

func (m *MorningBriefFacility) SetNote(w NoteWriter, outcomes NoteOutcomesReader) {
	m.note, m.outcomes = w, outcomes
}

type noteDay struct {
	now         string
	since       string
	experiences []rows.Experience
	intentions  []rows.Intention
	portrait    rows.SelfModelSynthesis
	outcomes    []NoteOutcome
	beyond      bool
}

func (d noteDay) shownOutcomes(k int) []NoteOutcome {
	return d.outcomes[:k]
}

func (d noteDay) render(experiences, outcomes int) string {
	parts := []string{d.now}
	switch {
	case len(d.experiences) == 0:
		parts = append(parts, "Since this time yesterday: nothing was recorded.")
	case experiences == 0:
		parts = append(parts, fmt.Sprintf("Since this time yesterday: %d records, none in view for want of room.", len(d.experiences)))
	default:
		parts = append(parts, "Since this time yesterday, newest first, each with its record entry:")
		for _, e := range d.experiences[:experiences] {
			parts = append(parts, fmt.Sprintf("- #%d %s", e.CreatedSeq, evidenceText(e)))
		}
		if experiences < len(d.experiences) {
			parts = append(parts, fmt.Sprintf("(the %d most recent of %d shown, for want of room)", experiences, len(d.experiences)))
		} else if len(d.experiences) == briefDiffCap {
			parts = append(parts, fmt.Sprintf("(the %d most recent shown)", briefDiffCap))
		}
	}
	if len(d.intentions) > 0 {
		parts = append(parts, "Active intentions, each with its record entry:")
		for _, i := range d.intentions {
			parts = append(parts, fmt.Sprintf("- #%d %s", i.CreatedSeq, i.Statement))
		}
	}
	switch {
	case len(d.outcomes) == 0:
	case outcomes == 0 && d.beyond:
		parts = append(parts, fmt.Sprintf(noteOutcomesNoneShownBeyond, d.since, len(d.outcomes)))
	case outcomes == 0:
		parts = append(parts, fmt.Sprintf(noteOutcomesNoneShown, d.since, len(d.outcomes)))
	default:
		parts = append(parts, fmt.Sprintf("What did not succeed since %s, each with its outcome id:", d.since))
		for _, o := range d.shownOutcomes(outcomes) {
			parts = append(parts, fmt.Sprintf("- %s: %s %s at %s — %s", o.ID, o.Kind, o.Outcome, o.At.UTC().Format(time.RFC3339), o.Summary))
		}
		switch {
		case d.beyond && outcomes < len(d.outcomes):
			parts = append(parts, fmt.Sprintf(noteOutcomesBeyondTrimmed, outcomes, len(d.outcomes)))
		case d.beyond:
			parts = append(parts, noteOutcomesBeyond)
		case outcomes < len(d.outcomes):
			parts = append(parts, fmt.Sprintf(noteOutcomesTrimmed, outcomes, len(d.outcomes)))
		}
	}
	switch {
	case d.portrait.SynthesisText != "" && d.portrait.CreatedSeq != 0:
		parts = append(parts, fmt.Sprintf("Your current self-model, record entry #%d: %s", d.portrait.CreatedSeq, d.portrait.SynthesisText))
	case d.portrait.SynthesisText != "":
		parts = append(parts, fmt.Sprintf("Your current self-model: %s", d.portrait.SynthesisText))
	}
	return strings.Join(parts, "\n")
}

const (
	noteOutcomesNoneShown       = "What did not succeed since %s: %d records, none in view for want of room."
	noteOutcomesNoneShownBeyond = "What did not succeed since %s: more than %d records, none in view for want of room; the Record page lists them all."
	noteOutcomesTrimmed         = "(the %d most recent of %d shown, for want of room)"
	noteOutcomesBeyond          = "(more did not succeed in this span than are listed here; the Record page lists them all)"
	noteOutcomesBeyondTrimmed   = "(the %d most recent shown, for want of room, of more than %d that did not succeed in this span; the Record page lists them all)"
)

func spanStart(since, now time.Time, loc *time.Location) string {
	if now.Sub(since) <= briefWindow+time.Hour {
		return "this time yesterday"
	}
	return since.In(loc).Format("Monday, January 2 at 15:04")
}

func checkNote(reply string, entries map[uint64]bool, outcomes map[string]bool, maxChars int) (kept KeptNote, ok bool, over int) {
	text := strings.TrimSpace(reply)
	var got struct {
		Note   *string     `json:"note"`
		Claims []NoteClaim `json:"claims"`
	}

	if err := canonicaljson.DecodeStrict([]byte(text), &got); err != nil || got.Note == nil || strings.TrimSpace(*got.Note) == "" {
		return KeptNote{}, false, 0
	}
	kept.Note = *got.Note
	if n := utf8.RuneCountInString(kept.Note); n > maxChars {
		return KeptNote{}, true, n
	}
	kept.Claims = []NoteClaim{}
claims:
	for _, c := range got.Claims {
		if c.Sentence == "" || !strings.Contains(kept.Note, c.Sentence) {
			kept.Unchecked++
			continue
		}
		for _, e := range c.Entries {
			if !entries[e] {
				kept.Unchecked++
				continue claims
			}
		}
		for _, o := range c.Outcomes {
			if !outcomes[o] {
				kept.Unchecked++
				continue claims
			}
		}
		if c.Entries == nil {
			c.Entries = []uint64{}
		}
		if c.Outcomes == nil {
			c.Outcomes = []string{}
		}
		kept.Claims = append(kept.Claims, c)
	}
	return kept, true, 0
}

func (m *MorningBriefFacility) writeNote(ctx context.Context, cfg *briefSettings, now time.Time, experiences []rows.Experience, intentions []rows.Intention, portrait rows.SelfModelSynthesis, clearedBefore uint64) {
	if m.note == nil || m.outcomes == nil {
		return
	}

	since := now.Add(-briefWindow)
	if last := m.note.LastWindowUntil(); !last.IsZero() && last.Before(now) {
		since = last.Add(time.Nanosecond)
	}
	outcomes, beyond, err := m.outcomes(ctx, since, now)
	if err != nil {

		m.teller.say(Condition{Facility: "operator_note", Kind: ConditionRefused, Said: fmt.Sprintf("the note to the operator was not written: what did not succeed could not be read (%v); yesterday's stands", err)})
		return
	}
	loc := cfg.tz
	if loc == nil {
		loc = time.UTC
	}
	day := noteDay{now: fmt.Sprintf("Current time: %s", now.In(loc).Format("Monday, January 2 — 15:04")), since: spanStart(since, now, loc), experiences: experiences, portrait: portrait, outcomes: outcomes, beyond: beyond}
	for _, i := range intentions {
		if i.State == "active" {
			day.intentions = append(day.intentions, i)
		}
	}
	check := simpleCheck(m.llm)
	callCtx, system, err := fittedPreamble(ctx, m.authority, operatorNotePrompt(cfg.MaxChars, day.since), day.render(0, 0), nil, check)
	if err != nil {
		logsink.Warn("operator_note.error", "the note to the operator was not written: authority context: %v", err)
		return
	}

	shown, failed := len(day.experiences), len(day.outcomes)
	ask := func(after string) (string, bool) {
		shown, failed = len(day.experiences), len(day.outcomes)
		if check != nil {
			a := &admission{check: check, ctx: callCtx, system: system}
			if !a.admits(day.render(shown, failed) + after) {
				k, ok := largestAdmitted(len(day.experiences), func(k int) bool { return a.admits(day.render(k, failed) + after) })
				if ok {
					shown = k
				} else {
					shown = 0
					failed, ok = largestAdmitted(len(day.outcomes), func(k int) bool { return a.admits(day.render(0, k) + after) })
				}

				if a.err != nil {
					logsink.Warn("operator_note.error", "the note to the operator was not written: %v; yesterday's stands", a.err)
					return "", false
				}
				if !ok {
					m.teller.say(Condition{Facility: "operator_note", Kind: ConditionNoRoom, Said: "the note's smallest request does not fit the model's input budget — no note this morning; yesterday's stands"})
					return "", false
				}
			}
		}
		user := day.render(shown, failed) + after
		reply, _, err := m.llm.ChatSimple(wire.WithTapSource(callCtx, "operator_note"), system, user)
		if err != nil {
			m.teller.say(Condition{Facility: "operator_note", Kind: ConditionRefused, Said: fmt.Sprintf("the note to the operator's model call failed (%v) — no note this morning; yesterday's stands", err)})
			return "", false
		}
		return reply, true
	}

	admitted := func() map[uint64]bool {
		entries := map[uint64]bool{}
		for _, i := range day.intentions {
			entries[i.CreatedSeq] = true
		}
		if day.portrait.SynthesisText != "" && day.portrait.CreatedSeq != 0 {
			entries[day.portrait.CreatedSeq] = true
		}
		for _, e := range day.experiences[:shown] {
			entries[e.CreatedSeq] = true
		}
		return entries
	}

	ids := func() map[string]bool {
		out := map[string]bool{}
		for _, o := range day.shownOutcomes(failed) {
			out[o.ID] = true
		}
		return out
	}
	reply, sent := ask("")
	if !sent {
		return
	}
	kept, ok, over := checkNote(reply, admitted(), ids(), cfg.MaxChars)
	if !ok || over > 0 {
		if m.clearings() != clearedBefore {
			return
		}
		again := "\n\nWhat you returned was not the JSON asked for. Return only that JSON now."
		if over > 0 {
			again = fmt.Sprintf("\n\nThe note you wrote was %d characters, over its bound of %d, and was not kept. Write it again now, shorter: at most %d characters, as the same JSON.", over, cfg.MaxChars, cfg.MaxChars)
		}
		if reply, sent = ask(again); !sent {
			return
		}
		var second int
		if kept, ok, second = checkNote(reply, admitted(), ids(), cfg.MaxChars); !ok || second > 0 {
			m.teller.say(Condition{Facility: "operator_note", Kind: ConditionRefused, Said: "the note to the operator was asked for twice and neither reply was a note within its bound (prompt.brief_max_chars) in the JSON asked for — refused whole; yesterday's stands"})
			return
		}
	}
	kept.WindowFrom, kept.WindowUntil = since, now
	kept.Coverage = NoteCoverage{Experiences: len(day.experiences), ExperiencesShown: shown, Outcomes: len(day.outcomes), OutcomesShown: failed, OutcomesBeyond: day.beyond}
	content, err := json.Marshal(kept)
	if err != nil {
		logsink.Warn("operator_note.error", "the note to the operator could not be kept: %v", err)
		return
	}
	m.briefMu.Lock()
	defer m.briefMu.Unlock()
	if m.cleared != clearedBefore {
		logsink.Info("operator_note.decision", "the morning brief was turned off while the note was being written — it is not kept")
		return
	}
	m.note.SetOperatorNote(string(content))
	logsink.Info("operator_note.end", "wrote the note to the operator: %d chars, %d claim(s) kept, %d dropped", utf8.RuneCountInString(kept.Note), len(kept.Claims), kept.Unchecked)
}
