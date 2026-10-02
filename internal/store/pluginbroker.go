package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrPluginKVQuota = errors.New("plugin kv quota exceeded")

const pluginKVView = `k.plugin_id = ? AND (k.activation = ? OR
	(k.activation = '' AND k.temp = 0 AND NOT EXISTS
		(SELECT 1 FROM plugin_kv local WHERE local.plugin_id = k.plugin_id
		 AND local.key = k.key AND local.activation = ?)))`

func (s *Store) PluginKVPut(scope PluginScope, key, value string, temp bool, maxKeys, maxTotalBytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !temp {
		scope.Activation = ""
	}
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var count, total int
	err = tx.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(k.key AS BLOB)) + LENGTH(CAST(k.value AS BLOB))), 0)
		   FROM plugin_kv k WHERE `+pluginKVView+` AND k.key <> ?`,
		scope.PluginID, scope.Activation, scope.Activation, key).Scan(&count, &total)
	if err != nil {
		return err
	}
	if count+1 > maxKeys {
		return fmt.Errorf("%w: %d keys at the %d-key ceiling", ErrPluginKVQuota, count+1, maxKeys)
	}
	if total+len(key)+len(value) > maxTotalBytes {
		return fmt.Errorf("%w: %d bytes over the %d-byte namespace ceiling", ErrPluginKVQuota, total+len(key)+len(value), maxTotalBytes)
	}

	tempInt := 0
	if temp {
		tempInt = 1
	}
	_, err = tx.Exec(`INSERT INTO plugin_kv (plugin_id, activation, key, value, temp, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(plugin_id, activation, key) DO UPDATE SET value = excluded.value, temp = excluded.temp,
		hidden = NULL, updated_at = excluded.updated_at`,
		scope.PluginID, scope.Activation, key, value, tempInt, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PluginKVGet(scope PluginScope, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pluginKVGet(s.db, scope, key)
}

func pluginKVGet(h dbi, scope PluginScope, key string) (string, bool, error) {
	var value string
	err := h.QueryRow(`SELECT k.value FROM plugin_kv k WHERE `+pluginKVView+` AND k.key = ? AND k.hidden IS NULL`,
		scope.PluginID, scope.Activation, scope.Activation, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) PluginKVDelete(scope PluginScope, key string, temporary bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !temporary || scope.Activation == "" {
		res, err := s.w().Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND activation = '' AND key = ?`, scope.PluginID, key)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		return n > 0, err
	}
	tx, err := s.w().Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, found, err := pluginKVGet(tx, scope, key)
	if err != nil || !found {
		return false, err
	}
	var inherited bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM plugin_kv
		WHERE plugin_id = ? AND activation = '' AND key = ? AND temp = 0 AND hidden IS NULL)`,
		scope.PluginID, key).Scan(&inherited); err != nil {
		return false, err
	}
	if inherited {
		_, err = tx.Exec(`INSERT INTO plugin_kv (plugin_id, activation, key, value, temp, hidden, updated_at)
			VALUES (?, ?, ?, '', 1, 1, ?) ON CONFLICT(plugin_id, activation, key)
			DO UPDATE SET value = '', temp = 1, hidden = 1, updated_at = excluded.updated_at`,
			scope.PluginID, scope.Activation, key, time.Now().UTC().Format(time.RFC3339Nano))
	} else {
		_, err = tx.Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND activation = ? AND key = ?`,
			scope.PluginID, scope.Activation, key)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) PluginKVList(scope PluginScope, prefix string, limit int) ([]string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, false, nil
	}
	rows, err := s.db.Query(`SELECT k.key FROM plugin_kv k WHERE `+pluginKVView+`
		AND k.hidden IS NULL AND substr(k.key, 1, ?) = ? ORDER BY k.key LIMIT ?`,
		scope.PluginID, scope.Activation, scope.Activation, len([]rune(prefix)), prefix, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, false, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(keys) > limit
	if truncated {
		keys = keys[:limit]
	}
	return keys, truncated, nil
}

func (s *Store) PluginKVClearTemp(scope PluginScope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w().Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND activation = ? AND temp = 1`, scope.PluginID, scope.Activation)
	return err
}

func pluginTempWhere(pluginID *string) (string, []any) {
	if pluginID == nil {
		return "temp = 1", nil
	}
	return "plugin_id = ? AND temp = 1", []any{*pluginID}
}

func clearPluginKVTemp(h dbi, pluginID *string) error {
	where, args := pluginTempWhere(pluginID)
	_, err := h.Exec(`DELETE FROM plugin_kv WHERE `+where, args...)
	return err
}

type PluginReceipt struct {
	ReceiptID   string
	PluginID    string
	Operation   string
	Target      string
	Success     bool
	ReceiptJSON []byte
	CreatedAt   string
}

func (s *Store) AppendPluginReceipt(receiptID, pluginID, operation, target string, success bool, receiptJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := s.w().Exec(`INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		receiptID, pluginID, operation, target, successInt, string(receiptJSON),
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) PluginReceipts(pluginID string) ([]PluginReceipt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT receipt_id, plugin_id, operation, target, success, receipt_json, created_at
		FROM plugin_receipts WHERE plugin_id = ? ORDER BY id ASC`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginReceipt
	for rows.Next() {
		var r PluginReceipt
		var success int
		var js string
		if err := rows.Scan(&r.ReceiptID, &r.PluginID, &r.Operation, &r.Target, &success, &js, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Success = success == 1
		r.ReceiptJSON = []byte(js)
		out = append(out, r)
	}
	return out, rows.Err()
}
