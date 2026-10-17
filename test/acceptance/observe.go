//go:build acceptance

package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/acceptance"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var usageTables = []string{"turn_metrics", "subagent_metrics", "facility_usage"}

// observer reads the slot's database through the store's read-only
// mount (mode=ro, query_only): the driver writes nothing there.
type observer struct {
	st *store.Store
}

// alarmIDs is every armed alarm, so an attempt can tell the ones it set
// from the ones that were already there.
func (o observer) alarmIDs() (map[string]bool, error) {
	rows, err := o.st.DB().Query(`SELECT alarm_id FROM alarms`)
	if err != nil {
		return nil, fmt.Errorf("read alarms: %w", err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// busy reports whether the database shows work outstanding: an open
// work session, a pending or claimed work-queue item, or — when before
// is given — an alarm armed since before was taken.
func (o observer) busy(before map[string]bool) (bool, error) {
	var open int
	if err := o.st.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE status = 'active'`).Scan(&open); err != nil {
		return false, fmt.Errorf("read work sessions: %w", err)
	}
	queued, err := o.st.PendingWorkCount()
	if err != nil {
		return false, fmt.Errorf("read work queue: %w", err)
	}
	if open > 0 || queued > 0 {
		return true, nil
	}
	if before == nil {
		return false, nil
	}
	now, err := o.alarmIDs()
	if err != nil {
		return false, err
	}
	for id := range now {
		if !before[id] {
			return true, nil
		}
	}
	return false, nil
}

// usage reads every usage table's rows inside [start, end], column by
// column as stored. Usage is complete only when every table exists, can
// be windowed, and holds no unknown (NULL) value — C6 makes a missing
// measurement NULL precisely so that it is never read as zero. Each
// shortfall is named in gaps.
func (o observer) usage(start, end int64) (map[string][]map[string]any, []string, error) {
	db := o.st.DB()
	out := map[string][]map[string]any{}
	var gaps []string
	for _, table := range usageTables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			return nil, nil, fmt.Errorf("read schema: %w", err)
		}
		if n == 0 {
			gaps = append(gaps, table+": table absent in this build — its usage is unknown, not zero")
			continue
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, acceptance.UsageTimeColumn).Scan(&n); err != nil {
			return nil, nil, fmt.Errorf("read %s columns: %w", table, err)
		}
		if n == 0 {
			gaps = append(gaps, table+": no "+acceptance.UsageTimeColumn+" column — its rows cannot be placed in the window")
			continue
		}
		// The table name is one of usageTables, never input.
		rows, err := db.Query(`SELECT * FROM `+table+` WHERE `+acceptance.UsageTimeColumn+` BETWEEN ? AND ? ORDER BY `+acceptance.UsageTimeColumn, start, end)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", table, err)
		}
		list, nulls, err := scanRows(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", table, err)
		}
		if nulls > 0 {
			gaps = append(gaps, fmt.Sprintf("%s: %d unknown (NULL) value(s) in the window", table, nulls))
		}
		out[table] = list
	}
	return out, gaps, nil
}

// scanRows keeps every column as the driver returned it, and counts the
// NULLs.
func scanRows(rows *sql.Rows) ([]map[string]any, int, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	list := []map[string]any{}
	nulls := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, 0, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if vals[i] == nil && c != "turn_id" {
				nulls++
			}
			row[c] = vals[i]
		}
		// A NULL turn_id on a facility's row says the row is not a turn's,
		// which is known. On any other row it is an unknown like the rest.
		if id, has := row["turn_id"]; has && id == nil && !strings.HasPrefix(fmt.Sprint(row["source"]), "facility:") {
			nulls++
		}
		list = append(list, row)
	}
	return list, nulls, rows.Err()
}
