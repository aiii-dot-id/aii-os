package store

// The capability broker's two tables (build-order step 4,
// PLUGIN_FRAMEWORK.md §7-§8): the per-plugin RING4 kv namespace and the
// host-authored receipt log. Both are broker-only surfaces — the
// identity's verbs never write here, and a plugin never reaches this
// package (the broker's binding supplies plugin_id structurally; no
// plugin-supplied parameter ever names the namespace).

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPluginKVQuota is the kv namespace refusing growth at a ceiling.
// The broker maps it onto the wire vocabulary (KV_QUOTA_EXCEEDED); the
// store only reports the structural fact.
var ErrPluginKVQuota = errors.New("plugin kv quota exceeded")

// PluginKVPut upserts one key in the plugin's scoped namespace. temp
// marks the row for ClearTemp (the uncertified-tier scope: cleared at
// (de)activation); a re-put re-stamps the flag, so the CURRENT
// activation's scope always owns the row. maxKeys / maxTotalBytes are
// the namespace ceilings (broker config); both count key+value bytes so
// a thousand giant keys cannot ride a value-only budget.
func (s *Store) PluginKVPut(pluginID, key, value string, temp bool, maxKeys, maxTotalBytes int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Quota counts everything EXCEPT the row being replaced — replacing
	// a value in place must be judged by the size it leaves, not the
	// size it passes through. LENGTH(CAST(.. AS BLOB)) counts bytes;
	// LENGTH on TEXT counts characters.
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

// PluginKVGet reads one key from the plugin's namespace.
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

// PluginKVDelete removes one key; reports whether a row existed
// (idempotent — deleting the absent is not an error).
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

// PluginKVClearTemp drops the plugin's temp-scoped rows — the
// uncertified-tier RING4 discipline: T0 storage lives exactly one
// activation. Idempotent; called at deactivation AND at activation (a
// crashed process never reached deactivation, and stale temp rows
// surviving into the next activation would quietly promote T0 storage
// to persistence).
func (s *Store) PluginKVClearTemp(pluginID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM plugin_kv WHERE plugin_id = ? AND temp = 1`, pluginID)
	return err
}

// PluginReceipt is one host-authored broker receipt row — the proof
// plane (threat model §5: the receipt, not the plugin's return string,
// is what the identity may cite).
type PluginReceipt struct {
	ReceiptID   string
	PluginID    string
	Operation   string
	Target      string
	Success     bool
	ReceiptJSON []byte
	CreatedAt   string
}

// AppendPluginReceipt records one broker-authored receipt. Only the
// broker calls this, only for effects it evaluated itself — a plugin
// response never reaches here (the daemon-injects rule, BBB_V2_AUDIT
// §6.4).
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

// PluginReceipts lists a plugin's receipts, oldest first.
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
