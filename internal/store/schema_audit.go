package store

import (
	"fmt"
	"regexp"
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
var schemaDeclRe = regexp.MustCompile(`(?is)CREATE\s+((?:VIRTUAL\s+)?TABLE|(?:UNIQUE\s+)?INDEX|VIEW|TRIGGER)\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)`)

// .
// .
// .
// .
// .
func parseDeclarations(sqlText string) map[string]bool {
	declared := map[string]bool{}
	for _, line := range strings.Split(sqlText, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		for _, m := range schemaDeclRe.FindAllStringSubmatch(line, -1) {
			kind := strings.ToLower(strings.Fields(m[1])[0])
			switch kind {
			case "unique":
				kind = "index"
			case "virtual":
				// .
				// .
				// .
				// .
				// .
				kind = "table"
				for _, suffix := range fts5Shadows(line) {
					declared["table:"+m[2]+suffix] = true
				}
			}
			declared[kind+":"+m[2]] = true
		}
	}
	return declared
}

// .
// .
// .
// .
func fts5Shadows(decl string) []string {
	compact := strings.ReplaceAll(strings.ToLower(decl), " ", "")
	out := []string{"_data", "_idx", "_config"}
	if !strings.Contains(compact, "columnsize=0") {
		out = append(out, "_docsize")
	}
	if !fts5ContentRe.MatchString(decl) {
		out = append(out, "_content")
	}
	return out
}

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

// .
var tableHeadRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s*\(`)

// .
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

// .
var tableConstraint = map[string]bool{
	"primary": true, "foreign": true, "unique": true, "check": true, "constraint": true,
}

// .
var leadingIdentRe = regexp.MustCompile("(?is)^\\s*[\"`\\[]?([a-z_][a-z_0-9]*)")

// .
// .
// .
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
		// .
		// .
		// .
		// .
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

// .
// .
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

// .
// .
// .
// .
// .
// .
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
		// .
		// .
		// .
		// .
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
		// .
		// .
		// .
		if shape := s.columnProblems(); len(shape) > 0 {
			return &ShapeError{Problems: shape}
		}
	}
	if len(problems) > 0 {
		// .
		// .
		return fmt.Errorf("%d schema-ownership problem(s): %s", len(problems), strings.Join(problems, "; "))
	}
	return nil
}

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
type ShapeError struct{ Problems []string }

func (e *ShapeError) Error() string {
	return fmt.Sprintf("%d table(s) do not match schema.sql: %s",
		len(e.Problems), strings.Join(e.Problems, "; "))
}

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
		// .
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

// .
func declaredChecks(sqlText string) map[string][]string {
	out := map[string][]string{}
	for name, body := range tableBodies(sqlText) {
		if c := checkClauses(body); len(c) > 0 {
			out[name] = c
		}
	}
	return out
}

// .
// .
// .
// .
// .
// .
func (s *Store) columnProblems() []string {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return []string{fmt.Sprintf("cannot read embedded schema: %v", err)}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	want, err := referenceShape(string(raw))
	if err != nil {
		return []string{fmt.Sprintf("cannot build the reference shape: %v", err)}
	}
	var problems []string
	for table, wantCols := range want {
		liveCols, err := s.liveShape(table)
		if err != nil {
			problems = append(problems, fmt.Sprintf("cannot read the shape of table %s: %v", table, err))
			continue
		}
		if len(liveCols) == 0 {
			continue
		}
		for _, d := range shapeDifferences(liveCols, wantCols) {
			problems = append(problems, fmt.Sprintf("live table %s disagrees with schema.sql — %s", table, d))
		}
	}
	problems = append(problems, s.constraintProblems(string(raw))...)
	problems = append(problems, s.textProblems(string(raw))...)
	sort.Strings(problems)
	return problems
}

// .
// .
// .
func (s *Store) constraintProblems(schemaText string) []string {
	var problems []string
	for table, want := range declaredChecks(schemaText) {
		var liveSQL string
		err := s.db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&liveSQL)
		if err != nil {
			continue
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

// .
// .
// .
// .
// .
// .
// .
func (s *Store) textProblems(schemaText string) []string {
	var problems []string
	live := map[string]map[string]string{}
	for _, kind := range []string{"table", "index", "trigger", "view"} {
		objects, err := s.liveObjects(kind)
		if err != nil {
			return []string{fmt.Sprintf("cannot read the live %ss: %v", kind, err)}
		}
		live[kind] = objects
	}
	check := func(kind, name, declared string) {
		text, ok := live[kind][name]
		if !ok {
			return
		}
		if canonDDL(text) != canonDDL(declared) {
			problems = append(problems, fmt.Sprintf("live %s %s does not read as schema.sql declares it", kind, name))
		}
	}
	for name, sql := range declaredTableSQL(schemaText) {
		check("table", name, sql)
	}
	for name, d := range declaredIndexStatements(schemaText) {
		check("index", name, d.SQL)
	}
	for name, sql := range declaredTriggerStatements(schemaText) {
		check("trigger", name, sql)
	}
	for name, sql := range declaredViewSQL(schemaText) {
		check("view", name, sql)
	}
	for name, sql := range declaredVirtualSQL(schemaText) {
		check("table", name, sql)
	}
	sort.Strings(problems)
	return problems
}
