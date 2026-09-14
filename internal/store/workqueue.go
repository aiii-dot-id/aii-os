package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// .
// .
// .
// .
// .
// .

// .
type WorkItem struct {
	ID         string
	Kind       string
	Payload    string
	DedupKey   string
	Source     string
	State      string
	Priority   int
	Scheduled  int64
	ClaimedAt  int64
	LeaseMs    int64
	DoneAt     int64
	RetryCount int
	MaxRetries int
	Error      string
	CreatedMs  int64
}

// .
// .
// .
// .
// .
func (s *Store) EnqueueWork(item *WorkItem) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return "", errWorkFrozen(item.Kind)
	}
	return s.enqueueWorkLocked(s.db, item)
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
// .
func errWorkFrozen(kind string) error {
	return fmt.Errorf("work queue frozen (SAFE): %q not enqueued — "+
		"no database writes while integrity is unverified", kind)
}

func (s *Store) enqueueWorkLocked(h dbi, item *WorkItem) (string, error) {
	if item.Kind == "" {
		return "", fmt.Errorf("work item requires kind")
	}
	if item.State == "" {
		item.State = "PENDING"
	}
	if item.MaxRetries == 0 {
		item.MaxRetries = 3
	}
	// .
	// .
	if item.Priority == 0 {
		item.Priority = 5
	}
	if item.LeaseMs == 0 {
		item.LeaseMs = 300000
	}
	item.CreatedMs = time.Now().UTC().UnixMilli()
	if item.ID == "" {
		item.ID = newWorkID()
	}

	if item.DedupKey != "" {
		var existing string
		err := h.QueryRow(
			`SELECT id FROM work_queue WHERE kind = ? AND dedup_key = ? AND state IN ('PENDING','CLAIMED') LIMIT 1`,
			item.Kind, item.DedupKey,
		).Scan(&existing)
		if err == nil {
			return existing, nil
		}
	}

	_, err := h.Exec(
		`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state, priority, scheduled_ms, lease_ms, retry_count, max_retries, created_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		item.ID, item.Kind, item.Payload, nullable(item.DedupKey), item.Source,
		item.State, item.Priority, item.Scheduled, item.LeaseMs, item.MaxRetries, item.CreatedMs,
	)
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) SetClaimLimit(kind string, limit int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimLimits == nil {
		s.claimLimits = map[string]int{}
	}
	if limit <= 0 {
		delete(s.claimLimits, kind)
		return
	}
	s.claimLimits[kind] = limit
}

// .
// .
func (s *Store) claimExclusionLocked() (string, []interface{}) {
	clause := ""
	var args []interface{}
	for kind, limit := range s.claimLimits {
		clause += " AND NOT (kind = ? AND (SELECT COUNT(*) FROM work_queue w2 WHERE w2.kind = ? AND w2.state = 'CLAIMED') >= ?)"
		args = append(args, kind, kind, limit)
	}
	return clause, args
}

// .
func (s *Store) RunningWorkCount(kind string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND state = 'CLAIMED'`, kind).Scan(&n)
	return n, err
}

