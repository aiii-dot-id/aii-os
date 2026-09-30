package store

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

type schemaDescription struct {
	Objects map[string]map[string]string
	Owners  map[string]string
	Shapes  map[string]map[string]columnShape
	Tables  map[string]tableDescription
}

type tableDescription struct {
	Kind         string
	WithoutRowID bool
}

var embeddedSchema struct {
	sync.Once
	description *schemaDescription
	err         error
}

func referenceSchema(text string) (*schemaDescription, error) {
	if raw, err := schemaFS.ReadFile("schema.sql"); err == nil && text == string(raw) {
		embeddedSchema.Do(func() { embeddedSchema.description, embeddedSchema.err = buildReferenceSchema(text) })
		return embeddedSchema.description, embeddedSchema.err
	}
	return buildReferenceSchema(text)
}

func buildReferenceSchema(text string) (*schemaDescription, error) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(text); err != nil {
		return nil, fmt.Errorf("create reference schema: %w", err)
	}
	d := &schemaDescription{Objects: map[string]map[string]string{}, Owners: map[string]string{}, Shapes: map[string]map[string]columnShape{}}
	rows, err := db.Query(`SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, name, owner, statement string
		if err := rows.Scan(&kind, &name, &owner, &statement); err != nil {
			rows.Close()
			return nil, err
		}
		name, owner = sqliteName(name), sqliteName(owner)
		if d.Objects[kind] == nil {
			d.Objects[kind] = map[string]string{}
		}
		d.Objects[kind][name] = statement
		d.Owners[kind+":"+name] = owner
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	d.Tables, err = describeTables(db)
	if err != nil {
		return nil, err
	}
	for name := range d.Objects["table"] {
		d.Shapes[name], err = tableShape(db, name)
		if err != nil {
			return nil, fmt.Errorf("reference table %s: %w", name, err)
		}
	}

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, err
	}
	for name, table := range d.Tables {
		if table.Kind != "table" || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		stmts := []string{"EXPLAIN INSERT INTO " + quoteIdentifier(name) + " DEFAULT VALUES", "EXPLAIN DELETE FROM " + quoteIdentifier(name)}
		var assignments []string
		for _, col := range sortedKeys(d.Shapes[name]) {
			if d.Shapes[name][col].Hidden == 0 {
				q := quoteIdentifier(col)
				assignments = append(assignments, q+"="+q)
			}
		}
		if len(assignments) > 0 {
			stmts = append(stmts, "EXPLAIN UPDATE "+quoteIdentifier(name)+" SET "+strings.Join(assignments, ","))
		}
		for _, stmt := range stmts {
			probe, err := db.Query(stmt)
			if err != nil {
				return nil, fmt.Errorf("target table %s write program: %w", name, err)
			}
			if err := probe.Close(); err != nil {
				return nil, err
			}
		}
	}
	return d, nil
}

func describeTables(q dbi) (map[string]tableDescription, error) {
	rows, err := q.Query("PRAGMA main.table_list")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]tableDescription{}
	for rows.Next() {
		var schema, name, kind string
		var columns, wr, strict int
		if err := rows.Scan(&schema, &name, &kind, &columns, &wr, &strict); err != nil {
			return nil, err
		}
		out[sqliteName(name)] = tableDescription{Kind: kind, WithoutRowID: wr != 0}
	}
	return out, rows.Err()
}

func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func sqliteName(name string) string {
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func schemaObjects(text, kind string) map[string]string {
	d, err := referenceSchema(text)
	if err != nil {
		return nil
	}
	return d.Objects[kind]
}
