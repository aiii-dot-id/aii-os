package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"strings"
	"time"
)

const subagentDescriptionPrefix = "sub-agent: "

const SubagentWorkKind = "subagent.run"

func queuedChild(also string) string {
	return `(EXISTS (SELECT 1 FROM work_queue w WHERE w.kind = ? AND w.dedup_key = work_sessions.id` + also + `)
	OR EXISTS (SELECT 1 FROM work_queue w WHERE w.kind = ? AND w.dedup_key >= (work_sessions.id || '#')
		AND w.dedup_key < (work_sessions.id || '$')` + also + `))`
}

func (s *Store) childTest() (string, []any, error) {
	if s.readOnly {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='work_queue'`).Scan(&n); err != nil {
			return "", nil, fmt.Errorf("read work queue schema: %w", err)
		}
		if n == 0 {
			return "0", nil, nil
		}
	}
	return queuedChild(""), []any{SubagentWorkKind, SubagentWorkKind}, nil
}

func SubagentDescription(goal string) string { return subagentDescriptionPrefix + goal }

func SubagentGoal(description string) string {
	return strings.TrimPrefix(description, subagentDescriptionPrefix)
}

type WorkSession struct {
	ID          string
	Description string
	Project     string
	Status      string
	State       string
	LeaseOwner  string
	LeaseUntil  string
	CreatedSeq  uint64
	UpdatedSeq  uint64
	Result      string
	HarvestedMs int64

	Focus    string
	NextMove string
	Plan     string

	ExpectedEvidence string
	Falsifier        string
	DecisionNeeded   string

	Evidence         string
	EvidenceReadback string

	Child bool
}

const workSessionFields = `id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result,
project_id, focus, next_move, plan, expected_evidence, falsifier, decision_needed, evidence, evidence_readback`

func (s *Store) workSessionSelect() (string, error) {
	if !s.readOnly {
		return workSessionFields, nil
	}
	live, err := s.liveColumns("work_sessions")
	if err != nil {
		return "", err
	}
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return "", err
	}
	ref, err := referenceSchema(string(raw))
	if err != nil {
		return "", err
	}
	fields := strings.Split(workSessionFields, ",")
	for i, field := range fields {
		name := strings.TrimSpace(field)
		fields[i] = quoteIdentifier(name)
		if live[name] {
			continue
		}
		shape, known := ref.Shapes["work_sessions"][name]
		switch {
		case known && shape.PK == 0 && shape.Dflt == "''":
			fields[i] = "'' AS " + quoteIdentifier(name)
		case known && shape.PK == 0 && !shape.NotNull:
			fields[i] = "NULL AS " + quoteIdentifier(name)
		default:
			return "", &SchemaError{Phase: "read-only work", Cause: fmt.Errorf("recorded column work_sessions.%s is unavailable", name)}
		}
	}
	return strings.Join(fields, ","), nil
}

func (s *Store) WorkSessionByID(id string) (*WorkSession, error) {
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
	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err = s.db.QueryRow(
		`SELECT `+fields+`, `+child+` FROM work_sessions WHERE id = ?`, append(args, id)...,
	).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback, &ws.Child)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ws.State, ws.LeaseOwner, ws.LeaseUntil, ws.Result = state.String, leaseOwner.String, leaseUntil.String, result.String
	ws.CreatedSeq, ws.UpdatedSeq = uint64(createdSeq.Int64), uint64(updatedSeq.Int64)
	return &ws, nil
}

func (s *Store) ActiveWorkSession() (*WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fields, err := s.workSessionSelect()
	if err != nil {
		return nil, err
	}

	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	child, args, err := s.childTest()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + fields + ` FROM work_sessions
		WHERE status = 'active' AND NOT ` + child + ` ORDER BY rowid DESC LIMIT 1`
	err = s.db.QueryRow(q, args...).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ws.State = state.String
	ws.LeaseOwner = leaseOwner.String
	ws.LeaseUntil = leaseUntil.String
	ws.Result = result.String
	if createdSeq.Valid {
		ws.CreatedSeq = uint64(createdSeq.Int64)
	}
	if updatedSeq.Valid {
		ws.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &ws, nil
}

func (s *Store) UpdateWorkState(ctx context.Context, sessionID, state string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE work_sessions SET state=? WHERE id=?`, state, sessionID); err != nil {
		return err
	}
	if err = s.recordWorkChangeTx(ctx, tx, interaction.WorkChange{Session: sessionID, State: &state}, interaction.TurnID(ctx), "Work state updated"); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	return err
}

