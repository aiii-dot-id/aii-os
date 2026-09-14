package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
// .
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
// .
// .
// .
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
		// .
		// .
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
// .
// .
// .
// .
// .
// .
func (s *Store) Housekeep() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return "", fmt.Errorf("pin connection: %w", err)
	}
	defer conn.Close()

	// .
	// .
	// .
	// .
	// .
	if _, err := conn.ExecContext(context.Background(), "PRAGMA analysis_limit=400"); err != nil {
		return "", fmt.Errorf("analysis_limit: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), "PRAGMA optimize"); err != nil {
		return "", fmt.Errorf("optimize: %w", err)
	}

	var busy, logPages, checkpointed int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &logPages, &checkpointed); err != nil {
		// .
		// .
		return "optimize ok, wal checkpoint unavailable", nil
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
	// .
	// .
	pruneNote := ""
	if n, perr := pruneToolEventsOn(connExecer{conn}); perr != nil {
		pruneNote = fmt.Sprintf(", tool_events prune FAILED: %v", perr)
	} else if n > 0 {
		pruneNote = fmt.Sprintf(", tool_events pruned %d", n)
	}

	// .
	// .
	// .
	sidecarNote := sidecarHousekeeping(conn)

	_, _ = conn.ExecContext(context.Background(), "PRAGMA shrink_memory")

	if busy != 0 {
		return "optimize ok, wal checkpoint busy (a reader held it)" + pruneNote + sidecarNote, nil
	}
	return fmt.Sprintf("optimize ok, wal checkpointed (%d pages)%s%s", checkpointed, pruneNote, sidecarNote), nil
}

// .
type connExecer struct{ conn *sql.Conn }

func (c connExecer) Exec(query string, args ...any) (sql.Result, error) {
	return c.conn.ExecContext(context.Background(), query, args...)
}
