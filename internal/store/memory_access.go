package store

import (
	"database/sql"
	"encoding/json"
	"errors"
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

// .
// .
const accessHistoryLimit = 32

// .
// .
type MemoryRef struct {
	Store string
	ID    string
}

// .
type MemoryAccess struct {
	MemoryRef
	Count   int64
	LastAt  time.Time
	History []time.Time
}

func checkMemoryRef(r MemoryRef) error {
	if _, ok := Catalog[r.Store]; !ok {
		return fmt.Errorf("memory access: %q is not a catalogued store", r.Store)
	}
	if r.ID == "" {
		return fmt.Errorf("memory access: empty id in store %s", r.Store)
	}
	return nil
}

// .
// .
// .
func (s *Store) RecordMemoryAccess(at time.Time, refs ...MemoryRef) error {
	for _, r := range refs {
		if err := checkMemoryRef(r); err != nil {
			return err
		}
	}
	if len(refs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := at.UTC().Format(time.RFC3339Nano)
	for _, r := range refs {
		var raw string
		err := tx.QueryRow(`SELECT history FROM memory_access WHERE store = ? AND id = ?`, r.Store, r.ID).Scan(&raw)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			raw = "[]"
		case err != nil:
			return fmt.Errorf("read the access history of %s/%s: %w", r.Store, r.ID, err)
		}
		var history []string
		if err := json.Unmarshal([]byte(raw), &history); err != nil {
			// .
			// .
			// .
			history = nil
		}
		history = append(history, stamp)
		if len(history) > accessHistoryLimit {
			history = history[len(history)-accessHistoryLimit:]
		}
		enc, err := json.Marshal(history)
		if err != nil {
			return fmt.Errorf("encode the access history of %s/%s: %w", r.Store, r.ID, err)
		}
		if _, err := tx.Exec(`INSERT INTO memory_access (store, id, count, last_at, history) VALUES (?, ?, 1, ?, ?)
			ON CONFLICT(store, id) DO UPDATE SET
				count = memory_access.count + 1,
				last_at = excluded.last_at,
				history = excluded.history`,
			r.Store, r.ID, stamp, string(enc)); err != nil {
			return fmt.Errorf("record the access of %s/%s: %w", r.Store, r.ID, err)
		}
	}
	return tx.Commit()
}

// .
// .
func (s *Store) MemoryAccessOf(ref MemoryRef) (MemoryAccess, bool, error) {
	if err := checkMemoryRef(ref); err != nil {
		return MemoryAccess{}, false, err
	}
	all, err := s.MemoryAccesses(ref.Store, []string{ref.ID})
	if err != nil {
		return MemoryAccess{}, false, err
	}
	a, ok := all[ref.ID]
	return a, ok, nil
}

// .
// .
// .
// .
func (s *Store) MemoryAccesses(store string, ids []string) (map[string]MemoryAccess, error) {
	if _, ok := Catalog[store]; !ok {
		return nil, fmt.Errorf("memory access: %q is not a catalogued store", store)
	}
	out := map[string]MemoryAccess{}
	if len(ids) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	const chunk = 200
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		args := make([]any, 0, len(part)+1)
		args = append(args, store)
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := s.db.Query(
			`SELECT id, count, last_at, history FROM memory_access WHERE store = ? AND id IN (?`+strings.Repeat(",?", len(part)-1)+`)`,
			args...)
		if err != nil {
			return nil, fmt.Errorf("read memory access: %w", err)
		}
		for rows.Next() {
			var a MemoryAccess
			var last, raw string
			if err := rows.Scan(&a.ID, &a.Count, &last, &raw); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan memory access: %w", err)
			}
			a.Store = store
			if t, err := time.Parse(time.RFC3339Nano, last); err == nil {
				a.LastAt = t
			}
			var stamps []string
			if err := json.Unmarshal([]byte(raw), &stamps); err == nil {
				for _, st := range stamps {
					if t, err := time.Parse(time.RFC3339Nano, st); err == nil {
						a.History = append(a.History, t)
					}
				}
			}
			out[a.ID] = a
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read memory access: %w", err)
		}
		rows.Close()
	}
	return out, nil
}
