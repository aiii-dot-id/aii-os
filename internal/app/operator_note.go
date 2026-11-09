package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type operatorNoteKeeper struct {
	store interface {
		SaveOperatorNote(content string) error
		RemoveOperatorNote() error
		OperatorNote() (string, time.Time, error)
	}
}

func (k operatorNoteKeeper) SetOperatorNote(content string) {
	if err := k.store.SaveOperatorNote(content); err != nil {
		logsink.Warn("operator_note.error", "the note to the operator could not be kept: %v", err)
	}
}

func (k operatorNoteKeeper) LastWindowUntil() time.Time {
	content, _, err := k.store.OperatorNote()
	if err != nil || content == "" {
		return time.Time{}
	}
	var kept cognitive.KeptNote
	if json.Unmarshal([]byte(content), &kept) != nil {
		return time.Time{}
	}
	return kept.WindowUntil
}

func (k operatorNoteKeeper) ClearOperatorNote() {
	if err := k.store.RemoveOperatorNote(); err != nil {
		logsink.Warn("operator_note.error", "the note to the operator could not be taken away: %v", err)
	}
}

const noteOutcomesPages, noteOutcomesShown = 40, 50

func (a *App) outcomesBetween(ctx context.Context, since, until time.Time) (out []cognitive.NoteOutcome, beyond bool, err error) {
	out = []cognitive.NoteOutcome{}

	f := interaction.Filter{Kinds: []interaction.Kind{interaction.ToolResult, interaction.Notice, interaction.Legacy}, From: since.UTC().Format(time.RFC3339Nano)}
	if !until.IsZero() {
		f.Until = until.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano)
	}
	q := interaction.Query{Version: 1, Source: "recorded", Filter: f, Limit: 50, Outcomes: []interaction.Outcome{interaction.Failed, interaction.Unknown, interaction.Cancelled, interaction.Refused}}
	for page := 0; ; page++ {
		if page == noteOutcomesPages {
			return out, true, nil
		}
		p, err := a.QueryInteractions(ctx, q)
		if err != nil {
			return nil, false, err
		}
		for _, r := range p.Rows {
			switch r.Outcome {
			case interaction.Failed, interaction.Unknown, interaction.Cancelled, interaction.Refused:
				at, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
				out = append(out, cognitive.NoteOutcome{ID: r.ID, Kind: string(r.Kind), Outcome: string(r.Outcome), Summary: outcomeSummary(r), At: at})
			}
		}
		if p.NextCursor == "" {
			return out, false, nil
		}
		q.Cursor = p.NextCursor
	}
}

func outcomeSummary(r interaction.Record) string {
	words := r.Content
	if utf8.RuneCountInString(words) > 160 {
		words = string([]rune(words)[:160]) + " …"
	}
	if r.Details.Tool != "" {
		return r.Details.Tool + ": " + words
	}
	return words
}

func (a *App) operatorNoteView(ctx context.Context) (*dashboard.OperatorNote, error) {
	content, written, err := a.store.OperatorNote()
	if err != nil || content == "" {
		return nil, err
	}
	var kept cognitive.KeptNote
	if err := json.Unmarshal([]byte(content), &kept); err != nil {
		return nil, fmt.Errorf("the kept note cannot be read: %w", err)
	}
	view := &dashboard.OperatorNote{Text: kept.Note, Unchecked: kept.Unchecked, Claims: []dashboard.NoteClaim{}, NotSucceeded: []dashboard.NoteNotDone{}}
	if !written.IsZero() {
		view.WrittenAt = written.UTC().Format(time.RFC3339)
	}
	for _, c := range kept.Claims {
		entries := make([]string, len(c.Entries))
		for i, e := range c.Entries {
			entries[i] = strconv.FormatUint(e, 10)
		}
		view.Claims = append(view.Claims, dashboard.NoteClaim{Sentence: c.Sentence, Entries: entries, Outcomes: c.Outcomes})
	}

	if kept.WindowFrom.IsZero() || kept.WindowUntil.IsZero() {
		view.NotSucceededUnread = "the note was kept without its window"
		return view, nil
	}
	view.WindowFrom, view.WindowUntil = kept.WindowFrom.UTC().Format(time.RFC3339), kept.WindowUntil.UTC().Format(time.RFC3339)
	outcomes, beyond, err := a.outcomesBetween(ctx, kept.WindowFrom, kept.WindowUntil)
	if err != nil {
		view.NotSucceededUnread = err.Error()
		return view, nil
	}

	view.NotSucceededBeyond = beyond
	if len(outcomes) > noteOutcomesShown {
		view.NotSucceededMore = len(outcomes) - noteOutcomesShown
		outcomes = outcomes[:noteOutcomesShown]
	}
	for _, o := range outcomes {
		view.NotSucceeded = append(view.NotSucceeded, dashboard.NoteNotDone{ID: o.ID, Kind: o.Kind, Outcome: o.Outcome, Summary: o.Summary, At: o.At.UTC().Format(time.RFC3339)})
	}
	return view, nil
}
