package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func (s *Store) upgradeInteractionHistory() error {
	counts := map[string]int64{}
	for _, name := range []string{"conversations", "outbox"} {
		var n int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&n); err != nil {
			return err
		}
		counts[name] = n
	}
	changed := map[string]bool{}
	var cursor int64
	first := true
	for {
		rows, err := s.h().Query(`SELECT id,turn_seq,role,created_at,kind,recorded_at FROM conversations WHERE (? OR turn_seq>?) AND source_id IS NULL AND (recorded_at IS NULL OR (kind='legacy' AND role IN ('operator','resident','participant'))) ORDER BY turn_seq LIMIT 128`, first, cursor)
		if err != nil {
			return err
		}
		type oldRow struct {
			id, role, stamp, kind string
			seq                   int64
			at                    sql.NullString
		}
		var batch []oldRow
		for rows.Next() {
			var r oldRow
			if err = rows.Scan(&r.id, &r.seq, &r.role, &r.stamp, &r.kind, &r.at); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			cursor = r.seq
			first = false
			kind := r.kind
			if kind == "legacy" && (r.role == "resident" || r.role == "operator" || r.role == "participant") {
				kind = "message"
			}
			at := r.at
			if !at.Valid {
				if parsed, e := time.Parse(time.RFC3339Nano, r.stamp); e == nil {
					if stamp, e := interaction.NormalizeTime(parsed); e == nil {
						at = sql.NullString{String: stamp, Valid: true}
					}
				}
			}
			if kind == r.kind && at == r.at {
				continue
			}
			if _, err = s.h().Exec(`UPDATE conversations SET kind=?,recorded_at=? WHERE id=?`, kind, at, r.id); err != nil {
				return err
			}
			var gotKind string
			var gotAt sql.NullString
			if err = s.h().QueryRow(`SELECT kind,recorded_at FROM conversations WHERE id=?`, r.id).Scan(&gotKind, &gotAt); err != nil {
				return err
			}
			if gotKind != kind || gotAt != at {
				return fmt.Errorf("interaction conversion readback differs: %s", r.id)
			}
			changed["conversations"] = true
		}
	}
	var afterID string
	first = true
	for {
		rows, err := s.h().Query(`SELECT id,created_ms FROM outbox WHERE to_role='operator' AND interaction_id IS NULL AND (? OR id>?) ORDER BY id LIMIT 128`, first, afterID)
		if err != nil {
			return err
		}
		type mail struct {
			id string
			ms int64
		}
		var batch []mail
		for rows.Next() {
			var r mail
			if err = rows.Scan(&r.id, &r.ms); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			first = false
			afterID = r.id
			id := fmt.Sprintf("ix_import_%x", sha256.Sum256([]byte("outbox\x00"+r.id)))
			var exists int
			if err = s.h().QueryRow(`SELECT COUNT(*) FROM conversations WHERE id=? OR (source_kind='outbox' AND source_id=?)`, id, r.id).Scan(&exists); err != nil {
				return err
			}
			if exists != 0 {
				return fmt.Errorf("interaction import source collision: %s", r.id)
			}
			stamp, _ := interaction.NormalizeTime(time.Now().UTC())
			var occurred any
			if r.ms > 0 {
				if text, e := interaction.NormalizeTime(time.UnixMilli(r.ms)); e == nil {
					occurred = text
				}
			}
			details, _ := json.Marshal(interaction.Details{LegacySource: "outbox", OrderBasis: "import"})
			if _, err = s.h().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,source_kind,source_id,recorded_at,occurred_at,details) VALUES(?,'default','system','',COALESCE((SELECT MAX(turn_seq) FROM conversations),0)+1,?,'legacy','outbox',?,?,?,?)`, id, stamp, r.id, stamp, occurred, string(details)); err != nil {
				return err
			}
			res, err := s.h().Exec(`UPDATE outbox SET interaction_id=? WHERE id=? AND interaction_id IS NULL`, id, r.id)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil || n != 1 {
				return fmt.Errorf("interaction import link refused: %s: %w", r.id, err)
			}
			var linked string
			if err = s.h().QueryRow(`SELECT interaction_id FROM outbox WHERE id=?`, r.id).Scan(&linked); err != nil {
				return err
			}
			if linked != id {
				return fmt.Errorf("interaction import link readback differs")
			}
			changed["conversations"] = true
			changed["outbox"] = true
		}
	}
	for name := range changed {
		var n int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&n); err != nil {
			return err
		}
		if s.schemaConversions == nil {
			s.schemaConversions = map[string]RuntimeConversion{}
		}
		before := counts[name]
		reason := "typed interaction classification, normalized time and explicit legacy delivery references"
		if previous, ok := s.schemaConversions[name]; ok {
			before = previous.Before
			reason = previous.Reason + "; " + reason
		}
		s.schemaConversions[name] = RuntimeConversion{Before: before, After: n, Reason: reason}
	}
	return nil
}
