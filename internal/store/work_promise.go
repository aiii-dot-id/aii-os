package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func (s *Store) PromiseChangedAt(sessionID string) (resumesOn string, at time.Time, seq uint64, turn string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var created string
	err = s.db.QueryRow(`SELECT COALESCE(json_extract(details,'$.work.resumes_on'),''), created_at, turn_seq, turn_id FROM conversations
		WHERE kind = ? AND json_extract(details,'$.work.session') = ? AND json_extract(details,'$.work.resumes_on') IS NOT NULL
		ORDER BY turn_seq DESC LIMIT 1`, string(interaction.Notice), sessionID).Scan(&resumesOn, &created, &seq, &turn)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, 0, "", nil
	}
	if err != nil {
		return "", time.Time{}, 0, "", err
	}
	at, err = time.Parse(time.RFC3339Nano, created)
	return resumesOn, at, seq, turn, err
}

func (s *Store) ContinuationRanSince(sessionID string, afterSeq uint64) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ran bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversations c JOIN turn_annotations a ON a.turn_seq = c.turn_seq AND a.kind = ? AND a.key = 'continuation'
		WHERE c.turn_seq > ? AND json_extract(a.payload, '$.session') = ?)`,
		AnnotationTurnSource, afterSeq, sessionID).Scan(&ran)
	return ran, err
}

func ContinuationFactNames(sessionID string) string { return "Work session " + sessionID + " " }

func (s *Store) PromiseProgressSince(promise string, after time.Time) (bool, error) {
	if promise == "" {
		return false, nil
	}
	s.mu.RLock()
	var updated sql.NullInt64
	err := s.db.QueryRow(`SELECT updated_seq FROM commitments WHERE id = ?
		UNION ALL SELECT updated_seq FROM intentions WHERE id = ? LIMIT 1`, promise, promise).Scan(&updated)
	s.mu.RUnlock()
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !updated.Valid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	at, present, err := s.LedgerTimeAt(uint64(updated.Int64))
	if err != nil || !present {
		return false, err
	}
	return at.After(after), nil
}

func (s *Store) PromiseOf(id string) (kind, counterpart string, found bool, err error) {
	if id == "" {
		return "", "", false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var who sql.NullString
	err = s.db.QueryRow(`SELECT 'commitment', COALESCE((SELECT counterpart_name FROM relationships r WHERE r.id = c.counterpart_id), c.counterpart_id) FROM commitments c WHERE c.id = ?
		UNION ALL SELECT 'intention', NULL FROM intentions WHERE id = ? LIMIT 1`, id, id).Scan(&kind, &who)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return kind, who.String, true, nil
}
