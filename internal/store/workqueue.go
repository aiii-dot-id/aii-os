package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// --- Work Queue: durable dispatch with leases (docs/WORK_QUEUE.md) ---
//
// The queue owns DELIVERY and DURABILITY; handlers own MEANING. Agent
// frameworks (future plugins) enqueue their own steps; TIME enqueues
// alarm dispatch; the identity will schedule agent work by enqueuing to
// a plugin kind. The queue never learns what a plan is.

// WorkItem is one durable unit of doing.
type WorkItem struct {
	ID         string
	Kind       string // routing key ("facility.dream", "witness.anchor", "wake.timer", "plugin.<id>.task", "agent.step")
	Payload    string // JSON, opaque to the queue
	DedupKey   string // exactly-once-effects token ("" = none)
	Source     string // who enqueued: identity | time | plugin:<id> | substrate (provenance, never permission)
	State      string // PENDING | CLAIMED | DONE | FAILED
	Priority   int
	Scheduled  int64 // unix ms visibility (0 = now)
	ClaimedAt  int64
	LeaseMs    int64
	DoneAt     int64
	RetryCount int
	MaxRetries int
	Error      string
	CreatedMs  int64
}

// EnqueueWork inserts a durable work item. Dedup: an outstanding
// (PENDING/CLAIMED) row with the same (kind, dedup_key) is not
// duplicated — the enqueue is suppressed (returning the existing row's
// id) — but terminal rows never block new work: a new firing is new
// work. Exactly-once EFFECTS, not exactly-once delivery.
func (s *Store) EnqueueWork(item *WorkItem) (string, error) {
	if s.workFrozen() {
		return "", errWorkFrozen(item.Kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueWorkLocked(s.db, item)
}

// errWorkFrozen is the one refusal both enqueue paths give.
//
// Enqueue was the single mutation the freeze let through, on the reading
// that recording intent is harmless. Canon says otherwise, in two places:
// "Cognition and TIME diagnostics ... must not enqueue work", and under
// alarm firing, "No alarm-owner callback, rhythm_alarms update,
// WORK-QUEUE ENQUEUE ... while safe mode is active"
// (opensuperclaw docs/10-core/SAFE_MODE.md 3.3.1). R55 says it shorter:
// no database writes while integrity is unverified.
//
// The reading was wrong on its own terms too. A queued row is not a
// note of intent — it is work the executor runs the moment the freeze
// lifts, chosen by an identity whose integrity was in question when it
// chose. SAFE is a forensic snapshot; a snapshot that grows is not one.
func errWorkFrozen(kind string) error {
	return fmt.Errorf("work queue frozen (SAFE): %q not enqueued — "+
		"no database writes while integrity is unverified (R55; canon SAFE_MODE 3.3.1)", kind)
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
	// Priority zero-value = DEFAULT (5), not highest — a caller that
	// forgets priority must not preempt everything. Explicit high: 1-4.
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
			return existing, nil // suppressed duplicate in flight
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

// ClaimWork atomically claims the next due item (priority, then age).
// Single-writer v1: one executor goroutine, one connection — the claim
// is a transaction-scoped UPDATE, no SKIP LOCKED needed (M4).
func (s *Store) ClaimWork(kinds []string, nowMs int64) (*WorkItem, error) {
	if s.workFrozen() {
		return nil, nil // SAFE freeze: rows are the forensic snapshot
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Finalize the SELECT before the UPDATE — never hold a statement
	// across another prepare on the same connection.
	row := struct {
		id string
	}{}
	q := `SELECT id FROM work_queue WHERE state = 'PENDING' AND scheduled_ms <= ?
	      ORDER BY priority ASC, created_ms ASC LIMIT 1`
	args := []interface{}{nowMs}
	if len(kinds) > 0 {
		// Kinds may be exact or namespace wildcards ("alarm.*"). One
		// clause per kind keeps the semantics explicit.
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
		q = `SELECT id FROM work_queue WHERE state = 'PENDING' AND scheduled_ms <= ? AND (` + clauses[4:] + `)
		      ORDER BY priority ASC, created_ms ASC LIMIT 1`
	}
	err := s.db.QueryRow(q, args...).Scan(&row.id)
	if err == sql.ErrNoRows {
		return nil, nil // nothing due — caller polls
	}
	if err != nil {
		// Finding 17: swallowing real DB errors here made a failing store
		// indistinguishable from an empty queue — the executor idled
		// politely through an outage.
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

// CompleteWork marks a claimed item DONE (terminal — SAFE-mode forensics).
// FROZEN (SAFE): refused — the row stays CLAIMED as the honest ambiguous
// record ("in flight when integrity broke; the effect may or may not
// have landed"). The lease sweep re-drives it after unfreeze.
func (s *Store) CompleteWork(id string) error {
	if s.workFrozen() {
		return fmt.Errorf("work queue frozen (SAFE): completion withheld — row %s stays CLAIMED as forensic record", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().UnixMilli()
	_, err := s.db.Exec(
		`UPDATE work_queue SET state = 'DONE', done_at = ? WHERE id = ? AND state = 'CLAIMED'`,
		now, id,
	)
	return err
}

// retryBackoffBaseMs is the first retry delay; each further attempt
// doubles it, capped by retryBackoffMaxShift.
//
// A failed row returned to PENDING with scheduled_ms untouched, and
// ClaimWork selects `state = 'PENDING' AND scheduled_ms <= now` — so the
// very next executor pass reclaimed it. A failing subagent.run burned
// all three of its attempts back to back, three LLM calls deep, in the
// time it takes to loop. Retrying instantly is not retrying; it is the
// same failure three times.
//
// Mechanism constants, like the supervisor's close graces — not operator
// knobs. 1s, 2s, 4s ... 64s is long enough for a provider blip or a
// rate limit to clear and short enough that real work is not stalled.
const (
	retryBackoffBaseMs   = 1000
	retryBackoffMaxShift = 6
)

// FailWork records a failure: retry (back to PENDING, after a backoff)
// while retries remain, else FAILED (terminal, error preserved — the
// forensic record).
// FailWork: frozen under SAFE (same forensic contract as Complete).
func (s *Store) FailWork(id string, errMsg string) error {
	if s.workFrozen() {
		return fmt.Errorf("work queue frozen (SAFE): failure withheld — row %s stays CLAIMED as forensic record", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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

// SweepExpiredLeases is crash recovery as a query: CLAIMED rows whose
// lease elapsed return to PENDING (retry) or FAILED (exhausted).
// Terminal rows are NEVER touched — they are the forensic record (M2).
func (s *Store) SweepExpiredLeases(nowMs int64) (int, error) {
	if s.workFrozen() {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Same backoff as FailWork, for the same reason: an expired lease is
	// a failure, and reclaiming it in the same breath repeats it.
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

// WorkQueueFrozen is the SAFE-mode freeze flag: when set, NOTHING moves
// — no claims, no sweeps, no state mutations, and no new rows. The queue
// is the forensic snapshot of what was in flight (§2.5), and a snapshot
// that grows is not one.
func (s *Store) SetWorkQueueFrozen(frozen bool) {
	s.mu.Lock()
	s.wqFrozen = frozen
	s.mu.Unlock()
}

func (s *Store) workFrozen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wqFrozen
}

// PendingWorkCount reports due work (continuity/health surfacing).
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

// newWorkID: time-ordered unique id (uuidv7-shaped, no dependency).
//
// rand.Read's error is ignored because it cannot be non-nil: since Go
// 1.24 crypto/rand.Read "never returns an error, and always fills b
// entirely" — it crashes the program irrecoverably instead. Handling it
// would be writing a branch the language guarantees is dead. Noted
// because a review flagged the unchecked return (Sol 5.6, 2026-08-24);
// it is a pattern match, not a defect on this toolchain.
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
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return hex.EncodeToString(b[:])
}

// CountLiveWork counts non-terminal items of a kind — the agency
// parallel-spawn ceiling reads this (PENDING or CLAIMED; DONE/FAILED
// never block new work).
func (s *Store) CountLiveWork(kind string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND state IN ('PENDING','CLAIMED')`, kind,
	).Scan(&n)
	return n, err
}

// EnqueueWorkWithSessionBelowLimit atomically creates one queue item and
// its work session below the live-item ceiling. Neither can exist alone.
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
