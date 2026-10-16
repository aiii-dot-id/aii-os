package store

import (
	"fmt"
	"sort"
	"strings"
)

func declaredSchema() (map[string]bool, error) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded schema: %w", err)
	}
	return schemaComponents(string(raw))
}

func schemaComponents(text string) (map[string]bool, error) {
	d, err := referenceSchema(text)
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
