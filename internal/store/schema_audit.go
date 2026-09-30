package store

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var schemaDeclRe = regexp.MustCompile(`(?is)CREATE\s+((?:VIRTUAL\s+)?TABLE|(?:UNIQUE\s+)?INDEX|VIEW|TRIGGER)\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)`)

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

var tableHeadRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_0-9]+)\s*\(`)

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

var tableConstraint = map[string]bool{
	"primary": true, "foreign": true, "unique": true, "check": true, "constraint": true,
}

var leadingIdentRe = regexp.MustCompile("(?is)^\\s*[\"`\\[]?([a-z_][a-z_0-9]*)")

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

func declaredSchema() (map[string]bool, error) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded schema: %w", err)
	}
	d, err := referenceSchema(string(raw))
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for kind, objects := range d.Objects {
		for name := range objects {
			declared[kind+":"+name] = true
		}
	}
	return declared, nil
}

func (s *Store) auditSchema() error {
	declared, err := declaredSchema()
	if err != nil {
		return err
	}
	rows, err := s.h().Query(`SELECT type, name FROM sqlite_master WHERE name NOT GLOB 'sqlite_*'`)
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
		live[kind+":"+sqliteName(name)] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite_master iteration failed: %w", err)
	}

	var undeclared, problems []string
	for key := range live {

		if strings.HasPrefix(key, "view:") {
			continue
		}
		if !declared[key] {
			undeclared = append(undeclared, key)
		}
	}
	sort.Strings(undeclared)
	for key := range declared {
		if !live[key] {
			problems = append(problems, fmt.Sprintf("schema.sql declares %s but the database lacks it", key))
		}
	}
	if len(problems) > 0 {

		for _, key := range undeclared {
			problems = append(problems, undeclaredProblem(key))
		}
		return fmt.Errorf("%d schema-ownership problem(s): %s", len(problems), strings.Join(problems, "; "))
	}
	if len(undeclared) > 0 {
		return &UndeclaredObjectError{Objects: undeclared}
	}

	if shape := s.columnProblems(); len(shape) > 0 {
		return &ShapeError{Problems: shape}
	}
	return nil
}

type UndeclaredObjectError struct{ Objects []string }

func (e *UndeclaredObjectError) Error() string {
	problems := make([]string, len(e.Objects))
	for i, key := range e.Objects {
		problems[i] = undeclaredProblem(key)
	}
	return fmt.Sprintf("%d schema-ownership problem(s): %s", len(problems), strings.Join(problems, "; "))
}

func undeclaredProblem(key string) string {
	return fmt.Sprintf("live %s exists but schema.sql never declared it — created outside the sole owner", key)
}

type ShapeError struct{ Problems []string }

func (e *ShapeError) Error() string {
	return fmt.Sprintf("%d table(s) do not match schema.sql: %s",
		len(e.Problems), strings.Join(e.Problems, "; "))
}

func (s *Store) columnProblems() []string {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return []string{fmt.Sprintf("cannot read embedded schema: %v", err)}
	}

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
	problems = append(problems, s.textProblems(string(raw))...)
	sort.Strings(problems)
	return problems
}

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
