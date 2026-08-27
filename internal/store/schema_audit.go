package store

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The runtime half of the operator's schema-ownership principle
// (2026-08-22): code must not mutate database objects. schema.sql is
// the sole owner of database shape. schema_ownership_test.go enforces the
// compile-time half (no DDL in Go source); auditSchema enforces the
// running-database half — nothing exists in sqlite_master that schema.sql
// does not declare, and everything the file declares does exist.
//
// Views are the sanctioned exception (operator, 2026-08-22): a view
// may be created by code. The audit does not compare live views
// against the file — a code-created view must not fail boot. (The
// operator holds these with skepticism: usually a query in Go is the
// better way to accomplish a code goal.)
//
// Runs at every writable boot, after initSchema. A rogue object (created
// ad-hoc by any means — a debug session, a bug, a hand-typed statement)
// fails the boot loudly instead of persisting silently. The read-only
// SAFE mount deliberately skips it: that mount exists to display state
// when integrity is already in question, and must never add its own
// failure modes.
// schemaDeclRe extracts every component declaration from schema.sql.
// Kinds collapse to sqlite_master's vocabulary (table/index/view/trigger)
// so the declared set and the live set compare directly. UNIQUE INDEX
// normalizes to plain index: sqlite_master reports no "unique" kind, and
// the first version of this parser collapsed it to a "unique:" key that
// could never match a live row — every writable boot would have failed
// the moment the file declared its first unique index (found in review,
// 2026-08-22, by appending one and watching boots fail both directions).
var schemaDeclRe = regexp.MustCompile(`(?is)CREATE\s+(TABLE|(?:UNIQUE\s+)?INDEX|VIEW|TRIGGER)\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)`)

// parseDeclarations returns the "kind:name" component set a schema text
// declares. SQL comments run to end-of-line and are cut wherever they
// start — a trailing "-- ... CREATE INDEX ..." prose mention is
// documentation, not declaration (the first version stripped only
// full-line comments, so trailing prose could phantom-declare).
func parseDeclarations(sqlText string) map[string]bool {
	declared := map[string]bool{}
	for _, line := range strings.Split(sqlText, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		for _, m := range schemaDeclRe.FindAllStringSubmatch(line, -1) {
			kind := strings.ToLower(strings.Fields(m[1])[0])
			if kind == "unique" {
				kind = "index" // sqlite_master reports unique indexes as plain indexes
			}
			declared[kind+":"+m[2]] = true
		}
	}
	return declared
}

// declaredColumns returns, per declared table, the set of column NAMES
// its body names.
//
// Names, not full definitions: sqlite_master stores the CREATE text
// verbatim (comments and whitespace included, only IF NOT EXISTS
// stripped), so comparing text would fail every existing database the
// moment a comment inside a table body was edited — hostile to a file
// that documents itself as heavily as this one. A column that is not
// there is the failure that actually bites.
//
// This closes a hole the name-only audit had from the start: it caught a
// table added and a table removed, and MISSED a table whose shape
// changed under a name it already knew. Reusing a name with new columns
// passed the audit and then failed at the first query — proven against a
// live database before this was written.
// tableBodies returns each declared table's body text, comments removed.
func tableBodies(sqlText string) map[string]string {
	var clean strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		clean.WriteString(line)
		clean.WriteString("\n")
	}
	text := clean.String()
	out := map[string]string{}
	for _, loc := range tableHeadRe.FindAllStringSubmatchIndex(text, -1) {
		name := strings.ToLower(text[loc[2]:loc[3]])
		if body, ok := balancedBody(text[loc[1]-1:]); ok {
			out[name] = body
		}
	}
	return out
}

func declaredColumns(sqlText string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for name, body := range tableBodies(sqlText) {
		out[name] = columnNames(body)
	}
	return out
}