// .
// .
// .
func (s *Store) SubagentQueueStates(kind string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT dedup_key, state FROM work_queue WHERE kind = ? AND dedup_key IS NOT NULL AND state IN ('PENDING','CLAIMED')`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, state string
		if err := rows.Scan(&key, &state); err != nil {
			return nil, err
		}
		if i := strings.Index(key, "#"); i >= 0 {
			key = key[:i]
		}
		if out[key] != "CLAIMED" {
			out[key] = state
		}
	}
	return out, rows.Err()
}

func (s *Store) ClaimWork(kinds []string, nowMs int64) (*WorkItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return nil, nil
	}
	excl, exclArgs := s.claimExclusionLocked()

	// .
	// .
	row := struct {
		id string
	}{}
	q := `SELECT id FROM work_queue WHERE state = 'PENDING' AND scheduled_ms <= ?` + excl + `
	      ORDER BY priority ASC, created_ms ASC LIMIT 1`
	args := append([]interface{}{nowMs}, exclArgs...)
	if len(kinds) > 0 {
		// .
		// .
		clauses := ""
		for _, k := range kinds {
			if len(k) > 2 && k[len(k)-2:] == ".*" {
				clauses += " OR kind LIKE ?"
				args = append(args, k[:len(k)-1]+"%")
			} else {
				clauses += " OR kind = ?"
				args = append(args, k)
			}
		}
		q = `SELECT id FROM work_queue WHERE state = 'PENDING' AND scheduled_ms <= ?` + excl + ` AND (` + clauses[4:] + `)
		      ORDER BY priority ASC, created_ms ASC LIMIT 1`
		args = append(append([]interface{}{nowMs}, exclArgs...), args[1+len(exclArgs):]...)
	}
	err := s.db.QueryRow(q, args...).Scan(&row.id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		// .
		// .
		// .
		return nil, fmt.Errorf("claim select: %w", err)
	}

	var w WorkItem
	var dedup sql.NullString
	err = s.db.QueryRow(
		`UPDATE work_queue SET state = 'CLAIMED', claimed_at = ?
		 WHERE id = ? AND state = 'PENDING' RETURNING id, kind, payload, dedup_key, source, state, priority, scheduled_ms, claimed_at, lease_ms, retry_count, max_retries, created_ms`,
		nowMs, row.id,
	).Scan(&w.ID, &w.Kind, &w.Payload, &dedup, &w.Source, &w.State, &w.Priority,
		&w.Scheduled, &w.ClaimedAt, &w.LeaseMs, &w.RetryCount, &w.MaxRetries, &w.CreatedMs)
	w.DedupKey = dedup.String
	if err != nil {
		return nil, fmt.Errorf("claim %s: %w", row.id, err)
	}
	return &w, nil
}

// .
// .
// .
// .
func (s *Store) CompleteWork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return fmt.Errorf("work queue frozen (SAFE): completion withheld — row %s stays CLAIMED as forensic record", id)
	}
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.Exec(
		`UPDATE work_queue SET state = 'DONE', done_at = ? WHERE id = ? AND state = 'CLAIMED'`,
		now, id,
	)
	return err
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
const (
	retryBackoffBaseMs   = 1000
	retryBackoffMaxShift = 6
)

// .
// .
// .
// .
func (s *Store) FailWork(id string, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return fmt.Errorf("work queue frozen (SAFE): failure withheld — row %s stays CLAIMED as forensic record", id)
	}
	_, err := s.db.Exec(
		`UPDATE work_queue
		   SET state = CASE WHEN retry_count + 1 >= max_retries THEN 'FAILED' ELSE 'PENDING' END,
		       retry_count = retry_count + 1,
		       claimed_at = 0,
		       scheduled_ms = ? + (? << MIN(retry_count, ?)),
		       error_msg = ?
		 WHERE id = ? AND state = 'CLAIMED'`,
		time.Now().UTC().UnixMilli(), retryBackoffBaseMs, retryBackoffMaxShift, errMsg, id,
	)
	return err
}

// .
// .
// .
func (s *Store) SweepExpiredLeases(nowMs int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return 0, nil
	}
	// .
	// .
	res, err := s.db.Exec(
		`UPDATE work_queue
		   SET state = CASE WHEN retry_count + 1 >= max_retries THEN 'FAILED' ELSE 'PENDING' END,
		       retry_count = retry_count + 1,
		       claimed_at = 0,
		       scheduled_ms = ? + (? << MIN(retry_count, ?)),
		       error_msg = COALESCE(NULLIF(error_msg,''), 'lease expired')
		 WHERE state = 'CLAIMED' AND claimed_at + lease_ms < ?`,
		nowMs, retryBackoffBaseMs, retryBackoffMaxShift, nowMs,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) SetWorkQueueFrozen(frozen bool) {
	s.mu.Lock()
	s.wqFrozen = frozen
	s.mu.Unlock()
}

// .
func (s *Store) PendingWorkCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM work_queue WHERE state IN ('PENDING','CLAIMED')`,
	).Scan(&n)
	return n, err
}

func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// .
// .
// .
// .
// .
// .
// .
// .
func newWorkID() string {
	var b [16]byte
	rand.Read(b[:])
	ms := time.Now().UTC().UnixMilli()
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// .
// .
// .
func (s *Store) CountLiveWork(kind string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND state IN ('PENDING','CLAIMED')`, kind,
	).Scan(&n)
	return n, err
}

// .
// .
func (s *Store) EnqueueWorkWithSessionBelowLimit(item *WorkItem, limit int, sessionID, description string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wqFrozen {
		return 0, false, errWorkFrozen(item.Kind)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var live int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND state IN ('PENDING','CLAIMED')`, item.Kind,
	).Scan(&live); err != nil {
		return 0, false, err
	}
	if live >= limit {
		return live, false, nil
	}
	if _, err := s.enqueueWorkLocked(tx, item); err != nil {
		return live, false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO work_sessions (id, description, status, state, project_id) VALUES (?, ?, 'active', '', ?)`,
		sessionID, description, s.activeProject,
	); err != nil {
		return live, false, err
	}
	if err := tx.Commit(); err != nil {
		return live, false, err
	}
	return live, true, nil
}
