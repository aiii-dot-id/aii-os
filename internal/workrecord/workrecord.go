package workrecord

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const Schema = "aii.work-record/1"

const Warning = "Tool arguments are kept as an excerpt and a hash; tool output is raw, as the record kept it, and may hold secrets (keys, tokens, personal data) a tool printed. This file stays where you put it."

type Source interface {
	WorkRecordSessions(since time.Time, id, project string) ([]store.WorkRecordSession, error)
	WorkRecordOf(sessions []string) (map[string]*store.WorkRecordParts, error)
}

const batch = 50

type Projects interface {
	Load(id string) (*project.Project, error)
}

type Filter struct {
	Since   time.Time
	Session string
	Project string
}

type UnknownSessionError struct{ ID string }

func (e *UnknownSessionError) UnknownSession() string { return e.ID }

func (e *UnknownSessionError) Error() string {
	return fmt.Sprintf("no work session %s in this identity's record", e.ID)
}

type OutsideFiltersError struct{ ID string }

func (e *OutsideFiltersError) UnknownSession() string { return e.ID }

func (e *OutsideFiltersError) Error() string {
	return fmt.Sprintf("work session %s is in this identity's record, and outside the other filters (-since, -project)", e.ID)
}

type Header struct {
	Schema     string       `json:"schema"`
	Kind       string       `json:"kind"`
	Warning    string       `json:"warning"`
	ExportedAt string       `json:"exported_at"`
	Filter     FilterRecord `json:"filter"`
}

type FilterRecord struct {
	Since   string `json:"since,omitempty"`
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
}

type Line struct {
	Schema  string         `json:"schema"`
	Kind    string         `json:"kind"`
	Session SessionRecord  `json:"session"`
	Project *ProjectRecord `json:"project"`

	ProjectUnavailable string        `json:"project_unavailable,omitempty"`
	Grades             []GradeRecord `json:"grades"`
	Turns              []TurnRecord  `json:"turns"`
	Calls              []CallRecord  `json:"calls"`
}

type End struct {
	Schema   string `json:"schema"`
	Kind     string `json:"kind"`
	Sessions int    `json:"sessions"`
}

type SessionRecord struct {
	ID               string `json:"id"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	Family           string `json:"family"`
	ProjectID        string `json:"project_id"`
	Focus            string `json:"focus"`
	NextMove         string `json:"next_move"`
	Plan             string `json:"plan"`
	ExpectedEvidence string `json:"expected_evidence"`
	Falsifier        string `json:"falsifier"`
	DecisionNeeded   string `json:"decision_needed"`
	State            string `json:"state"`
	Result           string `json:"result"`
	Evidence         string `json:"evidence"`
	EvidenceReadback string `json:"evidence_readback"`
	SubAgent         bool   `json:"sub_agent"`
	StartedAtMS      int64  `json:"started_at_ms,omitempty"`
	DeliveredAtMS    int64  `json:"delivered_at_ms,omitempty"`

	ToolCallsCounted *int `json:"tool_calls_counted"`

	Check              string `json:"check"`
	CheckSetAfterCalls *int   `json:"check_set_after_calls"`
}

type ProjectRecord struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Outcome     string   `json:"outcome"`
	Acceptance  []string `json:"acceptance"`
	Constraints []string `json:"constraints"`
}

type GradeRecord struct {
	Grade      string `json:"grade"`
	Item       string `json:"item,omitempty"`
	Turn       uint64 `json:"turn"`
	RecordedAt string `json:"recorded_at"`
	Text       string `json:"text"`
}

type TurnRecord struct {
	TurnID                   string `json:"turn_id"`
	EndedAtMS                int64  `json:"ended_at_ms"`
	Source                   string `json:"source"`
	Provider                 string `json:"provider"`
	Model                    string `json:"model"`
	Rounds                   int    `json:"rounds"`
	Calls                    int    `json:"calls"`
	PromptTokens             *int64 `json:"prompt_tokens"`
	CompletionTokens         *int64 `json:"completion_tokens"`
	TotalTokens              *int64 `json:"total_tokens"`
	CachedPromptTokens       *int64 `json:"cached_prompt_tokens"`
	CacheWriteTokens         *int64 `json:"cache_write_tokens"`
	UnknownAttempts          *int64 `json:"unknown_attempts"`
	SilentCalls              *int64 `json:"silent_calls"`
	EmittedCalls             *int64 `json:"emitted_calls"`
	EmissionCoverageComplete *bool  `json:"emission_coverage_complete"`
}

type CallRecord struct {
	Tex            string `json:"tex"`
	Seq            uint64 `json:"seq"`
	TurnID         string `json:"turn_id"`
	Ordinal        int    `json:"ordinal"`
	Tool           string `json:"tool"`
	Actor          string `json:"actor"`
	Model          string `json:"model"`
	ProviderCallID string `json:"provider_call_id"`
	WorkSession    string `json:"work_session_id"`
	SessionReason  string `json:"session_reason"`
	ProjectID      string `json:"project_id"`
	CalledAt       string `json:"called_at"`
	ArgsExcerpt    string `json:"args_excerpt"`
	ArgsChars      *int   `json:"args_chars"`
	ArgsRecord     string `json:"args_record"`
	Ended          bool   `json:"ended"`
	ResultSeq      uint64 `json:"result_seq,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	ReasonCode     string `json:"reason_code,omitempty"`
	ExitStatus     *int   `json:"exit_status"`
	DurationMS     *int64 `json:"duration_ms"`
	Truncated      bool   `json:"truncated"`
	Result         string `json:"result"`
	ResultLastLine string `json:"result_last_line,omitempty"`
	ResultCut      bool   `json:"result_cut"`
	ResultChars    *int   `json:"result_chars"`
	ResultRecord   string `json:"result_record,omitempty"`

	Recovers []string `json:"recovers,omitempty"`
}