func (s *Store) UpdateWorkPlan(ctx context.Context, sessionID string, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded *string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()

	if focus == nil && nextMove == nil && plan == nil && expectedEvidence == nil && falsifier == nil && decisionNeeded == nil {
		return nil
	}
	sets := make([]string, 0, 3)
	args := make([]interface{}, 0, 4)
	if focus != nil {
		sets = append(sets, "focus = ?")
		args = append(args, *focus)
	}
	if nextMove != nil {
		sets = append(sets, "next_move = ?")
		args = append(args, *nextMove)
	}
	if plan != nil {
		sets = append(sets, "plan = ?")
		args = append(args, *plan)
	}
	if expectedEvidence != nil {
		sets = append(sets, "expected_evidence = ?")
		args = append(args, *expectedEvidence)
	}
	if falsifier != nil {
		sets = append(sets, "falsifier = ?")
		args = append(args, *falsifier)
	}
	if decisionNeeded != nil {
		sets = append(sets, "decision_needed = ?")
		args = append(args, *decisionNeeded)
	}
	args = append(args, sessionID)
	q := `UPDATE work_sessions SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(q, args...); err != nil {
		return err
	}
	if err = s.recordWorkChangeTx(ctx, tx, interaction.WorkChange{Session: sessionID, Focus: focus, NextMove: nextMove, Plan: plan, ExpectedEvidence: expectedEvidence, Falsifier: falsifier, DecisionNeeded: decisionNeeded}, interaction.TurnID(ctx), "Work plan updated"); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	return err
}

func (s *Store) StartWorkSession(ctx context.Context, id, description string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO work_sessions (id, description, status, state, project_id) VALUES (?, ?, 'active', '', ?)`,
		id, description, s.activeProject)
	if err != nil {
		return err
	}
	if err = s.recordWorkChangeTx(ctx, tx, interaction.WorkChange{Session: id}, interaction.TurnID(ctx), "Work started: "+description); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	if err == nil {
		_, child := s.workRow(id)
		ev = &WorkEvent{Kind: WorkStarted, ID: id, Project: s.activeProject, Actor: workActor(child)}
	}
	return err
}

func (s *Store) RecentPlan() (*WorkSession, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	child, args, err := s.childTest()
	if err != nil {
		return nil, false, err
	}
	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err = s.db.QueryRow(
		`SELECT id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result
		, project_id, focus, next_move, plan, expected_evidence, falsifier, decision_needed, evidence, evidence_readback FROM work_sessions
		WHERE (focus != '' OR next_move != '' OR plan != '')
		AND NOT `+child+`
		ORDER BY rowid DESC LIMIT 1`,
		args...).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	ws.State = state.String
	ws.LeaseOwner = leaseOwner.String
	ws.LeaseUntil = leaseUntil.String
	ws.Result = result.String
	if createdSeq.Valid {
		ws.CreatedSeq = uint64(createdSeq.Int64)
	}
	if updatedSeq.Valid {
		ws.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &ws, true, nil
}

const interruptedByRestart = "FAILED: interrupted by a runtime restart before delivery"

const preStartMarker = "unserved: failed before start:"

func PreStartFailure(cause string) string { return preStartMarker + " " + cause }

func IsPreStartFailure(result string) bool { return strings.HasPrefix(result, preStartMarker) }

func (s *Store) SweepOrphanWorkSessions() ([]WorkSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w().Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`UPDATE work_sessions SET status='delivered', result=?, evidence='external_effect_unknown', delivered_at=?
		WHERE status='active' AND NOT `+queuedChild(` AND w.state IN ('PENDING','CLAIMED')`)+`
		RETURNING id, description, `+queuedChild(""), interruptedByRestart, time.Now().UTC().UnixMilli(),
		SubagentWorkKind, SubagentWorkKind, SubagentWorkKind, SubagentWorkKind)
	if err != nil {
		return nil, err
	}
	var swept []WorkSession
	for rows.Next() {
		ws := WorkSession{Status: "delivered", Result: interruptedByRestart, Evidence: EvidenceExternalUnknown}
		if err := rows.Scan(&ws.ID, &ws.Description, &ws.Child); err != nil {
			rows.Close()
			return nil, err
		}
		swept = append(swept, ws)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return swept, nil
}

func SweptSessionNote(swept []WorkSession) string {
	var lines []string
	for _, ws := range swept {
		if ws.Child {
			continue
		}
		lines = append(lines, fmt.Sprintf("Work session %s (%q) was active at the last shutdown and has been closed as interrupted — its external effects are unknown; verify before repeating them.", ws.ID, ws.Description))
	}
	return strings.Join(lines, "\n")
}

func (s *Store) DeliverWorkSession(ctx context.Context, id, result, evidence, readback string) error {
	if evidence != "" && !IsEvidenceClass(evidence) {
		return fmt.Errorf("deliver: unknown evidence class %q", evidence)
	}
	if EvidenceVerifiedTier(evidence) && evidenceReadbackClean(readback) == "" {
		return fmt.Errorf("deliver: evidence class %q requires a verification readback", evidence)
	}
	if EvidenceVerifiedTier(evidence) && !strings.HasPrefix(result, "served:") {
		return fmt.Errorf("deliver: a %s result must begin served: — a verified class cannot attach to a partial or unserved outcome", evidence)
	}
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE work_sessions SET status='delivered', result=?, evidence=?, evidence_readback=?, delivered_at=?, harvested_ms=NULL
		WHERE id=? AND (status != 'delivered' OR result LIKE ? || '%')`,
		result, evidence, readback, time.Now().UTC().UnixMilli(), id, preStartMarker)
	if err != nil {
		return err
	}
	if n, aerr := res.RowsAffected(); aerr != nil || n != 1 {
		return fmt.Errorf("delivery refused for %s: session already delivered with a real result (trajectory replay?)", id)
	}
	if err = s.recordWorkChangeTx(ctx, tx, interaction.WorkChange{Session: id, Result: &result, Evidence: &evidence, EvidenceReadback: &readback}, interaction.TurnID(ctx), "Work result delivered"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		changed = true
		return err
	}
	changed = true
	project, child := s.workRow(id)
	ev = &WorkEvent{Kind: WorkDelivered, ID: id, Project: project, Actor: workActor(child), Outcome: outcomeClass(result), Evidence: evidence}
	return nil
}

func (s *Store) LifetimeTicks() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ticks int64
	err := s.db.QueryRow(
		`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`,
	).Scan(&ticks)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return ticks, err
}

type Lived struct {
	Ticks   int64
	BirthAt time.Time
}

func (s *Store) Lived() (Lived, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var l Lived
	var birth string
	err := s.db.QueryRow(`SELECT lifetime_ticks, birth_at FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&l.Ticks, &birth)
	if err == sql.ErrNoRows {
		return Lived{}, nil
	}
	if err != nil {
		return Lived{}, err
	}
	if l.BirthAt, err = time.Parse(time.RFC3339, birth); err != nil {
		return Lived{Ticks: l.Ticks}, fmt.Errorf("birth time %q: %w", birth, err)
	}
	return l, nil
}

