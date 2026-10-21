package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func (s *Store) QuickCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query("PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("quick_check scan: %w", err)
		}
		if line != "ok" && len(problems) < 5 {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("quick_check rows: %w", err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("quick_check found damage: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (s *Store) ForeignKeyCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {

		var table, parent string
		var rowid any
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("foreign_key_check scan: %w", err)
		}
		if len(problems) < 5 {
			problems = append(problems, fmt.Sprintf("%s row %v has no parent in %s", table, rowid, parent))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("foreign_key_check rows: %w", err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("foreign_key_check found orphans: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (s *Store) Housekeep(ctx context.Context) (string, error) {
	return s.HousekeepOptimized(ctx, CompressionOptimization{}, 0)
}

type CompressionOptimization = compressvfs.Optimization

func (s *Store) HousekeepOptimized(ctx context.Context, optimization CompressionOptimization, replacedMemories time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return "read-only mount; housekeeping skipped", nil
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("pin connection: %w", err)
	}
	defer conn.Close()

	pruneNote := ""
	if n, perr := prunePluginReceipts(ctx, conn, time.Now()); perr != nil {
		pruneNote = fmt.Sprintf(", plugin receipts prune FAILED: %v", perr)
	} else if n > 0 {
		pruneNote = fmt.Sprintf(", plugin receipts pruned %d", n)
	}

	if replacedMemories <= 0 {
		replacedMemories = DefaultPluginMemoryReplacedRetention
	}

	if ferr := s.frozenErr(); ferr != nil {
		pruneNote += fmt.Sprintf(", replaced plugin memories not pruned: %v", ferr)
	} else if n, perr := prunePluginMemoriesReplaced(ctx, conn, time.Now(), replacedMemories, s.frozenErr); perr != nil {
		pruneNote += fmt.Sprintf(", replaced plugin memories pruned %d and then stopped: %v", n, perr)
	} else {
		if n > 0 {
			pruneNote += fmt.Sprintf(", replaced plugin memories pruned %d", n)
		}
		if kept, kerr := pluginMemoriesKeptPastWindow(ctx, conn, time.Now(), replacedMemories); kerr != nil {
			pruneNote += fmt.Sprintf(", replaced plugin memories kept past their window could not be counted: %v", kerr)
		} else if kept != "" {
			pruneNote += ", replaced plugin memories kept past their window, a running activation's corrections stand on them: " + kept
		}
	}

	if _, err := conn.ExecContext(ctx, "PRAGMA analysis_limit=400"); err != nil {
		return "", fmt.Errorf("analysis_limit: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return "", fmt.Errorf("optimize: %w", err)
	}

	var busy, logPages, checkpointed int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &logPages, &checkpointed); err != nil {

		return "", fmt.Errorf("wal checkpoint: %w", err)
	}

	sidecarNote := sidecarHousekeeping(conn)

	_, _ = conn.ExecContext(ctx, "PRAGMA shrink_memory")

	if busy != 0 {
		return "optimize ok, wal checkpoint busy (a reader held it)" + pruneNote + sidecarNote, nil
	}

	s.db.SetMaxIdleConns(0)
	defer s.db.SetMaxIdleConns(storeIdleConnections)
	var report compressvfs.MaintenanceResult
	var raw string
	compactionNote := ""
	if err := conn.QueryRowContext(ctx, "PRAGMA "+compressvfs.CompactPragma+"="+strconv.Itoa(optimization.Mode())).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY {
			compactionNote = ", compressed-container compaction busy (reader holds database)"
		} else {
			return "", fmt.Errorf("compressed-container compaction: %w", err)
		}
	} else if raw != "" {
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			return "", fmt.Errorf("read compression maintenance result: %w", err)
		}
		if report.ReclaimedBytes > 0 {
			compactionNote = fmt.Sprintf(", compressed-container reclaimed %d bytes", report.ReclaimedBytes)
		}
		if optimization.Mode() != 0 || report.DictionaryBytes > 0 {
			compactionNote += fmt.Sprintf(", compression dictionary %d bytes", report.DictionaryBytes)
		}
		if report.Warning != "" {
			compactionNote += ", " + report.Warning
		}
	}
	return fmt.Sprintf("optimize ok, wal checkpointed (%d pages)%s%s%s", checkpointed, pruneNote, sidecarNote, compactionNote), nil
}

const pluginReceiptRetention = 90 * 24 * time.Hour

func prunePluginReceipts(ctx context.Context, conn *sql.Conn, now time.Time) (int64, error) {
	res, err := conn.ExecContext(ctx, `DELETE FROM plugin_receipts WHERE julianday(created_at) < julianday(?)`,
		now.Add(-pluginReceiptRetention).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const DefaultPluginMemoryReplacedRetention = 90 * 24 * time.Hour

const (
	pluginMemoriesHeld = `WITH RECURSIVE held(id) AS (
		SELECT overrides FROM plugin_memories WHERE temp = 1 AND overrides IS NOT NULL
		UNION
		SELECT superseded_by FROM plugin_memories WHERE temp = 1 AND superseded_by IS NOT NULL
		UNION
		SELECT m.superseded_by FROM plugin_memories m JOIN held h ON m.id = h.id WHERE m.superseded_by IS NOT NULL) `
	pluginMemoriesPast = `temp = 0 AND superseded_by IS NOT NULL AND julianday(updated_at) < julianday(?)`
)

func prunePluginMemoriesReplaced(ctx context.Context, conn *sql.Conn, now time.Time, window time.Duration, frozen func() error) (int64, error) {

	const next = `SELECT id FROM plugin_memories WHERE ` + pluginMemoriesPast + ` AND id NOT IN (SELECT id FROM held)
		ORDER BY julianday(updated_at), id LIMIT ?`
	cutoff := now.Add(-window).UTC().Format(time.RFC3339Nano)
	var total int64
	for {
		if frozen != nil {
			if err := frozen(); err != nil {
				return total, err
			}
		}
		n, err := prunePluginMemoryBatch(ctx, conn, pluginMemoriesHeld+next, cutoff)
		total += n
		if err != nil || n < int64(pluginMemoryPruneBatch) {
			return total, err
		}
	}
}

var pluginMemoryPruneBatch = 500

func prunePluginMemoryBatch(ctx context.Context, conn *sql.Conn, next, cutoff string) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, next, cutoff, pluginMemoryPruneBatch)
	if err != nil {
		return 0, err
	}
	var ids []any
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, tx.Commit()
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	for _, table := range []string{"memory_vectors", "memory_access"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE store = 'plugin_memories' AND id IN (`+in+`)`, ids...); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM plugin_memories WHERE id IN (`+in+`)`, ids...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

func pluginMemoriesKeptPastWindow(ctx context.Context, conn *sql.Conn, now time.Time, window time.Duration) (string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT plugin_id, COUNT(*) FROM plugin_memories WHERE `+pluginMemoriesPast+`
		GROUP BY plugin_id ORDER BY plugin_id`, now.Add(-window).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return "", err
		}
		kept = append(kept, fmt.Sprintf("%s %d", id, n))
	}
	return strings.Join(kept, ", "), rows.Err()
}
