package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type columnShape struct {
	Type    string
	NotNull bool
	Dflt    string
	PK      int
	Hidden  int
}

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
	if c.Hidden != other.Hidden {
		parts = append(parts, fmt.Sprintf("generated/hidden %d -> %d", other.Hidden, c.Hidden))
	}
	return strings.Join(parts, ", ")
}

func referenceShape(schemaText string) (map[string]map[string]columnShape, error) {
	d, err := referenceSchema(schemaText)
	if err != nil {
		return nil, err
	}
	return d.Shapes, nil
}

func tableShape(q interface {
	Query(string, ...interface{}) (*sql.Rows, error)
}, table string) (map[string]columnShape, error) {
	rows, err := q.Query("PRAGMA table_xinfo(" + quoteIdentifier(table) + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]columnShape{}
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk, &hidden); err != nil {
			return nil, err
		}
		d := ""
		if dflt != nil {
			d = fmt.Sprintf("%v", dflt)
		}
		out[sqliteName(name)] = columnShape{
			Type:    strings.ToUpper(strings.TrimSpace(ctype)),
			NotNull: notNull != 0,
			Dflt:    d,
			PK:      pk,
			Hidden:  hidden,
		}
	}
	return out, rows.Err()
}

func (s *Store) liveShape(table string) (map[string]columnShape, error) {
	return tableShape(s.h(), table)
}

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

func columnNameSet(shape map[string]columnShape) map[string]bool {
	out := make(map[string]bool, len(shape))
	for c := range shape {
		out[c] = true
	}
	return out
}
