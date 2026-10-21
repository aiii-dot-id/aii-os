package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type FamilyAttempt struct {
	ID          string
	Description string
	Outcome     string
	Result      string
	Grade       string
	ToolCalls   int
	CallsKnown  bool
	StartedAt   int64
	DeliveredAt int64
}

type FamilyRecord struct {
	Family   string
	Attempts []FamilyAttempt
	Shortest *FamilyAttempt
	Steps    []string
}

func (s *Store) FamilyAttempts(family string, limit int) (FamilyRecord, error) {
	if !FamilySlug(family) {
		return FamilyRecord{}, fmt.Errorf("family %q is not a slug: lowercase letters, digits and hyphens, at most 40 characters", family)
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := FamilyRecord{Family: family}
	rows, err := s.db.Query(`SELECT id, description, COALESCE(result,''), tool_calls, COALESCE(started_at,0), COALESCE(delivered_at,0)
		FROM work_sessions WHERE family = ? AND status = 'delivered' ORDER BY rowid DESC LIMIT ?`, family, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a FamilyAttempt
		var calls sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Description, &a.Result, &calls, &a.StartedAt, &a.DeliveredAt); err != nil {
			rows.Close()
			return out, err
		}
		a.Outcome, a.ToolCalls, a.CallsKnown = outcomeClass(a.Result), int(calls.Int64), calls.Valid
		out.Attempts = append(out.Attempts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := range out.Attempts {
		a := &out.Attempts[i]
		err := s.db.QueryRow(`SELECT COALESCE(json_extract(details,'$.grade.grade'),'') FROM conversations
			WHERE json_extract(details,'$.grade.session') = ? ORDER BY rowid DESC LIMIT 1`, a.ID).Scan(&a.Grade)
		if err != nil && err != sql.ErrNoRows {
			return out, err
		}
		if a.Outcome == "served" && a.CallsKnown && (out.Shortest == nil || a.ToolCalls < out.Shortest.ToolCalls) {
			out.Shortest = a
		}
	}
	if out.Shortest == nil {
		return out, nil
	}
	steps, err := s.db.Query(`SELECT COALESCE(json_extract(details,'$.tool'),'') FROM conversations
		WHERE kind = 'tool_call' AND json_extract(details,'$.work_session_id') = ? ORDER BY rowid`, out.Shortest.ID)
	if err != nil {
		return out, err
	}
	defer steps.Close()
	for steps.Next() {
		var tool string
		if err := steps.Scan(&tool); err != nil {
			return out, err
		}
		out.Steps = append(out.Steps, tool)
	}
	return out, steps.Err()
}

func RenderFamily(r FamilyRecord, playbook string) string {
	if len(r.Attempts) == 0 {
		return fmt.Sprintf("Kind %s: no delivered attempt names this family yet.", r.Family)
	}
	count := map[string]int{}
	graded, gradedServed := 0, 0
	for _, a := range r.Attempts {
		count[a.Outcome]++
		if a.Grade != "" {
			graded++
			if a.Grade == "served" {
				gradedServed++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Kind %s: %d delivered attempt(s), newest first: served %d, partial %d, unserved %d; graded %d (served %d).",
		r.Family, len(r.Attempts), count["served"], count["partial"], count["unserved"], graded, gradedServed)
	for _, a := range r.Attempts {
		calls := "calls not counted"
		if a.CallsKnown {
			calls = fmt.Sprintf("%d calls", a.ToolCalls)
		}
		grade := "ungraded"
		if a.Grade != "" {
			grade = "graded " + a.Grade
		}
		when, wall := "", ""
		if a.DeliveredAt > 0 {
			when = time.UnixMilli(a.DeliveredAt).UTC().Format("2006-01-02") + " "
		}
		if a.StartedAt > 0 && a.DeliveredAt > a.StartedAt {
			wall = ", " + time.Duration(a.DeliveredAt-a.StartedAt).Round(time.Second).String()
		}
		outcome := a.Outcome
		if outcome == "" {
			outcome = "no outcome word"
		}
		fmt.Fprintf(&b, "\n  %s %s%s, %s, %s%s: %s", a.ID, when, outcome, grade, calls, wall, a.Description)
	}
	if r.Shortest != nil {
		fmt.Fprintf(&b, "\nShortest served attempt: %s in %d calls.", r.Shortest.ID, r.Shortest.ToolCalls)
		if len(r.Steps) > 0 {
			numbered := make([]string, len(r.Steps))
			for i, t := range r.Steps {
				numbered[i] = fmt.Sprintf("%d %s", i+1, t)
			}
			fmt.Fprintf(&b, " Its calls: %s.", strings.Join(numbered, ", "))
		} else {
			b.WriteString(" Its call records are no longer kept.")
		}
	}
	if playbook != "" {
		fmt.Fprintf(&b, "\nYour playbook for this kind: %s.", playbook)
	}
	return b.String()
}

func (s *Store) SessionToolCalls(id string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind = 'tool_call' AND json_extract(details,'$.work_session_id') = ?`, id).Scan(&n)
	return n, err
}

func (s *Store) SessionDeliveredInTurn(turn string) (string, error) {
	if turn == "" {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT w.id FROM conversations c JOIN work_sessions w ON w.id = json_extract(c.details,'$.work_session_id')
		WHERE c.turn_id = ? AND c.kind = 'notice' AND c.role = 'system'
		  AND json_extract(c.details,'$.actor') = 'main' AND json_extract(c.details,'$.work.result') IS NOT NULL
		  AND w.status = 'delivered'
		ORDER BY c.turn_seq DESC LIMIT 1`, turn).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
