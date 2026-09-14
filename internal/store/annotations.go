package store

// .
// .
// .
// .
// .
// .
// .

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// .
// .
func (s *Store) AddConversationTurnSeq(role, content string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := "turn_" + uuid.New().String()
	if _, err := s.db.Exec(
		`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, project_id)
		 VALUES (?, 'default', ?, ?, COALESCE((SELECT MAX(turn_seq) FROM conversations), 0) + 1, ?, ?)`,
		id, role, content, time.Now().UTC().Format(time.RFC3339Nano), s.activeProject,
	); err != nil {
		return 0, err
	}
	var seq uint64
	if err := s.db.QueryRow(`SELECT turn_seq FROM conversations WHERE id = ?`, id).Scan(&seq); err != nil {
		return 0, fmt.Errorf("read the recorded turn's seq: %w", err)
	}
	return seq, nil
}

// .
// .
// .
func (s *Store) AnnotateTurn(turnSeq uint64, kind, key, payload string) error {
	if turnSeq == 0 || kind == "" {
		return fmt.Errorf("an annotation names a recorded turn and a kind")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE turn_seq = ?`, turnSeq).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("no recorded turn %d", turnSeq)
	}
	_, err := s.db.Exec(
		`INSERT INTO turn_annotations (turn_seq, kind, key, payload, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(turn_seq, kind) DO UPDATE SET key = excluded.key, payload = excluded.payload, created_at = excluded.created_at`,
		turnSeq, kind, key, payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// .
// .
func (s *Store) TurnSeqByAnnotation(kind, key string) (uint64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	err := s.db.QueryRow(`SELECT turn_seq FROM turn_annotations WHERE kind = ? AND key = ? ORDER BY turn_seq DESC LIMIT 1`, kind, key).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

// .
// .
func (s *Store) TurnAnnotations(kind string, seqs []uint64) (map[uint64]string, error) {
	out := map[uint64]string{}
	if len(seqs) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(seqs)), ",")
	args := make([]interface{}, 0, len(seqs)+1)
	args = append(args, kind)
	for _, seq := range seqs {
		args = append(args, seq)
	}
	rows, err := s.db.Query(`SELECT turn_seq, payload FROM turn_annotations WHERE kind = ? AND turn_seq IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq uint64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, err
		}
		out[seq] = payload
	}
	return out, rows.Err()
}
