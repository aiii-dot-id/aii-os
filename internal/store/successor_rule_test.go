package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	predecessorVersion      = "0.1.23"
	predecessorSchemaDigest = "aa140ab565a810b58b5fd1aaa9c03ef598d250a0be738b91610e6e52001505fa"
	predecessorReadsMarks   = false
)

// schemaReading renders what the successor rule reads of a schema, in a
// fixed order: each object by kind and name, each table's kind, each
// column's shape and each table's provenance.
func schemaReading(text string) (string, error) {
	ref, err := referenceSchema(text)
	if err != nil {
		return "", err
	}
	prov, problems := parseProvenance(text)
	if len(problems) > 0 {
		return "", errors.New(strings.Join(problems, "; "))
	}
	var b strings.Builder
	for _, kind := range sortedKeys(ref.Objects) {
		for _, name := range sortedKeys(ref.Objects[kind]) {
			fmt.Fprintf(&b, "object %s %s\n", kind, name)
		}
	}
	for _, table := range sortedKeys(ref.Tables) {
		fmt.Fprintf(&b, "table %s %+v %+v\n", table, ref.Tables[table], prov[table])
		for _, col := range sortedKeys(ref.Shapes[table]) {
			fmt.Fprintf(&b, "column %s.%s %+v\n", table, col, ref.Shapes[table][col])
		}
	}
	return b.String(), nil
}

func TestEveryChangeToWhatIsNotDerivedIsOneThePredecessorCanUndo(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "predecessor_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	reading, err := schemaReading(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256([]byte(reading)); hex.EncodeToString(sum[:]) != predecessorSchemaDigest {
		t.Fatalf("testdata/predecessor_schema.sql does not read as %s's schema.sql as released (digest %x)", predecessorVersion, sum)
	}
	head, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range successorRuleProblems(string(raw), string(head), predecessorReadsMarks) {
		t.Error(problem)
	}
}

// successorRuleProblems is the rule, as one reading of two schemas.
func successorRuleProblems(predecessor, head string, readsMarks bool) []string {
	var out []string
	was, err := referenceSchema(predecessor)
	if err != nil {
		return []string{"the predecessor's schema: " + err.Error()}
	}
	now, err := referenceSchema(head)
	if err != nil {
		return []string{"this tree's schema: " + err.Error()}
	}
	wasProv, _ := parseProvenance(predecessor)
	statements := declaredTableSQL(head)
	for _, table := range sortedKeys(statements) {
		if now.Tables[table].Kind != "table" {
			continue
		}
		if _, known := was.Tables[table]; !known {
			_, marked := successorTable(statements[table])
			switch {
			case !marked:
				out = append(out, "table "+table+" is new and not marked -- successor-table: the predecessor refuses a database holding a table it does not declare")
			case !readsMarks:
				out = append(out, "table "+table+" is new, and "+predecessorVersion+" does not read marks: it refuses a database holding a table it does not declare")
			}
			continue
		}
		if wasProv[table].Provenance == Derived {
			continue
		}
		marks := successorColumns(statements[table])
		for _, col := range sortedKeys(now.Shapes[table]) {
			shape := now.Shapes[table][col]
			old, known := was.Shapes[table][col]
			switch {
			case known && old != shape:
				out = append(out, "column "+table+"."+col+" changed its shape ("+shape.describe(old)+"): the predecessor's rebuild may refuse its rows")
			case known:
			case shape.NotNull || shape.PK != 0:
				out = append(out, "column "+table+"."+col+" is new and not nullable, or a key")
			case marks[col] == "":
				out = append(out, "column "+table+"."+col+" is new and not marked -- successor-column")
			case !readsMarks:
				out = append(out, "column "+table+"."+col+" is new, and "+predecessorVersion+" does not read marks: it refuses the database once the column holds a value")
			}
		}
		for _, col := range sortedKeys(was.Shapes[table]) {
			if _, still := now.Shapes[table][col]; !still {
				out = append(out, "column "+table+"."+col+" of a table that is not derived is gone: the predecessor adds it back to rows written without it")
			}
		}
	}
	return out
}

// The rule's reading finds what it is for: each way of breaking it, in a
// planted pair of schemas, is named, and the conforming change is not.
func TestTheSuccessorRuleFindsWhatItIsFor(t *testing.T) {
	const was = "CREATE TABLE IF NOT EXISTS ledger (\n    -- provenance: derived, clear-order 2\n    seq INTEGER PRIMARY KEY\n);\n" +
		"CREATE TABLE IF NOT EXISTS facts (\n    -- provenance: derived, clear-order 1\n    id TEXT PRIMARY KEY\n);\n" +
		"CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL,\n    gone TEXT,\n    kind TEXT\n);\n"
	const now = "CREATE TABLE IF NOT EXISTS ledger (\n    -- provenance: derived, clear-order 2\n    seq INTEGER PRIMARY KEY\n);\n" +
		"CREATE TABLE IF NOT EXISTS facts (\n    -- provenance: derived, clear-order 1\n    id TEXT PRIMARY KEY,\n    anything TEXT NOT NULL\n);\n" +
		"CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL,\n    kind INTEGER,\n" +
		"    good TEXT, -- successor-column: 0.3.0\n    unmarked TEXT,\n    strict TEXT NOT NULL DEFAULT '' -- successor-column: 0.3.0\n);\n" +
		"CREATE TABLE IF NOT EXISTS added (\n    -- provenance: ephemeral\n    -- successor-table: 0.3.0\n    id TEXT PRIMARY KEY\n);\n" +
		"CREATE TABLE IF NOT EXISTS bare (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY\n);\n"
	got := strings.Join(successorRuleProblems(was, now, true), "\n")
	for _, want := range []string{
		"table bare is new and not marked",
		"column notes.kind changed its shape",
		"column notes.unmarked is new and not marked",
		"column notes.strict is new and not nullable",
		"column notes.gone of a table that is not derived is gone",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("not found: %q\n%s", want, got)
		}
	}
	for _, quiet := range []string{"table added", "facts", "notes.good"} {
		if strings.Contains(got, quiet) {
			t.Errorf("a conforming change was named (%q):\n%s", quiet, got)
		}
	}
	// Toward a predecessor that reads no marks, a marked table or column
	// still breaks it: the release adds neither.
	got = strings.Join(successorRuleProblems(was, now, false), "\n")
	for _, want := range []string{"table added is new, and", "column notes.good is new, and " + predecessorVersion + " does not read marks"} {
		if !strings.Contains(got, want) {
			t.Errorf("not found toward a predecessor that reads no marks: %q\n%s", want, got)
		}
	}
}
