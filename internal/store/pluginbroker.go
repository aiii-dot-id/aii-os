package store

// .
// .
// .
// .
// .
// .

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// .
// .
// .
var ErrPluginKVQuota = errors.New("plugin kv quota exceeded")

// .
// .
// .
// .
// .
// .
func (s *Store) PluginKVPut(pluginID, key, value string, temp bool, maxKeys, maxTotalBytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// .
	// .
	// .
	// .
	var count, total int
	err = tx.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(key AS BLOB)) + LENGTH(CAST(value AS BLOB))), 0)
		   FROM plugin_kv WHERE plugin_id = ? AND key <> ?`, pluginID, key).Scan(&count, &total)
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
	_, err = tx.Exec(`INSERT INTO plugin_kv (plugin_id, key, value, temp, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(plugin_id, key) DO UPDATE SET value = excluded.value, temp = excluded.temp, updated_at = excluded.updated_at`,
		pluginID, key, value, tempInt, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// .
func (s *Store) PluginKVGet(pluginID, key string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var value string
	err := s.db.QueryRow(`SELECT value FROM plugin_kv WHERE plugin_id = ? AND key = ?`, pluginID, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// .
// .
func (s *Store) PluginKVDelete(pluginID, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND key = ?`, pluginID, key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
func (s *Store) PluginKVList(pluginID, prefix string, limit int) ([]string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return nil, false, nil
	}
	rows, err := s.db.Query(`SELECT key FROM plugin_kv WHERE plugin_id = ? AND substr(key, 1, ?) = ? ORDER BY key LIMIT ?`,
		pluginID, len([]rune(prefix)), prefix, limit+1)
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

func (s *Store) PluginKVClearTemp(pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND temp = 1`, pluginID)
	return err
}

// .
// .
// .
type PluginReceipt struct {
	ReceiptID   string
	PluginID    string
	Operation   string
	Target      string
	Success     bool
	ReceiptJSON []byte
	CreatedAt   string
}

// .
// .
// .
// .
func (s *Store) AppendPluginReceipt(receiptID, pluginID, operation, target string, success bool, receiptJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := s.db.Exec(`INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		receiptID, pluginID, operation, target, successInt, string(receiptJSON),
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// .
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
