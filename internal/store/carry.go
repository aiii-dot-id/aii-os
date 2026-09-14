package store

import (
	"database/sql"
	"fmt"
	"sort"
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
type CarryReport struct {
	Ephemeral map[string]int64
	Derived   map[string][2]int64
}

func (r CarryReport) String() string {
	var b strings.Builder
	names := make([]string, 0, len(r.Ephemeral))
	for n := range r.Ephemeral {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "carried %d ephemeral tables unchanged:", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%d", n, r.Ephemeral[n])
	}
	names = names[:0]
	for n := range r.Derived {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "\nrebuilt %d derived tables from the record:", len(names))
	for _, n := range names {
		fmt.Fprintf(&b, " %s=%d→%d", n, r.Derived[n][0], r.Derived[n][1])
	}
	return b.String()
}

// .
// .
// .
// .
func CarryAcross(dbPath, ledgerPath string) (CarryReport, error) {
	report := CarryReport{Ephemeral: map[string]int64{}, Derived: map[string][2]int64{}}
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return report, fmt.Errorf("cannot read embedded schema: %w", err)
	}

	// .
	// .
	// .
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(5000)")
	if err != nil {
		return report, fmt.Errorf("open %s: %w", dbPath, err)
	}
	countAll := func(db *sql.DB) (map[string]int64, error) {
		counts := map[string]int64{}
		for name := range Catalog {
			var n int64
			if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", name)).Scan(&n); err != nil {
				if strings.Contains(err.Error(), "no such table") {
					continue
				}
				return nil, fmt.Errorf("count %s: %w", name, err)
			}
			counts[name] = n
		}
		return counts, nil
	}
	before, err := countAll(raw)
	if err != nil {
		raw.Close()
		return report, err
	}
	// .
	if err := recreateMirror(raw, string(schemaBytes)); err != nil {
		raw.Close()
		return report, err
	}
	if err := raw.Close(); err != nil {
		return report, fmt.Errorf("close: %w", err)
	}

	// .
	// .
	st, err := New(dbPath)
	if err != nil {
		return report, fmt.Errorf("open the projection as the runtime does: %w", err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		return report, fmt.Errorf("replay the record: %w", err)
	}
	after, err := countAll(st.db)
	if err != nil {
		return report, err
	}
	var changed []string
	for name, e := range Catalog {
		switch e.Provenance {
		case Ephemeral:
			if before[name] != after[name] {
				changed = append(changed, fmt.Sprintf("%s %d→%d", name, before[name], after[name]))
			}
			report.Ephemeral[name] = after[name]
		case Derived:
			report.Derived[name] = [2]int64{before[name], after[name]}
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		return report, fmt.Errorf("AN EPHEMERAL TABLE CHANGED ROW COUNT ACROSS THE CARRY — this must never happen: %s", strings.Join(changed, ", "))
	}
	return report, nil
}