func Write(ctx context.Context, w io.Writer, src Source, projects Projects, f Filter, now time.Time) (int, error) {
	sessions, err := src.WorkRecordSessions(f.Since, f.Session, f.Project)
	if err != nil {
		return 0, fmt.Errorf("read the work sessions: %w", err)
	}
	if f.Session != "" && len(sessions) == 0 {
		if !f.Since.IsZero() || f.Project != "" {
			if held, err := src.WorkRecordSessions(time.Time{}, f.Session, ""); err == nil && len(held) > 0 {
				return 0, &OutsideFiltersError{ID: f.Session}
			}
		}
		return 0, &UnknownSessionError{ID: f.Session}
	}
	out := bufio.NewWriter(w)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	head := Header{Schema: Schema, Kind: "header", Warning: Warning, ExportedAt: now.UTC().Format(time.RFC3339), Filter: FilterRecord{Session: f.Session, Project: f.Project}}
	if !f.Since.IsZero() {
		head.Filter.Since = f.Since.UTC().Format(time.RFC3339)
	}
	if err := enc.Encode(head); err != nil {
		return 0, err
	}
	n := 0
	for start := 0; start < len(sessions); start += batch {
		group := sessions[start:min(start+batch, len(sessions))]
		ids := make([]string, len(group))
		for i, ws := range group {
			ids[i] = ws.ID
		}
		parts, err := src.WorkRecordOf(ids)
		if err != nil {
			return n, fmt.Errorf("read the work sessions' calls, grades and turns: %w", err)
		}
		for _, ws := range group {
			if err := ctx.Err(); err != nil {
				return n, err
			}
			if err := enc.Encode(sessionLine(projects, ws, parts[ws.ID])); err != nil {
				return n, err
			}
			n++
		}
	}
	if err := enc.Encode(End{Schema: Schema, Kind: "end", Sessions: n}); err != nil {
		return n, err
	}
	return n, out.Flush()
}

