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
	ID                string
	PluginID          string
	Text              string
	Project           string
	Attribution       string
	Applicability     string
	WorkSession       string
	SourceIncarnation string
	Evidence          string
	SupersededBy      string
	Temp              bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (s *Store) PluginMemoryAdd(scope PluginScope, m PluginMemory, maxMemories int, supersedes string) error {
	_, err := s.pluginMemoryAdd(scope, m, maxMemories, supersedes, false)
	return err
}

func (s *Store) PluginMemoryAddExact(scope PluginScope, m PluginMemory, maxMemories int) (string, error) {
	return s.pluginMemoryAdd(scope, m, maxMemories, "", true)
}

func (s *Store) pluginMemoryAdd(scope PluginScope, m PluginMemory, maxMemories int, supersedes string, exact bool) (string, error) {
	if m.ID == "" || scope.PluginID == "" || m.Text == "" || (m.PluginID != "" && m.PluginID != scope.PluginID) {
		return "", errors.New("plugin memory: id, plugin id and text are required")
	}
	m.PluginID = scope.PluginID
	if m.Applicability == "" {
		m.Applicability = "legacy"
	}
	if m.Evidence == "" {
		m.Evidence = "[]"
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
	activation := ""
	if m.Temp {
		temp = 1
		activation = scope.Activation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if exact {
		id, err := pluginMemoryEquivalent(tx, scope, m)
		if err != nil {
			return "", err
		}
		if id != "" {
			return id, nil
		}
	}
	where, args := PluginMemoryPredicate(scope, true)
	var overrides any
	var oldTemp bool
	var root string
	var oldApplicability, oldProject, oldSession string
	if supersedes != "" {
		if supersedes == m.ID {
			return "", ErrPluginMemoryNotFound
		}
		err := tx.QueryRow(`SELECT b.temp, COALESCE(b.overrides, ''), b.applicability, b.project, b.work_session FROM plugin_memories b WHERE `+where+` AND b.id = ?`, append(args, supersedes)...).Scan(&oldTemp, &root, &oldApplicability, &oldProject, &oldSession)
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s is not current in this activation", ErrPluginMemoryNotFound, supersedes)
		}
		if err != nil {
			return "", err
		}
		if oldApplicability != m.Applicability || (m.Applicability == "task" && (oldSession != m.WorkSession || oldProject != m.Project)) || (m.Applicability == "project" && oldProject != m.Project) {
			return "", fmt.Errorf("%w: applicability differs", ErrPluginMemoryNotFound)
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
		return "", err
	}
	if supersedes != "" {
		current--
	}
	if maxMemories > 0 && current+1 > maxMemories {
		return "", fmt.Errorf("%w: %d memories at the %d-memory ceiling", ErrPluginMemoryQuota, current+1, maxMemories)
	}
	if _, err := tx.Exec(`INSERT INTO plugin_memories (id, plugin_id, activation, overrides, text, project, attribution, superseded_by, temp, created_at, updated_at, applicability, work_session, source_incarnation, evidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.PluginID, activation, overrides, m.Text, m.Project, m.Attribution, temp, stamp, stamp, m.Applicability, m.WorkSession, m.SourceIncarnation, m.Evidence); err != nil {
		return "", err
	}
	if supersedes != "" {

		if oldTemp || !m.Temp {
			if _, err := tx.Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ? WHERE id = ? AND plugin_id = ?`, m.ID, stamp, supersedes, scope.PluginID); err != nil {
				return "", err
			}
		}
		if !m.Temp && oldTemp && root != "" {
			if _, err := tx.Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ? WHERE id = ? AND plugin_id = ?`, m.ID, stamp, root, scope.PluginID); err != nil {
				return "", err
			}
		}
	}
	return m.ID, tx.Commit()
}

func (s *Store) PluginMemoryGet(scope PluginScope, id string) (PluginMemory, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m PluginMemory
	var temp int
	var created, updated string
	successor, args := pluginMemorySuccessor(scope)
	where, whereArgs := PluginMemoryPredicate(scope, false)
	args = append(args, whereArgs...)
	args = append(args, id)
	err := s.db.QueryRow(`SELECT b.id, b.plugin_id, b.text, b.project, b.attribution, COALESCE(`+successor+`, ''), b.temp, b.created_at, b.updated_at, b.applicability, b.work_session, b.source_incarnation, b.evidence
		FROM plugin_memories b WHERE `+where+` AND b.id = ?`, args...).
		Scan(&m.ID, &m.PluginID, &m.Text, &m.Project, &m.Attribution, &m.SupersededBy, &temp, &created, &updated, &m.Applicability, &m.WorkSession, &m.SourceIncarnation, &m.Evidence)
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

func (s *Store) PluginMemoryCount(scope PluginScope) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	where, args := PluginMemoryPredicate(scope, true)
	err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_memories b WHERE `+where, args...).Scan(&n)
	return n, err
}

func clearPluginMemoryTemp(h dbi, pluginID *string) error {
	where, args := pluginTempWhere(pluginID)
	return clearPluginMemoryWhere(h, where, args)
}

func (s *Store) PluginMemoryClearTemp(scope PluginScope) error {
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
