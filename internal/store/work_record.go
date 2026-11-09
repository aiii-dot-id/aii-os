package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

type WorkCall struct {
	Seq            uint64
	Tex            string
	TurnID         string
	Ordinal        int
	Tool           string
	Actor          string
	Model          string
	ProviderCallID string

	WorkSession   string
	SessionReason string
	ProjectID     string
	CalledAt      string
	CallContent   string
	ArgsRecord    string

	Ended         bool
	ResultSeq     uint64
	EndedAt       string
	Outcome       string
	ReasonCode    string
	ExitStatus    *int
	DurationMS    *int64
	Truncated     bool
	ResultContent string

	ResultClipped bool
	ResultRecord  string

	Request *interaction.RequestFacts

	PlanStep *string
}

func (c WorkCall) Args() string {
	rest := strings.TrimPrefix(c.CallContent, "→ "+c.Tool+"(")
	if rest == c.CallContent {
		return c.CallContent
	}
	return strings.TrimSuffix(rest, ")")
}

func (c WorkCall) HeardRead() bool { return heardRecall(c.Tool, c.Args()) }

func RecordChars(record string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.SplitN(record, " ", 2)[0], "["))
	return n, err == nil && n >= 0 && strings.HasPrefix(record, "[")
}

func (c WorkCall) KeptResult() (head, last string, cut bool) {
	_, kept, ok := strings.Cut(c.ResultContent, "\n← ")
	if !ok {
		return "", "", false
	}
	whole, known := RecordChars(c.ResultRecord)
	i := strings.LastIndex(kept, excerptEndsMark)
	if !known || i < 0 {
		return kept, "", false
	}
	rest, ok := strings.CutPrefix(kept[i+len(excerptEndsMark):], strconv.Itoa(whole)+" characters]")
	if !ok || (rest != "" && !strings.HasPrefix(rest, "\n[")) {
		return kept, "", false
	}
	return kept[:i], strings.TrimPrefix(rest, "\n"), true
}

const workCallSelect = `SELECT c.turn_seq, c.source_id, c.turn_id, c.project_id, COALESCE(c.recorded_at,''), c.content, c.details, COALESCE(q.payload,''), COALESCE(ps.payload,''),
	r.id IS NOT NULL, COALESCE(r.turn_seq,0), COALESCE(r.recorded_at,''), substr(COALESCE(r.content,''),1,?), length(COALESCE(r.content,'')), COALESCE(r.outcome,''), COALESCE(r.details,'{}')
	FROM conversations c
	LEFT JOIN turn_annotations q ON q.turn_seq=c.turn_seq AND q.kind='` + AnnotationRequest + `'
	LEFT JOIN turn_annotations ps ON ps.turn_seq=c.turn_seq AND ps.kind='` + AnnotationPlanStepServed + `'
	LEFT JOIN conversations r ON r.source_kind='tool' AND r.source_id=c.source_id AND r.kind='tool_result'
	WHERE c.kind='tool_call' AND c.source_kind='tool'`

const wholeRow = 1 << 40

const workCallSession = `(CASE WHEN r.id IS NULL THEN json_extract(c.details,'$.work_session_id') ELSE json_extract(r.details,'$.work_session_id') END)`

func scanWorkCall(rows rowScanner) (WorkCall, error) {
	var c WorkCall
	var callDetails, request, step, resultDetails string
	var length int
	if err := rows.Scan(&c.Seq, &c.Tex, &c.TurnID, &c.ProjectID, &c.CalledAt, &c.CallContent, &callDetails, &request, &step,
		&c.Ended, &c.ResultSeq, &c.EndedAt, &c.ResultContent, &length, &c.Outcome, &resultDetails); err != nil {
		return c, err
	}
	c.ResultClipped = utf8.RuneCountInString(c.ResultContent) < length
	var call, result interaction.Details
	if err := json.Unmarshal([]byte(callDetails), &call); err != nil {
		return c, fmt.Errorf("tool call %s: its details do not decode: %w", c.Tex, err)
	}
	if err := json.Unmarshal([]byte(resultDetails), &result); err != nil {
		return c, fmt.Errorf("tool result %s: its details do not decode: %w", c.Tex, err)
	}
	c.Tool, c.Actor, c.Model, c.ProviderCallID, c.Ordinal, c.ArgsRecord = call.Tool, call.Actor, call.Model, call.ProviderCallID, call.Ordinal, call.ArgsRecord
	c.WorkSession, c.SessionReason = call.WorkSession, call.SessionReason
	if request != "" {
		c.Request = new(interaction.RequestFacts)
		if err := json.Unmarshal([]byte(request), c.Request); err != nil {
			return c, fmt.Errorf("tool call %s: its request's facts do not decode: %w", c.Tex, err)
		}
	}
	if step != "" {
		var served planStepLine
		if err := json.Unmarshal([]byte(step), &served); err != nil {
			return c, fmt.Errorf("tool call %s: its plan step does not decode: %w", c.Tex, err)
		}
		c.PlanStep = &served.Line
	}
	if c.Ended {
		c.WorkSession, c.SessionReason = result.WorkSession, result.SessionReason
		c.ReasonCode, c.ExitStatus, c.DurationMS, c.Truncated, c.ResultRecord = result.ReasonCode, result.ExitStatus, result.DurationMS, result.Truncated, result.ResultRecord
	}
	return c, nil
}

