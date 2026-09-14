package store

import (
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// .
// .
// .
// .

// .
type SkillProposal struct {
	ID       string
	TsMs     int64
	Title    string
	Delta    string
	Evidence string
	Status   string
	Verified string
}

var wsIDRe = regexp.MustCompile(`ws_[0-9a-fA-F][0-9a-fA-F-]{7,}`)

// .
// .
// .
// .
// .
func (s *Store) ProposeSkill(title, delta, evidence string) (string, error) {
	if title == "" || delta == "" || evidence == "" {
		return "", fmt.Errorf("a proposal needs title, delta and evidence — a lesson without evidence is an opinion")
	}
	var phantoms []string
	for _, id := range wsIDRe.FindAllString(evidence, -1) {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			phantoms = append(phantoms, id)
		}
	}
	if len(phantoms) > 0 {
		return "", fmt.Errorf("evidence cites sessions that do not exist: %v — cite only trajectories the record holds", phantoms)
	}
	id := "sp_" + uuid.New().String()
	_, err := s.db.Exec(`INSERT INTO skill_proposals (id, ts_ms, title, delta, evidence) VALUES (?, ?, ?, ?, ?)`,
		id, time.Now().UTC().UnixMilli(), title, delta, evidence)
	return id, err
}

// .
func (s *Store) ListSkillProposals(limit int) ([]SkillProposal, error) {
	rows, err := s.db.Query(`SELECT id, ts_ms, title, delta, evidence, status, verified
		FROM skill_proposals ORDER BY ts_ms DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkillProposal
	for rows.Next() {
		var p SkillProposal
		if err := rows.Scan(&p.ID, &p.TsMs, &p.Title, &p.Delta, &p.Evidence, &p.Status, &p.Verified); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// .
// .
func (s *Store) DecideSkillProposal(id, status string) error {
	if status != "promoted" && status != "rejected" {
		return fmt.Errorf("a decision is promoted or rejected, got %q", status)
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
	// .
	// .
	// .
	if status == "promoted" {
		return fmt.Errorf("promotion is unavailable: proposal %s carries verified=none, and no replay verifier exists to change that. Rejecting is available now; promotion returns when a promotion can cite the evidence it claims", id)
	}
	res, err := s.db.Exec(`UPDATE skill_proposals SET status = ?, decided_ms = ? WHERE id = ? AND status = 'proposed'`,
		status, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return fmt.Errorf("no undecided proposal %s", id)
	}
	return nil
}
