package store

import (
	"strings"
	"testing"
)

// The shadow is created from the declaration verbatim, so if this
// parse is wrong every rebuild is wrong. It runs against the REAL
// schema.sql rather than a fixture: a parser that only handles the
// shapes a test author imagined is the parser that breaks on the file
// it exists for.
func TestDeclaredTableSQLCoversTheRealSchema(t *testing.T) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	tables := declaredTableSQL(text)
	declared, err := schemaComponents(text)
	if err != nil {
		t.Fatal(err)
	}

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

	// Every parsed statement must be a complete, balanced CREATE TABLE,
	// or the shadow will not compile as SQL.
	for name, sql := range tables {

		body := strings.TrimSuffix(strings.TrimSuffix(sql, " STRICT"), " WITHOUT ROWID")
		if !strings.HasPrefix(sql, "CREATE TABLE "+name+" (") || !strings.HasSuffix(body, ")") {
			t.Fatalf("table %s produced malformed SQL: %.80s", name, sql)
		}
		if strings.Count(sql, "(") != strings.Count(sql, ")") {
			t.Fatalf("table %s produced unbalanced SQL", name)
		}
	}
}

func isSidecarObject(name string) bool {
	for sc := range Sidecars {
		if name == sc || strings.HasPrefix(name, sc+"_") {
			return true
		}
	}
	return false
}

// DROP TABLE takes a table's indexes with it. A rebuild that does not
// put them back loses them silently — invisible until something is slow
// or a UNIQUE stops holding.
func TestDeclaredIndexSQLAttributesIndexesToTheirTable(t *testing.T) {
	raw, _ := schemaFS.ReadFile("schema.sql")
	text := string(raw)
	byTable := declaredIndexSQL(text)
	declared, err := schemaComponents(text)
	if err != nil {
		t.Fatal(err)
	}

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

// The annotation must pair with the column whose line carries it, and
// must not fire on anything else.
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

// Today's schema declares no renames. If that changes the author should
// see this test, because a rename is the one change that moves data.
func TestRealSchemaDeclaresNoRenamesYet(t *testing.T) {
	raw, _ := schemaFS.ReadFile("schema.sql")
	if got := declaredRenames(string(raw)); len(got) != 0 {
		t.Logf("schema.sql now declares renames: %v", got)
	}
}
