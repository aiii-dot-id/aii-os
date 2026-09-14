package store

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestDeclaredTableSQLCoversTheRealSchema(t *testing.T) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	tables := declaredTableSQL(text)
	declared := parseDeclarations(text)

	var missing []string
	for key := range declared {
		if strings.HasPrefix(key, "table:") {
			name := strings.TrimPrefix(key, "table:")
			if isSidecarObject(name) {
				continue
			}
			if _, ok := tables[name]; !ok {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("declaredTableSQL missed %d table(s) the audit knows about: %v", len(missing), missing)
	}
	if len(tables) < 10 {
		t.Fatalf("parsed only %d tables — parse broken?", len(tables))
	}

	// .
	// .
	for name, sql := range tables {
		// .
		// .
		body := strings.TrimSuffix(strings.TrimSuffix(sql, " STRICT"), " WITHOUT ROWID")
		if !strings.HasPrefix(sql, "CREATE TABLE "+name+" (") || !strings.HasSuffix(body, ")") {
			t.Fatalf("table %s produced malformed SQL: %.80s", name, sql)
		}
		if strings.Count(sql, "(") != strings.Count(sql, ")") {
			t.Fatalf("table %s produced unbalanced SQL", name)
		}
	}
}

// .
// .
// .
func isSidecarObject(name string) bool {
	for sc := range Sidecars {
		if name == sc || strings.HasPrefix(name, sc+"_") {
			return true
		}
	}
	return false
}

// .
// .
// .
func TestDeclaredIndexSQLAttributesIndexesToTheirTable(t *testing.T) {
	raw, _ := schemaFS.ReadFile("schema.sql")
	text := string(raw)
	byTable := declaredIndexSQL(text)
	declared := parseDeclarations(text)

	declaredIdx := 0
	for key := range declared {
		if strings.HasPrefix(key, "index:") {
			declaredIdx++
		}
	}
	parsed := 0
	for _, list := range byTable {
		parsed += len(list)
	}
	if parsed != declaredIdx {
		t.Fatalf("attributed %d indexes to tables but schema.sql declares %d", parsed, declaredIdx)
	}
	tables := declaredTableSQL(text)
	for table := range byTable {
		if _, ok := tables[table]; !ok {
			t.Fatalf("index attributed to table %q which schema.sql never declares", table)
		}
	}
}

// .
// .
func TestDeclaredRenamesReadsTheAnnotation(t *testing.T) {
	text := `
CREATE TABLE inbound (
    id TEXT PRIMARY KEY,
    reach_address TEXT NOT NULL,  -- renamed-from: address
    created_ms INTEGER,
    -- a comment mentioning renamed-from: nothing should not fire
    note TEXT
);`
	got := declaredRenames(text)
	if got["inbound"]["reach_address"] != "address" {
		t.Fatalf("expected reach_address renamed-from address, got %v", got["inbound"])
	}
	if len(got["inbound"]) != 1 {
		t.Fatalf("annotation fired on more than the column that carries it: %v", got["inbound"])
	}
}

// .
// .
func TestRealSchemaDeclaresNoRenamesYet(t *testing.T) {
	raw, _ := schemaFS.ReadFile("schema.sql")
	if got := declaredRenames(string(raw)); len(got) != 0 {
		t.Logf("schema.sql now declares renames: %v", got)
	}
}
