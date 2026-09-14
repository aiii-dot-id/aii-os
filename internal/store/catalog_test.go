package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .

var createTableRe = regexp.MustCompile(`(?m)^CREATE TABLE IF NOT EXISTS ([a-z_0-9]+) \(`)

func schemaText(t *testing.T) string {
	t.Helper()
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEveryTableDeclaresItsProvenance(t *testing.T) {
	text := schemaText(t)
	var declared []string
	for _, m := range createTableRe.FindAllStringSubmatch(text, -1) {
		declared = append(declared, m[1])
	}
	sort.Strings(declared)
	if len(declared) < 20 {
		t.Fatalf("parsed only %d tables from schema.sql", len(declared))
	}
	for _, name := range declared {
		if _, ok := Catalog[name]; !ok {
			t.Errorf("schema.sql declares %s without a provenance line", name)
		}
	}
	if len(Catalog) != len(declared) {
		t.Fatalf("catalog has %d tables, schema.sql declares %d", len(Catalog), len(declared))
	}
	d, e := DerivedTables(), EphemeralTables()
	if len(d) != 11 || len(e) != 23 {
		t.Fatalf("derived=%d ephemeral=%d; the counts changed — say so in the docs", len(d), len(e))
	}
	if d[len(d)-1] != "ledger" {
		t.Fatalf("the mirror must clear last, got %s", d[len(d)-1])
	}
	for _, name := range []string{"conversations", "work_sessions", "outbox", "alarms", "witness_identity", "identity_lifetime"} {
		if Catalog[name].Provenance != Ephemeral {
			t.Errorf("%s must be ephemeral — replay must never touch it", name)
		}
	}
	for name := range writers {
		if _, declared := Catalog[name]; !declared {
			t.Errorf("writers names %s, which schema.sql does not declare", name)
		}
	}
}

func TestProvenanceDeclarationsAreStrict(t *testing.T) {
	good := "CREATE TABLE IF NOT EXISTS ledger (\n    -- provenance: derived, clear-order 2\n    seq INTEGER PRIMARY KEY\n);\nCREATE TABLE IF NOT EXISTS beliefs (\n    -- provenance: derived, clear-order 1\n    id TEXT\n);\nCREATE TABLE IF NOT EXISTS conversations (\n    -- provenance: ephemeral\n    id TEXT\n);\n"
	if _, err := parseProvenance(good); err != nil {
		t.Fatalf("the good text must parse: %v", err)
	}
	cases := map[string]string{
		"missing":               strings.Replace(good, "    -- provenance: ephemeral\n", "", 1),
		"two lines":             strings.Replace(good, "    -- provenance: ephemeral\n", "    -- provenance: ephemeral\n    -- provenance: ephemeral\n", 1),
		"derived without order": strings.Replace(good, "derived, clear-order 1", "derived", 1),
		"ephemeral with order":  strings.Replace(good, "-- provenance: ephemeral", "-- provenance: ephemeral, clear-order 3", 1),
		"duplicate order":       strings.Replace(good, "clear-order 1", "clear-order 2", 1),
		"mirror not last":       strings.Replace(strings.Replace(good, "clear-order 2", "clear-order 9", 1), "clear-order 1", "clear-order 10", 1),
		"mirror ephemeral":      strings.Replace(good, "derived, clear-order 2", "ephemeral", 1),
		"unknown word":          strings.Replace(good, "-- provenance: ephemeral", "-- provenance: temporary", 1),
	}
	for name, text := range cases {
		if _, err := parseProvenance(text); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

// .
// .
func TestClearOrderRespectsReferences(t *testing.T) {
	text := schemaText(t)
	refRe := regexp.MustCompile(`REFERENCES\s+([a-z_0-9]+)\s*\(`)
	for _, m := range createBlockRe.FindAllStringSubmatch(text, -1) {
		child, body := m[1], m[2]
		ce, ok := Catalog[child]
		if !ok || ce.Provenance != Derived {
			continue
		}
		for _, r := range refRe.FindAllStringSubmatch(body, -1) {
			parent := r[1]
			pe, ok := Catalog[parent]
			if !ok || pe.Provenance != Derived || parent == child {
				continue
			}
			if ce.ClearOrder >= pe.ClearOrder {
				t.Errorf("%s (clear-order %d) references %s (clear-order %d) and would be cleared after it", child, ce.ClearOrder, parent, pe.ClearOrder)
			}
		}
	}
}

// .
// .
func TestDerivedTablesAreWrittenOnlyByTheMaterializer(t *testing.T) {
	writeRe := regexp.MustCompile(`(?i)\b(INSERT(?: OR [A-Z]+)? INTO|UPDATE|DELETE FROM|REPLACE INTO)\s+` + "`?" + `([a-z_]+)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range writeRe.FindAllStringSubmatch(string(src), -1) {
			table := strings.ToLower(m[2])
			entry, known := Catalog[table]
			if !known {
				continue
			}
			switch entry.Provenance {
			case Derived:
				if name == "materialize.go" {
					continue
				}
				if _, named := entry.AlsoWrittenBy[name]; !named {
					t.Errorf("%s writes derived table %s and writers does not name it", name, table)
				}
			case Ephemeral:
				if name == "materialize.go" {
					if _, named := entry.AlsoWrittenBy[name]; !named {
						t.Errorf("materialize.go writes ephemeral table %s and writers does not name it", table)
					}
				}
			}
		}
	}
	for table, files := range writers {
		for file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Errorf("writers names %s as a writer of %s but the file does not exist", file, table)
				continue
			}
			if !strings.Contains(strings.ToLower(string(src)), table) {
				t.Errorf("writers names %s as a writer of %s but the file never mentions it", file, table)
			}
		}
	}
}

// .

// .
// .
// .
// .
// .
func TestSidecarsAreCataloguedWithTheirBases(t *testing.T) {
	text := schemaText(t)
	virtual := strings.Count(strings.ToUpper(text), "CREATE VIRTUAL TABLE")
	if virtual == 0 || virtual != len(Sidecars) {
		t.Fatalf("schema.sql holds %d virtual tables, the catalog %d sidecars", virtual, len(Sidecars))
	}
	if len(Sidecars) != 18 {
		t.Fatalf("sidecars=%d; the count changed — say so in R88 and SCHEMA.md", len(Sidecars))
	}
	declared := parseDeclarations(text)
	for name, e := range Sidecars {
		if _, ok := Catalog[e.Base]; !ok {
			t.Errorf("sidecar %s: base %s is not catalogued", name, e.Base)
		}
		if !strings.HasPrefix(name, e.Base+"_") {
			t.Errorf("sidecar %s does not carry its base's name (%s)", name, e.Base)
		}
		var twin string
		switch {
		case strings.HasSuffix(name, "_fts"):
			twin = strings.TrimSuffix(name, "_fts") + "_tri"
		case strings.HasSuffix(name, "_tri"):
			twin = strings.TrimSuffix(name, "_tri") + "_fts"
		default:
			t.Errorf("sidecar %s is named outside the _fts/_tri pair", name)
		}
		if _, ok := Sidecars[twin]; twin != "" && !ok {
			t.Errorf("sidecar %s has no twin %s", name, twin)
		}
		for _, suffix := range []string{"_sidecar_ai", "_sidecar_ad", "_sidecar_au"} {
			if !declared["trigger:"+e.Base+suffix] {
				t.Errorf("base %s lacks the trigger %s%s", e.Base, e.Base, suffix)
			}
		}
		if e.Content != e.Base && !declared["view:"+e.Content] {
			t.Errorf("sidecar %s reads %s, which is not a declared view", name, e.Content)
		}
		if !declared["table:"+name] || !declared["table:"+name+"_docsize"] {
			t.Errorf("the audit does not know sidecar %s and its shadow tables", name)
		}
		if declared["table:"+name+"_content"] {
			t.Errorf("sidecar %s would keep its own copy of the text", name)
		}
	}
	if got := SidecarsOf("conversations"); strings.Join(got, " ") != "conversations_fts conversations_tri" {
		t.Errorf("SidecarsOf(conversations) = %v", got)
	}
	if got := SidecarNames(); len(got) != len(Sidecars) || !sort.StringsAreSorted(got) {
		t.Errorf("SidecarNames() = %v", got)
	}
}

func TestSidecarDeclarationsAreStrict(t *testing.T) {
	tables := "CREATE TABLE IF NOT EXISTS ledger (\n    -- provenance: derived, clear-order 2\n    seq INTEGER PRIMARY KEY\n);\nCREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL,\n    role TEXT NOT NULL\n);\n"
	view := "CREATE VIEW IF NOT EXISTS notes_searchable AS SELECT rowid AS rowid, id, body FROM notes WHERE role = 'kept';\n"
	good := tables + view +
		"-- provenance: sidecar of notes\n" +
		"CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(body, content='notes_searchable', content_rowid='rowid', tokenize='unicode61');\n" +
		"-- provenance: sidecar of notes\n" +
		"CREATE VIRTUAL TABLE IF NOT EXISTS notes_tri USING fts5(body, content='notes', content_rowid='rowid', tokenize='trigram');\n"
	cat, err := parseProvenance(good)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := parseSidecars(good, cat)
	if err != nil {
		t.Fatalf("the good text must parse: %v", err)
	}
	if sc["notes_fts"].Base != "notes" || sc["notes_fts"].Content != "notes_searchable" || sc["notes_tri"].Content != "notes" {
		t.Fatalf("parsed %+v", sc)
	}
	cases := map[string]string{
		"no provenance line":      strings.Replace(good, "-- provenance: sidecar of notes\nCREATE VIRTUAL TABLE IF NOT EXISTS notes_tri", "CREATE VIRTUAL TABLE IF NOT EXISTS notes_tri", 1),
		"base not catalogued":     strings.Replace(good, "sidecar of notes\nCREATE VIRTUAL TABLE IF NOT EXISTS notes_tri", "sidecar of memos\nCREATE VIRTUAL TABLE IF NOT EXISTS notes_tri", 1),
		"name without the base's": strings.Replace(good, "notes_tri", "tri_notes", 1),
		"copies the text":         strings.Replace(good, "content='notes', content_rowid='rowid', ", "", 1),
		"content is not a view":   strings.Replace(good, "content='notes_searchable'", "content='elsewhere'", 1),
		"view not over the base":  strings.Replace(good, "FROM notes WHERE", "FROM ledger WHERE", 1),
		"not fts5":                strings.Replace(good, "notes_tri USING fts5(", "notes_tri USING rtree(", 1),
		"declared twice":          good + "-- provenance: sidecar of notes\nCREATE VIRTUAL TABLE IF NOT EXISTS notes_tri USING fts5(body, content='notes', content_rowid='rowid', tokenize='trigram');\n",
	}
	for name, text := range cases {
		if _, err := parseSidecars(text, cat); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

// .
// .
// .
func TestTriggerAndVirtualDeclarationsAreReadWhole(t *testing.T) {
	text := schemaText(t)
	triggers := declaredTriggerSQL(text)
	for name, e := range Sidecars {
		got := triggers[e.Base]
		if len(got) != 3 {
			t.Errorf("base %s of %s: read %d triggers, want 3", e.Base, name, len(got))
			continue
		}
		for _, trg := range got {
			if !strings.HasPrefix(strings.ToUpper(trg), "CREATE TRIGGER") || !strings.HasSuffix(strings.TrimSpace(trg), "END;") {
				t.Errorf("trigger on %s was not read whole: %q", e.Base, trg)
			}
			if !strings.Contains(trg, name) && !strings.Contains(trg, e.Base+"_fts") {
				t.Errorf("trigger on %s does not mention its sidecars: %q", e.Base, trg)
			}
		}
	}
	virtual := declaredVirtualSQL(text)
	if len(virtual) != len(Sidecars) {
		t.Fatalf("read %d virtual declarations, want %d", len(virtual), len(Sidecars))
	}
	s := testStore(t)
	for name, decl := range virtual {
		var live string
		if err := s.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, name).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if normalizeDeclaration(live) != normalizeDeclaration(decl) {
			t.Errorf("%s: the file and the live object render differently:\n%s\n%s", name, normalizeDeclaration(decl), normalizeDeclaration(live))
		}
	}
	if n := normalizeDeclaration("CREATE VIRTUAL TABLE  IF NOT EXISTS x USING fts5(a)\n;"); n != "CREATE VIRTUAL TABLE x USING fts5(a)" {
		t.Errorf("normalizeDeclaration = %q", n)
	}
}
