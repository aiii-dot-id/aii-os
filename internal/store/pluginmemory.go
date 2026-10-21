package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrPluginMemoryQuota = errors.New("plugin memory quota exceeded")

type PluginMemoryQuotaError struct {
	Held, Ceiling, Current int

	ReplacedKept int

	Temporary int
}

func (e *PluginMemoryQuotaError) Error() string {
	return fmt.Sprintf("%v: %d memories held at the %d-memory ceiling, %d current and %d replaced",
		ErrPluginMemoryQuota, e.Held, e.Ceiling, e.Current, e.Held-e.Current)
}

func (e *PluginMemoryQuotaError) Unwrap() error { return ErrPluginMemoryQuota }

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
	heldWhere, heldArgs := PluginMemoryPredicate(scope, false)
	var held int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_memories b WHERE `+heldWhere, heldArgs...).Scan(&held); err != nil {
		return "", err
	}
	if maxMemories > 0 && held+1 > maxMemories {
		var current int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM plugin_memories b WHERE `+where, args...).Scan(&current); err != nil {
			return "", err
		}
		refusal := &PluginMemoryQuotaError{Held: held, Ceiling: maxMemories, Current: current}
		if err := tx.QueryRow(`SELECT COALESCE(SUM(b.temp = 1), 0), COALESCE(SUM(b.temp = 0 AND b.superseded_by IS NOT NULL), 0)
			FROM plugin_memories b WHERE `+heldWhere, heldArgs...).Scan(&refusal.Temporary, &refusal.ReplacedKept); err != nil {
			return "", err
		}
		return "", refusal
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

type PluginStored struct {
	PluginID string
	Memories int
	Replaced int
	Keys     int
}

const pluginStoredSQL = `SELECT plugin_id, SUM(memories), SUM(replaced), SUM(keys) FROM (
	SELECT plugin_id, COUNT(*) AS memories, COUNT(superseded_by) AS replaced, 0 AS keys
	  FROM plugin_memories GROUP BY plugin_id
	UNION ALL
	SELECT plugin_id, 0, 0, COUNT(*) FROM plugin_kv WHERE hidden IS NULL GROUP BY plugin_id)`

func (s *Store) PluginsStored() ([]PluginStored, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(pluginStoredSQL + ` GROUP BY plugin_id ORDER BY plugin_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginStored
	for rows.Next() {
		var p PluginStored
		if err := rows.Scan(&p.PluginID, &p.Memories, &p.Replaced, &p.Keys); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RemovePluginStored(pluginID string) (PluginStored, error) {
	if pluginID == "" {
		return PluginStored{}, errors.New("remove a plugin's stored data: a plugin id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return PluginStored{}, err
	}
	defer tx.Rollback()
	removed := PluginStored{PluginID: pluginID}
	err = tx.QueryRow(pluginStoredSQL+` WHERE plugin_id = ? GROUP BY plugin_id`, pluginID).
		Scan(&removed.PluginID, &removed.Memories, &removed.Replaced, &removed.Keys)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PluginStored{}, err
	}
	if _, err := tx.Exec(`DELETE FROM memory_vectors WHERE store = 'plugin_memories'
		AND id IN (SELECT id FROM plugin_memories WHERE plugin_id = ?)`, pluginID); err != nil {
		return PluginStored{}, err
	}
	if err := clearPluginMemoryWhere(tx, `plugin_id = ?`, []any{pluginID}); err != nil {
		return PluginStored{}, err
	}
	if _, err := tx.Exec(`DELETE FROM plugin_kv WHERE plugin_id = ?`, pluginID); err != nil {
		return PluginStored{}, err
	}
	return removed, tx.Commit()
}

const (
	PluginStoredPageRows  = 20
	PluginStoredPageBytes = 256 << 10
)

type PluginStoredKey struct {
	Key            string
	Value          string
	Temp           bool
	DeletionMarker bool
	UpdatedAt      time.Time
}

func (s *Store) PluginStoredMemories(pluginID string, before int64) (page []PluginMemory, total int, next int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_memories WHERE plugin_id = ?`, pluginID).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	rows, err := s.db.Query(`SELECT rowid, id, text, project, attribution, COALESCE(superseded_by, ''), temp, created_at, updated_at
		FROM plugin_memories WHERE plugin_id = ? AND (? = 0 OR rowid < ?) ORDER BY rowid DESC LIMIT ?`,
		pluginID, before, before, PluginStoredPageRows+1)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	size, last := 0, int64(0)
	for rows.Next() {
		m := PluginMemory{PluginID: pluginID}
		var rowid int64
		var temp int
		var created, updated string
		if err := rows.Scan(&rowid, &m.ID, &m.Text, &m.Project, &m.Attribution, &m.SupersededBy, &temp, &created, &updated); err != nil {
			return nil, 0, 0, err
		}
		if len(page) == PluginStoredPageRows || (len(page) > 0 && size+len(m.Text) > PluginStoredPageBytes) {
			next = last
			break
		}
		m.Temp = temp == 1
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		page, size, last = append(page, m), size+len(m.Text), rowid
	}
	return page, total, next, rows.Err()
}

func (s *Store) PluginStoredKeys(pluginID string, before int64) (page []PluginStoredKey, total int, next int64, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plugin_kv WHERE plugin_id = ?`, pluginID).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	rows, err := s.db.Query(`SELECT rowid, key, value, temp, hidden IS NOT NULL, updated_at
		FROM plugin_kv WHERE plugin_id = ? AND (? = 0 OR rowid < ?) ORDER BY rowid DESC LIMIT ?`,
		pluginID, before, before, PluginStoredPageRows+1)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	size, last := 0, int64(0)
	for rows.Next() {
		var k PluginStoredKey
		var rowid int64
		var temp, marker int
		var updated string
		if err := rows.Scan(&rowid, &k.Key, &k.Value, &temp, &marker, &updated); err != nil {
			return nil, 0, 0, err
		}
		if len(page) == PluginStoredPageRows || (len(page) > 0 && size+len(k.Key)+len(k.Value) > PluginStoredPageBytes) {
			next = last
			break
		}
		k.Temp, k.DeletionMarker = temp == 1, marker == 1
		k.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		page, size, last = append(page, k), size+len(k.Key)+len(k.Value), rowid
	}
	return page, total, next, rows.Err()
}