func (s *Store) IncrementLifetimeTicks() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.w().Exec(
		`UPDATE identity_lifetime SET lifetime_ticks = lifetime_ticks + 1, last_tick_at = ? WHERE singleton_id = 'current'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) LiveSubagentSessions() ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	child, args, err := s.childTest()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id FROM work_sessions
		 WHERE status = 'active' AND `+child+`
		 ORDER BY rowid DESC LIMIT 10`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		w := WorkSession{Child: true}
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) UnharvestedDeliveries(limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	child, args, err := s.childTest()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id, COALESCE(evidence,''), COALESCE(harvested_ms,0) FROM work_sessions
		 WHERE status = 'delivered' AND harvested_ms IS NULL AND `+child+`
		 ORDER BY rowid ASC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		w := WorkSession{Child: true}
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project, &w.Evidence, &w.HarvestedMs); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) MarkHarvested(id string, ms int64) error {
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.w().Exec(`UPDATE work_sessions SET harvested_ms=? WHERE id=?`, ms, id)
	if err == nil {
		if n, aerr := res.RowsAffected(); aerr == nil && n > 0 {
			project, child := s.workRow(id)
			ev = &WorkEvent{Kind: WorkHarvested, ID: id, Project: project, Actor: workActor(child)}
		}
	}
	return err
}

func (s *Store) RecentDeliveredSubagents(limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	child, args, err := s.childTest()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id, COALESCE(evidence,'') FROM work_sessions
		 WHERE status = 'delivered' AND `+child+`
		 ORDER BY rowid DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		w := WorkSession{Child: true}
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project, &w.Evidence); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) WorkSessionsByProject(projectID string, limit int) ([]WorkSession, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	child, args, err := s.childTest()
	if err != nil {
		return nil, false, err
	}
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id, `+child+` FROM work_sessions
		 WHERE project_id = ?
		 ORDER BY rowid DESC LIMIT ?+1`, append(args, projectID, limit)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project, &w.Child); err != nil {
			return nil, false, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

type ResumeFields struct {
	Focus            string
	Established      string
	NextAction       string
	ExpectedEvidence string
	Falsifier        string
	DecisionNeeded   string
	ResumePoint      string
	Evidence         string
}

func RenderResumeCard(f ResumeFields) string {
	if f.Focus == "" && f.Established == "" && f.NextAction == "" && f.ResumePoint == "" {
		return ""
	}
	orUnknown := func(v string) string {
		if strings.TrimSpace(v) == "" {
			return "not specified"
		}
		return v
	}
	var b strings.Builder
	b.WriteString("### Resume — the one next thing, and what would confirm it")
	if f.Focus != "" {
		b.WriteString("\nFocus: " + f.Focus)
	}
	if f.Established != "" {
		b.WriteString("\nEstablished: " + f.Established)
	}
	if f.NextAction != "" {
		b.WriteString("\nNext action: " + f.NextAction)
	}
	b.WriteString("\nExpected evidence: " + orUnknown(f.ExpectedEvidence))
	b.WriteString("\nFalsifier: " + orUnknown(f.Falsifier))
	if f.DecisionNeeded != "" {
		b.WriteString("\nDecision needed: " + f.DecisionNeeded)
	}
	if f.Evidence != "" {
		b.WriteString("\nEvidence: " + EvidenceScopeLabel(f.Evidence))
		if rec := EvidenceRecovery(f.Evidence); rec != "" {
			b.WriteString("\nRecovery: " + rec)
		}
	}
	if f.ResumePoint != "" {
		b.WriteString("\nResume point: " + f.ResumePoint)
	}
	return b.String()
}