func sessionLine(projects Projects, ws store.WorkRecordSession, parts *store.WorkRecordParts) Line {
	if parts == nil {
		parts = &store.WorkRecordParts{}
	}
	line := Line{Schema: Schema, Kind: "session", Grades: []GradeRecord{}, Turns: []TurnRecord{}, Calls: []CallRecord{}, Session: SessionRecord{
		ID: ws.ID, Description: ws.Description, Status: ws.Status, Family: ws.Family, ProjectID: ws.Project,
		Focus: ws.Focus, NextMove: ws.NextMove, Plan: ws.Plan, ExpectedEvidence: ws.ExpectedEvidence, Falsifier: ws.Falsifier,
		DecisionNeeded: ws.DecisionNeeded, State: ws.State, Result: ws.Result, Evidence: ws.Evidence, EvidenceReadback: ws.EvidenceReadback,
		SubAgent: ws.Child, StartedAtMS: ws.StartedAt, DeliveredAtMS: ws.DeliveredAt,
	}}
	if ws.ToolCallsKnown {
		n := ws.ToolCalls
		line.Session.ToolCallsCounted = &n
	}
	if ws.Project != "" {
		switch {
		case projects == nil:
			line.ProjectUnavailable = "the projects were not readable where this export was made"
		default:
			p, err := projects.Load(ws.Project)
			if err != nil {
				line.ProjectUnavailable = err.Error()
				break
			}
			line.Project = &ProjectRecord{ID: p.ID, Name: p.Name, State: p.State, Outcome: p.Contract.Outcome,
				Acceptance: orEmpty(p.Contract.Acceptance), Constraints: orEmpty(p.Contract.Constraints)}
		}
	}
	for _, g := range parts.Grades {
		line.Grades = append(line.Grades, GradeRecord{Grade: g.Grade, Item: g.Item, Turn: g.Turn, RecordedAt: g.RecordedAt, Text: g.Text})
	}
	for _, u := range parts.Turns {
		line.Turns = append(line.Turns, TurnRecord{TurnID: u.TurnID, EndedAtMS: u.EndedMS, Source: u.Source, Provider: u.Provider, Model: u.Model,
			Rounds: u.Rounds, Calls: u.Calls, PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
			CachedPromptTokens: u.CachedPromptTokens, CacheWriteTokens: u.CacheWriteTokens, UnknownAttempts: u.UnknownAttempts,
			SilentCalls: u.SilentCalls, EmittedCalls: u.EmittedCalls, EmissionCoverageComplete: u.EmissionCoverageComplete})
	}
	if parts.Check.Command != "" {
		n := parts.Check.SetAfterCalls
		line.Session.Check, line.Session.CheckSetAfterCalls = parts.Check.Command, &n
	}
	recovers := store.Recoveries(parts.Calls)
	for _, c := range parts.Calls {
		r := callRecord(c)
		r.Recovers = recovers[c.Tex]
		line.Calls = append(line.Calls, r)
	}
	return line
}

func callRecord(c store.WorkCall) CallRecord {
	r := CallRecord{Tex: c.Tex, Seq: c.Seq, TurnID: c.TurnID, Ordinal: c.Ordinal, Tool: c.Tool, Actor: c.Actor, Model: c.Model,
		ProviderCallID: c.ProviderCallID, WorkSession: c.WorkSession, SessionReason: c.SessionReason, ProjectID: c.ProjectID,
		CalledAt: c.CalledAt, ArgsExcerpt: c.Args(), ArgsRecord: c.ArgsRecord, Ended: c.Ended}
	if n, ok := store.RecordChars(c.ArgsRecord); ok {
		r.ArgsChars = &n
	}
	if !c.Ended {
		return r
	}
	r.ResultSeq, r.EndedAt, r.Outcome, r.ReasonCode = c.ResultSeq, c.EndedAt, c.Outcome, c.ReasonCode
	r.ExitStatus, r.DurationMS, r.Truncated, r.ResultRecord = c.ExitStatus, c.DurationMS, c.Truncated, c.ResultRecord
	r.Result, r.ResultLastLine, r.ResultCut = c.KeptResult()
	if n, ok := store.RecordChars(c.ResultRecord); ok {
		r.ResultChars = &n
	}
	return r
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

var ErrIncomplete = errors.New("the work record did not arrive whole")

func Check(r io.Reader) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<30)
	lines, sessions, ended, counted := 0, 0, false, 0
	for sc.Scan() {
		var line struct {
			Schema   string `json:"schema"`
			Kind     string `json:"kind"`
			Sessions int    `json:"sessions"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return 0, fmt.Errorf("%w: line %d is not JSON: %v", ErrIncomplete, lines+1, err)
		}
		switch {
		case line.Schema != Schema:
			return 0, fmt.Errorf("%w: line %d is of schema %q, not %s", ErrIncomplete, lines+1, line.Schema, Schema)
		case ended:
			return 0, fmt.Errorf("%w: line %d follows the end line", ErrIncomplete, lines+1)
		case lines == 0 && line.Kind != "header":
			return 0, fmt.Errorf("%w: the first line is not its header", ErrIncomplete)
		case line.Kind == "session":
			sessions++
		case line.Kind == "end":
			ended, counted = true, line.Sessions
		}
		lines++
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrIncomplete, err)
	}
	if !ended || counted != sessions {
		return 0, fmt.Errorf("%w: it ends without its end line, or the end line counts %d sessions where %d came", ErrIncomplete, counted, sessions)
	}
	return sessions, nil
}