var errHistoricalWork = errors.New("this database predates the work record: its conversation rows carry no tool call kinds")

func (s *Store) WorkCalls(session string, terms []string, beforeSeq uint64, limit, head int, openTurn string) ([]WorkCall, error) {
	if beforeSeq == 0 || beforeSeq > 1<<62 {
		beforeSeq = 1 << 62
	}
	q := workCallSelect + ` AND c.turn_seq < ? AND ` + workCallSession + ` = ?`
	args := []any{head, int64(beforeSeq), session}
	if openTurn != "" {
		q += ` AND NOT (r.id IS NULL AND c.turn_id = ?)`
		args = append(args, openTurn)
	}
	for _, t := range terms {
		q += ` AND (c.content || char(10) || COALESCE(r.content,'')) LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(t)+"%")
	}
	q += ` ORDER BY c.turn_seq DESC LIMIT ?`
	return s.workCalls(q, append(args, limit)...)
}

func (s *Store) workCalls(q string, args ...any) ([]WorkCall, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.historicalInteractions {
		return nil, errHistoricalWork
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkCall
	for rows.Next() {
		c, err := scanWorkCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) WorkCallByTex(tex string) (WorkCall, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.historicalInteractions {
		return WorkCall{}, false, errHistoricalWork
	}
	c, err := scanWorkCall(s.db.QueryRow(workCallSelect+` AND c.source_id = ?`, wholeRow, tex))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkCall{}, false, nil
	}
	return c, err == nil, err
}

type WorkGrade struct {
	Grade, Item string
	Turn        uint64
	RecordedAt  string
	Text        string
}

type WorkTurnUsage struct {
	TurnID                   string
	EndedMS                  int64
	Source, Provider, Model  string
	Rounds, Calls            int
	PromptTokens             *int64
	CompletionTokens         *int64
	TotalTokens              *int64
	CachedPromptTokens       *int64
	CacheWriteTokens         *int64
	UnknownAttempts          *int64
	SilentCalls              *int64
	EmittedCalls             *int64
	EmissionCoverageComplete *bool
}

type WorkRecordParts struct {
	Calls  []WorkCall
	Grades []WorkGrade
	Turns  []WorkTurnUsage

	Requests []WorkRequest

	Check WorkCheck
}

type WorkRequest struct {
	TurnID string
	Facts  interaction.RequestFacts
	Shared bool
}

const requestSessionsSQL = `SELECT a.key, COALESCE(` + workCallSession + `,'')
	FROM turn_annotations a
	JOIN conversations c ON c.turn_seq=a.turn_seq AND c.kind='tool_call' AND c.source_kind='tool'
	LEFT JOIN conversations r ON r.source_kind='tool' AND r.source_id=c.source_id AND r.kind='tool_result'
	WHERE a.kind='` + AnnotationRequest + `' AND a.key IN (SELECT value FROM json_each(?))`

func (s *Store) WorkRecordOf(sessions []string) (map[string]*WorkRecordParts, error) {
	out := make(map[string]*WorkRecordParts, len(sessions))
	for _, id := range sessions {
		out[id] = &WorkRecordParts{}
	}
	if len(sessions) == 0 {
		return out, nil
	}
	ids, err := json.Marshal(sessions)
	if err != nil {
		return nil, err
	}
	calls, err := s.workCalls(workCallSelect+` AND `+workCallSession+` IN (SELECT value FROM json_each(?)) ORDER BY c.turn_seq`, wholeRow, string(ids))
	if err != nil {
		return nil, err
	}
	turnsOf := map[string]map[string]bool{}
	name := func(session, turn string) {
		if turn == "" {
			return
		}
		if turnsOf[session] == nil {
			turnsOf[session] = map[string]bool{}
		}
		turnsOf[session][turn] = true
	}
	listed, requests := map[[2]string]bool{}, map[string]bool{}
	for _, c := range calls {
		if p := out[c.WorkSession]; p != nil {
			p.Calls = append(p.Calls, c)
			name(c.WorkSession, c.TurnID)
			if c.Request == nil {
				continue
			}
			if k := RequestKey(c.TurnID, c.Request.Ordinal); !listed[[2]string{c.WorkSession, k}] {
				listed[[2]string{c.WorkSession, k}], requests[k] = true, true
				p.Requests = append(p.Requests, WorkRequest{TurnID: c.TurnID, Facts: *c.Request})
			}
		}
	}
	checks, err := s.WorkChecksOf(sessions)
	if err != nil {
		return nil, err
	}
	for id, c := range checks {
		out[id].Check = c
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.historicalTurnMetrics {
		return nil, errors.New("this database's turn usage predates the work record: its turn_metrics names no turn")
	}
	if err := s.markSharedRequests(out, requests); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT json_extract(details,'$.work_session_id'), turn_id FROM conversations
		WHERE kind='notice' AND turn_id != '' AND json_extract(details,'$.work_session_id') IN (SELECT value FROM json_each(?))`, string(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session, turn string
		if err := rows.Scan(&session, &turn); err != nil {
			rows.Close()
			return nil, err
		}
		name(session, turn)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(`SELECT json_extract(details,'$.grade.session'), turn_seq, COALESCE(recorded_at,''), content, COALESCE(json_extract(details,'$.grade.grade'),''), COALESCE(json_extract(details,'$.grade.item'),'')
		FROM conversations WHERE kind='message' AND role='operator' AND json_extract(details,'$.grade.session') IN (SELECT value FROM json_each(?)) ORDER BY turn_seq`, string(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session string
		var g WorkGrade
		if err := rows.Scan(&session, &g.Turn, &g.RecordedAt, &g.Text, &g.Grade, &g.Item); err != nil {
			rows.Close()
			return nil, err
		}
		out[session].Grades = append(out[session].Grades, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var turns []string
	for _, set := range turnsOf {
		for turn := range set {
			turns = append(turns, turn)
		}
	}
	if len(turns) == 0 {
		return out, nil
	}
	list, err := json.Marshal(turns)
	if err != nil {
		return nil, err
	}
	rows, err = s.db.Query(`SELECT ts_ms, turn_id, COALESCE(source,''), COALESCE(provider,''), COALESCE(model,''), rounds, calls,
		prompt_tokens, completion_tokens, total_tokens, cached_prompt_tokens, cache_write_tokens, unknown_attempts, silent_calls,
		emitted_calls, emission_coverage_complete
		FROM turn_metrics
		WHERE turn_id IN (SELECT value FROM json_each(?)) AND (source IS NULL OR source NOT LIKE 'facility:%')
		ORDER BY ts_ms`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u WorkTurnUsage
		var counts [8]sql.NullInt64
		var complete sql.NullBool
		if err := rows.Scan(&u.EndedMS, &u.TurnID, &u.Source, &u.Provider, &u.Model, &u.Rounds, &u.Calls,
			&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5], &counts[6], &counts[7], &complete); err != nil {
			return nil, err
		}
		for i, dst := range []**int64{&u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.CachedPromptTokens, &u.CacheWriteTokens, &u.UnknownAttempts, &u.SilentCalls, &u.EmittedCalls} {
			if counts[i].Valid {
				n := counts[i].Int64
				*dst = &n
			}
		}
		if complete.Valid {
			b := complete.Bool
			u.EmissionCoverageComplete = &b
		}
		for session, set := range turnsOf {
			if set[u.TurnID] {
				out[session].Turns = append(out[session].Turns, u)
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) markSharedRequests(out map[string]*WorkRecordParts, keys map[string]bool) error {
	if len(keys) == 0 {
		return nil
	}
	list, err := json.Marshal(slices.Sorted(maps.Keys(keys)))
	if err != nil {
		return err
	}
	rows, err := s.db.Query(requestSessionsSQL, string(list))
	if err != nil {
		return err
	}
	defer rows.Close()
	holders := map[string]map[string]bool{}
	for rows.Next() {
		var k, session string
		if err := rows.Scan(&k, &session); err != nil {
			return err
		}
		if holders[k] == nil {
			holders[k] = map[string]bool{}
		}
		holders[k][session] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, p := range out {
		for i, r := range p.Requests {
			for session := range holders[RequestKey(r.TurnID, r.Facts.Ordinal)] {
				if session != "" && session != id {
					p.Requests[i].Shared = true
				}
			}
		}
	}
	return nil
}

type WorkRecordSession struct {
	WorkSession
	DeliveredAt int64
}

func (s *Store) WorkRecordSessions(since time.Time, id, project string) ([]WorkRecordSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fields, err := s.workSessionSelect()
	if err != nil {
		return nil, err
	}
	child, args, err := s.childTest()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + fields + `, ` + child + `, COALESCE(delivered_at,0) FROM work_sessions WHERE 1=1`
	if id != "" {
		q += ` AND id = ?`
		args = append(args, id)
	}
	if project != "" {
		q += ` AND project_id = ?`
		args = append(args, project)
	}
	if !since.IsZero() {
		ms := since.UnixMilli()
		q += ` AND (COALESCE(started_at,0) >= ? OR COALESCE(delivered_at,0) >= ?)`
		args = append(args, ms, ms)
	}
	rows, err := s.db.Query(q+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkRecordSession
	for rows.Next() {
		var ws WorkRecordSession
		if err := scanWorkSession(rows, &ws.WorkSession, &ws.Child, &ws.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}
