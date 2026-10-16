package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ReasoningCall struct {
	Call  int
	Model string
	Text  string

	Account bool
}

const AccountMark = "may be a summary"

func (c ReasoningCall) header() string {
	parts := []string{fmt.Sprintf("call %d", c.Call)}
	if c.Model != "" {
		parts = append(parts, c.Model)
	}
	if c.Account {
		parts = append(parts, AccountMark)
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

type Reasoning struct {
	TurnSeq   uint64
	Models    string
	Content   string
	CreatedAt string
}

func (r Reasoning) AccountMarked() bool {
	return strings.Contains(r.Models, "("+AccountMark+")")
}

func (s *Store) AddReasoning(turnSeq uint64, calls []ReasoningCall) error {
	var kept []ReasoningCall
	for i, c := range calls {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		if c.Call <= 0 {
			c.Call = i + 1
		}
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		return nil
	}
	if turnSeq == 0 {
		return fmt.Errorf("reasoning names the reply it belongs to, and turn 0 is none")
	}
	content := kept[0].Text
	var models []string
	seen := map[string]bool{}
	marked := false
	for _, c := range kept {
		name := c.Model
		if c.Account {

			marked = true
			name = strings.TrimSpace(name + " (" + AccountMark + ")")
		}
		if name != "" && !seen[name] {
			seen[name] = true
			models = append(models, name)
		}
	}

	if len(kept) > 1 || marked {
		var b strings.Builder
		for i, c := range kept {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(c.header() + "\n")
			b.WriteString(c.Text)
		}
		content = b.String()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w().Exec(`INSERT INTO reasoning (turn_seq, models, content, created_at) VALUES (?, ?, ?, ?)`,
		turnSeq, strings.Join(models, ", "), content, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) ReasoningOfTurn(turnSeq uint64) (Reasoning, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var r Reasoning
	err := s.db.QueryRow(`SELECT turn_seq, models, content, created_at FROM reasoning WHERE turn_seq = ?`, turnSeq).
		Scan(&r.TurnSeq, &r.Models, &r.Content, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Reasoning{}, false, nil
	}
	if err != nil {
		return Reasoning{}, false, err
	}
	return r, true, nil
}

func (s *Store) HasReasoning() (bool, error) {
	if s.reasoningSeen.Load() {
		return true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM reasoning LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.reasoningSeen.Store(true)
	return true, nil
}

func (s *Store) ReasoningTurns(turns []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(turns) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(turns))
	for _, t := range turns {
		if t <= 1<<62 {
			args = append(args, int64(t))
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	q := `SELECT turn_seq FROM reasoning WHERE turn_seq IN (?` + strings.Repeat(`, ?`, len(args)-1) + `)`
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq uint64
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		out[seq] = true
	}
	return out, rows.Err()
}

func (s *Store) ReasoningPage(terms []string, beforeSeq uint64, limit int) ([]Reasoning, error) {
	if beforeSeq == 0 || beforeSeq > 1<<62 {
		beforeSeq = 1 << 62
	}
	q := `SELECT turn_seq, models, content, created_at FROM reasoning WHERE turn_seq < ?`
	args := []any{int64(beforeSeq)}
	for _, t := range terms {
		q += ` AND content LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(t)+"%")
	}
	q += ` ORDER BY turn_seq DESC LIMIT ?`
	args = append(args, limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reasoning
	for rows.Next() {
		var r Reasoning
		if err := rows.Scan(&r.TurnSeq, &r.Models, &r.Content, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
