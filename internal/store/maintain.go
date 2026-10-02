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
		var rowid interface{}
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
	return s.HousekeepOptimized(ctx, CompressionOptimization{})
}

type CompressionOptimization = compressvfs.Optimization

func (s *Store) HousekeepOptimized(ctx context.Context, optimization CompressionOptimization) (string, error) {
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
