package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
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
// .
// .
// .
// .
// .
// .
type columnShape struct {
	Type    string
	NotNull bool
	Dflt    string
	PK      int
}

// .
// .
func (c columnShape) describe(other columnShape) string {
	var parts []string
	if c.Type != other.Type {
		parts = append(parts, fmt.Sprintf("type %s -> %s", other.Type, c.Type))
	}
	if c.NotNull != other.NotNull {
		parts = append(parts, fmt.Sprintf("not-null %t -> %t", other.NotNull, c.NotNull))
	}
	if c.Dflt != other.Dflt {
		parts = append(parts, fmt.Sprintf("default %q -> %q", other.Dflt, c.Dflt))
	}
	if c.PK != other.PK {
		parts = append(parts, fmt.Sprintf("primary-key %d -> %d", other.PK, c.PK))
	}
	return strings.Join(parts, ", ")
}

var embeddedReferenceShape struct {
	once  sync.Once
	shape map[string]map[string]columnShape
	err   error
}

// .
// .
// .
func referenceShape(schemaText string) (map[string]map[string]columnShape, error) {
	if raw, err := schemaFS.ReadFile("schema.sql"); err == nil && schemaText == string(raw) {
		embeddedReferenceShape.once.Do(func() {
			embeddedReferenceShape.shape, embeddedReferenceShape.err = buildReferenceShape(schemaText)
		})
		return embeddedReferenceShape.shape, embeddedReferenceShape.err
	}
	return buildReferenceShape(schemaText)
}

// .
// .
func buildReferenceShape(schemaText string) (map[string]map[string]columnShape, error) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, fmt.Errorf("open reference database: %w", err)
	}
	defer db.Close()
	// .
	// .
	// .
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaText); err != nil {
		return nil, fmt.Errorf("create reference schema: %w", err)
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("list reference tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]map[string]columnShape, len(tables))
	for _, t := range tables {
		shape, err := tableShape(db, t)
		if err != nil {
			return nil, fmt.Errorf("reference shape of %s: %w", t, err)
		}
		out[t] = shape
	}
	return out, nil
}

// .
// .
// .
func tableShape(q interface {
	Query(string, ...interface{}) (*sql.Rows, error)
}, table string) (map[string]columnShape, error) {
	rows, err := q.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]columnShape{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		d := ""
		if dflt != nil {
			d = fmt.Sprintf("%v", dflt)
		}
		out[strings.ToLower(name)] = columnShape{
			Type:    strings.ToUpper(strings.TrimSpace(ctype)),
			NotNull: notNull != 0,
			Dflt:    d,
			PK:      pk,
		}
	}
	return out, rows.Err()
}

// .
// .
func (s *Store) liveShape(table string) (map[string]columnShape, error) {
	return tableShape(s.db, table)
}

// .
// .
// .
func shapeDifferences(live, want map[string]columnShape) []string {
	var names []string
	seen := map[string]bool{}
	for c := range want {
		names = append(names, c)
		seen[c] = true
	}
	for c := range live {
		if !seen[c] {
			names = append(names, c)
		}
	}
	sort.Strings(names)

	var diffs []string
	for _, c := range names {
		w, declared := want[c]
		l, present := live[c]
		switch {
		case declared && !present:
			diffs = append(diffs, fmt.Sprintf("%s: declared but absent", c))
		case present && !declared:
			diffs = append(diffs, fmt.Sprintf("%s: present but not declared", c))
		case w != l:
			diffs = append(diffs, fmt.Sprintf("%s: %s", c, w.describe(l)))
		}
	}
	return diffs
}

// .
// .
func columnNameSet(shape map[string]columnShape) map[string]bool {
	out := make(map[string]bool, len(shape))
	for c := range shape {
		out[c] = true
	}
	return out
}
