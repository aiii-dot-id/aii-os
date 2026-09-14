package store

import (
	"fmt"
	"regexp"
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

// .
// .
type MemoryVector struct {
	Store      string
	ID         string
	Basis      string
	ContentSHA string
	Dims       int
	Scale      float64
	Q          []byte
	EmbeddedAt time.Time
}

// .
// .
// .
func (s *Store) PutMemoryVectors(vs []MemoryVector) error {
	if len(vs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO memory_vectors (store, id, basis, content_sha, dims, scale, q, embedded_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, v := range vs {
		if v.Store == "" || v.ID == "" || v.Basis == "" || v.Dims <= 0 || v.Scale <= 0 || len(v.Q) != v.Dims {
			return fmt.Errorf("memory vector for %s/%s is malformed (basis %q, dims %d, %d bytes, scale %v)", v.Store, v.ID, v.Basis, v.Dims, len(v.Q), v.Scale)
		}
		at := v.EmbeddedAt
		if at.IsZero() {
			at = time.Now()
		}
		if _, err := stmt.Exec(v.Store, v.ID, v.Basis, v.ContentSHA, v.Dims, v.Scale, v.Q, at.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// .
// .
func (s *Store) DropMemoryVectorsOffBasis(basis string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM memory_vectors WHERE basis != ?`, basis)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// .
var vectorBaseRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// .
// .
// .
// .
func (s *Store) PruneMemoryVectors(store, base string) (int64, error) {
	if !vectorBaseRe.MatchString(base) {
		return 0, fmt.Errorf("prune memory vectors: %q is not a table name", base)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM memory_vectors WHERE store = ? AND id NOT IN (SELECT id FROM `+base+`)`, store)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// .
func (s *Store) MemoryVectorCount(store, basis string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_vectors WHERE store = ? AND basis = ?`, store, basis).Scan(&n)
	return n, err
}