// tableHeadRe matches through the opening parenthesis of a table body.
var tableHeadRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s*\(`)

// balancedBody returns what is inside the parentheses that s starts with.
func balancedBody(s string) (string, bool) {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[1:i], true
			}
		}
	}
	return "", false
}

// tableConstraint names the body items that are not columns.
var tableConstraint = map[string]bool{
	"primary": true, "foreign": true, "unique": true, "check": true, "constraint": true,
}

// leadingIdentRe takes the first identifier of a table-body item.
var leadingIdentRe = regexp.MustCompile("(?is)^\\s*[\"`\\[]?([a-z_][a-z_0-9]*)")

// columnNames splits a table body on its TOP-LEVEL commas — a nested
// CHECK (x IN ('a','b')) must not be read as three columns — and takes
// the first identifier of each item that is not a table constraint.
func columnNames(body string) map[string]bool {
	cols := map[string]bool{}
	depth, start := 0, 0
	items := []string{}
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				items = append(items, body[start:i])
				start = i + 1
			}
		}
	}
	items = append(items, body[start:])
	for _, item := range items {
		// The leading identifier, however it is written: quoted, bracketed,
		// or run straight into an opening paren — "UNIQUE(a, b)" is a
		// constraint even with no space, and reading it as a column named
		// "unique(a" is how the first version of this reported a phantom.
		m := leadingIdentRe.FindStringSubmatch(item)
		if m == nil {
			continue
		}
		name := strings.ToLower(m[1])
		if tableConstraint[name] {
			continue
		}
		cols[name] = true
	}
	return cols
}

// declaredSchema returns the set of "kind:name" components schema.sql
// declares.
func declaredSchema() (map[string]bool, error) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded schema: %w", err)
	}
	declared := parseDeclarations(string(raw))
	if len(declared) < 10 {
		return nil, fmt.Errorf("parsed only %d declarations from schema.sql — parse broken?", len(declared))
	}
	return declared, nil
}

// auditSchema holds the live database to schema.sql's declaration, both
// directions: an undeclared live object was created outside the file
// (ad-hoc creation — the violation the directive names), and a declared
// object absent live means initSchema did not do its work (which cannot
// happen while initSchema executes the whole file, and that is exactly
// why it is worth proving).
func (s *Store) auditSchema() error {
	declared, err := declaredSchema()
	if err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT type, name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("cannot read sqlite_master: %w", err)
	}
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return fmt.Errorf("cannot scan sqlite_master row: %w", err)
		}
		live[kind+":"+name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite_master iteration failed: %w", err)
	}

	var problems []string
	for key := range live {
		// Views are the sanctioned exception (operator, 2026-08-22):
		// code may create views; a live view the file never declared
		// must not fail boot. (Held with skepticism — usually a query
		// in Go is the better way to accomplish a code goal.)
		if strings.HasPrefix(key, "view:") {
			continue
		}
		if !declared[key] {
			problems = append(problems, fmt.Sprintf("live %s exists but schema.sql never declared it — created outside the sole owner", key))
		}
	}
	for key := range declared {
		if !live[key] {
			problems = append(problems, fmt.Sprintf("schema.sql declares %s but the database lacks it", key))
		}
	}
	if len(problems) == 0 {
		// Only when the object set itself is sound: a table that is not
		// there at all has nothing to compare columns against, and saying
		// so twice helps nobody.
		if shape := s.columnProblems(); len(shape) > 0 {
			return &ShapeError{Problems: shape}
		}
	}
	if len(problems) > 0 {
		// Constructors wrap with their own context ("schema audit
		// failed: ..."); self-describing here doubled the phrase.
		return fmt.Errorf("%d schema-ownership problem(s): %s", len(problems), strings.Join(problems, "; "))
	}
	return nil
}

// ShapeError reports live tables whose columns disagree with schema.sql.
//
// SEPARATE FROM AN UNDECLARED OBJECT, and deliberately so. A table that
// schema.sql never declared can only have come from code doing DDL — a
// discipline violation, deterministic, and fixed by fixing the code; it
// fails the boot (operator ruling 2026-08-22). Columns that disagree is
// the signature of a DAMAGED PROJECTION, and the projection is a mirror
// the ledger can rebuild. Damage to a rebuildable mirror is exactly what
// SAFE exists for: come up read-only, say so, let the operator act. A
// dead process shows them nothing.
type ShapeError struct{ Problems []string }

