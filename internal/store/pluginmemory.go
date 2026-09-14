package store

import (
	"database/sql"
	"errors"
	"fmt"
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
var ErrPluginMemoryQuota = errors.New("plugin memory quota exceeded")

// .
// .
var ErrPluginMemoryNotFound = errors.New("plugin memory not found")

// .
type PluginMemory struct {
	ID           string
	PluginID     string
	Text         string
	Project      string
	Attribution  string
	SupersededBy string
	Temp         bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// .
// .
// .
func (s *Store) PluginMemoryAdd(m PluginMemory, maxMemories int) error {
	if m.ID == "" || m.PluginID == "" || m.Text == "" {
		return errors.New("plugin memory: id, plugin id and text are required")
	}
	if m.Attribution == "" {
		m.Attribution = "plugin"
	}
	now := m.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	temp := 0
	if m.Temp {
		temp = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_memories WHERE plugin_id = ? AND superseded_by IS NULL`, m.PluginID).Scan(&current); err != nil {
		return err
	}
	if maxMemories > 0 && current+1 > maxMemories {
		return fmt.Errorf("%w: %d memories at the %d-memory ceiling", ErrPluginMemoryQuota, current+1, maxMemories)
	}
	if _, err := tx.Exec(`INSERT INTO plugin_memories (id, plugin_id, text, project, attribution, superseded_by, temp, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		m.ID, m.PluginID, m.Text, m.Project, m.Attribution, temp, stamp, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

// .
// .
func (s *Store) PluginMemoryGet(pluginID, id string) (PluginMemory, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m PluginMemory
	var temp int
	var created, updated string
	err := s.db.QueryRow(`SELECT id, plugin_id, text, project, attribution, COALESCE(superseded_by, ''), temp, created_at, updated_at
		FROM plugin_memories WHERE plugin_id = ? AND id = ?`, pluginID, id).
		Scan(&m.ID, &m.PluginID, &m.Text, &m.Project, &m.Attribution, &m.SupersededBy, &temp, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginMemory{}, false, nil
	}
	if err != nil {
		return PluginMemory{}, false, err
	}
	m.Temp = temp == 1
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return m, true, nil
}

// .
// .
// .
func (s *Store) PluginMemorySupersede(pluginID, oldID, newID string) error {
	if oldID == "" || newID == "" || oldID == newID {
		return fmt.Errorf("%w: a memory cannot supersede itself", ErrPluginMemoryNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_memories WHERE plugin_id = ? AND id = ?`, pluginID, newID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s is not a memory of this plugin", ErrPluginMemoryNotFound, newID)
	}
	res, err := tx.Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ?
		WHERE plugin_id = ? AND id = ? AND superseded_by IS NULL`,
		newID, time.Now().UTC().Format(time.RFC3339Nano), pluginID, oldID)
	if err != nil {
		return err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return fmt.Errorf("%w: %s is not a current memory of this plugin", ErrPluginMemoryNotFound, oldID)
	}
	return tx.Commit()
}

// .
func (s *Store) PluginMemoryCount(pluginID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_memories WHERE plugin_id = ? AND superseded_by IS NULL`, pluginID).Scan(&n)
	return n, err
}

// .
// .
// .
func (s *Store) PluginMemoryClearTemp(pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memory_access WHERE store = 'plugin_memories'
		AND id IN (SELECT id FROM plugin_memories WHERE plugin_id = ? AND temp = 1)`, pluginID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM plugin_memories WHERE plugin_id = ? AND temp = 1`, pluginID); err != nil {
		return err
	}
	return tx.Commit()
}
