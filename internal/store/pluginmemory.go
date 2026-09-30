package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrPluginMemoryQuota = errors.New("plugin memory quota exceeded")

var ErrPluginMemoryNotFound = errors.New("plugin memory not found")

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

func (s *Store) PluginMemoryAdd(m PluginMemory, maxMemories int) error {
	return s.PluginMemoryAddScoped(PluginScope{PluginID: m.PluginID}, m, maxMemories, "")
}

func (s *Store) PluginMemoryAddScoped(scope PluginScope, m PluginMemory, maxMemories int, supersedes string) error {
	if m.ID == "" || scope.PluginID == "" || m.Text == "" || (m.PluginID != "" && m.PluginID != scope.PluginID) {
		return errors.New("plugin memory: id, plugin id and text are required")
	}
	m.PluginID = scope.PluginID
	if m.Attribution == "" {
		m.Attribution = "plugin"
	}
	now := m.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	temp := 0
	activation := ""
	if m.Temp {
		temp = 1
		activation = scope.Activation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	where, args := PluginMemoryPredicate(scope, true)
	var overrides any
	var oldTemp bool
	var root string
	if supersedes != "" {
		if supersedes == m.ID {
			return ErrPluginMemoryNotFound
		}
		err := tx.QueryRow(`SELECT b.temp, COALESCE(b.overrides, '') FROM plugin_memories b WHERE `+where+` AND b.id = ?`, append(args, supersedes)...).Scan(&oldTemp, &root)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s is not current in this activation", ErrPluginMemoryNotFound, supersedes)
		}
		if err != nil {
			return err
		}
		if !oldTemp {
			root = supersedes
		}
		if m.Temp && root != "" {
			overrides = root
		}
	}
	var current int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_memories b WHERE `+where, args...).Scan(&current); err != nil {
		return err
	}
	if supersedes != "" {
		current--
	}
	if maxMemories > 0 && current+1 > maxMemories {
		return fmt.Errorf("%w: %d memories at the %d-memory ceiling", ErrPluginMemoryQuota, current+1, maxMemories)
	}
	if _, err := tx.Exec(`INSERT INTO plugin_memories (id, plugin_id, activation, overrides, text, project, attribution, superseded_by, temp, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		m.ID, m.PluginID, activation, overrides, m.Text, m.Project, m.Attribution, temp, stamp, stamp); err != nil {
		return err
	}
	if supersedes != "" {

		if oldTemp || !m.Temp {
			if _, err := tx.Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ? WHERE id = ? AND plugin_id = ?`, m.ID, stamp, supersedes, scope.PluginID); err != nil {
				return err
			}
		}
		if !m.Temp && oldTemp && root != "" {
			if _, err := tx.Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ? WHERE id = ? AND plugin_id = ?`, m.ID, stamp, root, scope.PluginID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) PluginMemoryGet(pluginID, id string) (PluginMemory, bool, error) {
	return s.PluginMemoryGetScoped(PluginScope{PluginID: pluginID}, id)
}

func (s *Store) PluginMemoryGetScoped(scope PluginScope, id string) (PluginMemory, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m PluginMemory
	var temp int
	var created, updated string
	successor, args := pluginMemorySuccessor(scope)
	where, whereArgs := PluginMemoryPredicate(scope, false)
	args = append(args, whereArgs...)
	args = append(args, id)
	err := s.db.QueryRow(`SELECT b.id, b.plugin_id, b.text, b.project, b.attribution, COALESCE(`+successor+`, ''), b.temp, b.created_at, b.updated_at
		FROM plugin_memories b WHERE `+where+` AND b.id = ?`, args...).
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

func (s *Store) PluginMemorySupersede(pluginID, oldID, newID string) error {
	if oldID == "" || newID == "" || oldID == newID {
		return fmt.Errorf("%w: a memory cannot supersede itself", ErrPluginMemoryNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
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

func (s *Store) PluginMemoryCount(pluginID string) (int, error) {
	return s.PluginMemoryCountScoped(PluginScope{PluginID: pluginID})
}

func (s *Store) PluginMemoryCountScoped(scope PluginScope) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	where, args := PluginMemoryPredicate(scope, true)
	err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_memories b WHERE `+where, args...).Scan(&n)
	return n, err
}

func (s *Store) PluginMemoryClearTemp(pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := clearPluginMemoryTemp(tx, &pluginID); err != nil {
		return err
	}
	return tx.Commit()
}

func clearPluginMemoryTemp(h dbi, pluginID *string) error {
	where, args := pluginTempWhere(pluginID)
	return clearPluginMemoryWhere(h, where, args)
}

func (s *Store) PluginMemoryClearTempScoped(scope PluginScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := clearPluginMemoryWhere(tx, `temp = 1 AND plugin_id = ? AND activation = ?`, []any{scope.PluginID, scope.Activation}); err != nil {
		return err
	}
	return tx.Commit()
}

func clearPluginMemoryWhere(h dbi, where string, args []any) error {
	if _, err := h.Exec(`DELETE FROM memory_access WHERE store = 'plugin_memories'
		AND id IN (SELECT id FROM plugin_memories WHERE `+where+`)`, args...); err != nil {
		return err
	}
	_, err := h.Exec(`DELETE FROM plugin_memories WHERE `+where, args...)
	return err
}