func (e *ShapeError) Error() string {
	return fmt.Sprintf("%d table(s) do not match schema.sql: %s",
		len(e.Problems), strings.Join(e.Problems, "; "))
}

// checkClauses returns a table body's CHECK(...) constraints, normalized.
//
// Columns were only half the question. A CHECK is not a column, so a
// live table whose constraint no longer matches the file passed the
// audit and then refused inserts at runtime — which is exactly the shape
// of failure this audit exists to catch, one level in. Adding a role to
// conversations surfaced it: SQLite cannot ALTER a CHECK, so the table
// must be rebuilt, and nothing would have noticed a database that was
// not.
//
// Comments cannot appear inside a CHECK's parentheses, so unlike the
// full CREATE text this is safe to compare literally — a schema that
// documents itself as heavily as this one can be re-worded freely
// without tripping the audit.
func checkClauses(body string) []string {
	var out []string
	lower := strings.ToLower(body)
	for i := 0; ; {
		j := strings.Index(lower[i:], "check")
		if j < 0 {
			return out
		}
		at := i + j
		i = at + 5
		// A bare identifier ending in "check" is not the keyword.
		if at > 0 && isIdentByte(body[at-1]) {
			continue
		}
		rest := strings.TrimLeft(body[i:], " \t\n")
		if !strings.HasPrefix(rest, "(") {
			continue
		}
		inner, ok := balancedBody(rest)
		if !ok {
			continue
		}
		out = append(out, strings.Join(strings.Fields(strings.ToLower(inner)), " "))
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// declaredChecks returns, per declared table, its CHECK constraints.
func declaredChecks(sqlText string) map[string][]string {
	out := map[string][]string{}
	for name, body := range tableBodies(sqlText) {
		if c := checkClauses(body); len(c) > 0 {
			out[name] = c
		}
	}
	return out
}

// columnProblems reports every live table whose columns disagree with the
// ones schema.sql declares for it.
//
// A table only reaches here if it exists under a name the file declares,
// so this is the second half of the same question: not just "is it
// there?" but "is it the one the code was written against?".
func (s *Store) columnProblems() []string {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return []string{fmt.Sprintf("cannot read embedded schema: %v", err)}
	}
	var problems []string
	for table, want := range declaredColumns(string(raw)) {
		rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			problems = append(problems, fmt.Sprintf("cannot read the shape of table %s: %v", table, err))
			continue
		}
		got := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, ctype string
			var notNull, pk int
			var dflt interface{}
			if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
				problems = append(problems, fmt.Sprintf("cannot scan the shape of table %s: %v", table, err))
				break
			}
			got[strings.ToLower(name)] = true
		}
		rows.Close()
		if len(got) == 0 {
			continue // absent entirely — already reported by name
		}
		var missing, extra []string
		for c := range want {
			if !got[c] {
				missing = append(missing, c)
			}
		}
		for c := range got {
			if !want[c] {
				extra = append(extra, c)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("live table %s lacks column(s) %s that schema.sql declares", table, strings.Join(missing, ", ")))
		}
		if len(extra) > 0 {
			problems = append(problems, fmt.Sprintf("live table %s has column(s) %s that schema.sql never declared", table, strings.Join(extra, ", ")))
		}
	}
	problems = append(problems, s.constraintProblems(string(raw))...)
	sort.Strings(problems)
	return problems
}

// constraintProblems reports live tables whose CHECK constraints no
// longer match the file. A stale CHECK is a table that will refuse a
// value schema.sql says is legal — invisible until the first insert.
func (s *Store) constraintProblems(schemaText string) []string {
	var problems []string
	for table, want := range declaredChecks(schemaText) {
		var liveSQL string
		err := s.db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&liveSQL)
		if err != nil {
			continue // absent entirely — already reported by name
		}
		got := checkClauses(liveSQL)
		gotSet := map[string]bool{}
		for _, c := range got {
			gotSet[c] = true
		}
		for _, c := range want {
			if !gotSet[c] {
				problems = append(problems, fmt.Sprintf(
					"live table %s is missing the CHECK schema.sql declares (%s) — it will refuse values the file says are legal", table, c))
			}
		}
	}
	return problems
}
