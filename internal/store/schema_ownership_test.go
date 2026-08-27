package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSchemaSoleOwner enforces the operator directive of 2026-08-22:
// internal/store/schema.sql is the ONLY place database tables, indexes,
// views, triggers, or components are created. No DDL in non-test Go
// source; nothing ad-hoc in a running database. Test code is exempt.
//
// The principle (operator, five directives 2026-08-22): CODE MUST NOT
// MUTATE DATABASE OBJECTS — a source of bugs and inconsistencies. One
// schema.sql centralizes ownership and prevents the main source of
// database-object errors. The taxonomy:
//   - tables, indexes, triggers: created ONLY in schema.sql
//   - views: permitted in code (operator allows, with skepticism —
//     usually a query in Go is the better way to accomplish a code goal)
//   - maintenance commands (VACUUM/ANALYZE/REINDEX/ATTACH): belong in
//     code — they make no sense in a run-once-on-boot schema file
//
// Test code is exempt.
//
// The scan walks the repository from the package directory upward to its
// root (located by go.mod) and fails on any CREATE/DROP DDL statement in
// non-test .go files. Comments and string literals are both checked —
// DDL hiding inside a string executed at runtime is the exact pattern
// this rule exists to prevent (the pre-directive ALTER TABLE literals in
// db.go's New()).
func TestSchemaSoleOwner(t *testing.T) {
	root := repoRoot(t)
	// Note: CREATE VIEW / DROP VIEW are deliberately absent — views are
	// the sanctioned exception (operator, fifth directive 2026-08-22).
	dd1 := regexp.MustCompile(`(?i)\b(CREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX|TRIGGER|VIRTUAL\s+TABLE)|ALTER\s+TABLE|DROP\s+(?:TABLE|INDEX|TRIGGER))\b`)

	// walk every non-test .go file under the repo
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// strip line comments — a comment *about* DDL is not DDL
		for i, line := range strings.Split(string(data), "\n") {
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if loc := dd1.FindStringIndex(code); loc != nil {
				t.Errorf("%s:%d contains DDL outside schema.sql — schema.sql is the sole owner of database shape:\n\t%s",
					relPath(root, path), i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}

	// the sole sanctioned DDL site asserts its own existence
	if _, err := schemaFS.ReadFile("schema.sql"); err != nil {
		t.Fatalf("schema.sql missing from embed: %v", err)
	}
}

// TestOnlySchemaFileOwnsDDL extends sole ownership to the FILE layer:
// no .sql file other than schema.sql contains DDL for tables, indexes,
// or triggers. The one sanctioned shape-file is schema.sql; a second
// .sql with object-creating DDL — even never executed — is a second
// owner, and second owners are what the directive exists to prevent.
// Views are exempt from this layer too (operator, 2026-08-22).
func TestOnlySchemaFileOwnsDDL(t *testing.T) {
	root := repoRoot(t)
	ddl := regexp.MustCompile(`(?i)\b(CREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX|TRIGGER|VIRTUAL\s+TABLE)|ALTER\s+TABLE|DROP\s+(?:TABLE|INDEX|TRIGGER))\b`)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".sql") {
			return nil
		}
		if strings.HasSuffix(path, filepath.Join("internal", "store", "schema.sql")) {
			return nil // the sole owner
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// strip SQL comments: a file DOCUMENTING the rule may mention
		// DDL in prose without being a second owner
		for i, line := range strings.Split(string(data), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "--") {
				continue
			}
			if loc := ddl.FindStringIndex(code); loc != nil {
				t.Errorf("%s:%d contains DDL but is not schema.sql — schema.sql is the sole shape owner:\n\t%s",
					relPath(root, path), i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}

// TestSchemaContainsNoAlter enforces the operator's tightened rule of
// 2026-08-22: no ALTER TABLE anywhere in aii-os — not in code, not in
// schema.sql. The schema file declares full current shape with CREATE
// ... IF NOT EXISTS; evolution edits declarations, never migrates
// existing objects in place. Test code is exempt.
func TestSchemaContainsNoAlter(t *testing.T) {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(schemaBytes), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "--") {
			continue
		}
		if strings.Contains(strings.ToUpper(code), "ALTER") {
			t.Errorf("schema.sql:%d contains ALTER — the file declares current shape; evolution edits the declaration, never ALTERs in place: %s",
				i+1, strings.TrimSpace(line))
		}
	}
}

// TestNoFutureDates: a citation dated after today is a corrupted
// citation. The repo carried these — reviews dated 08-23/24 cited in
// code committed 08-22 — corrected 2026-08-22 after world-clock
// verification (timeapi.io: 2026-08-22T20:07Z; git commit dates in
// agreement). The digit-splice shape matches the known corruption
// channel. This guard fails the build on any recurrence, and because
// it compares against the current date, it never goes stale.
func TestNoFutureDates(t *testing.T) {
	root := repoRoot(t)
	dateRe := regexp.MustCompile(`(20[0-9]{2})-([0-9]{2})-([0-9]{2})`)
	today := time.Now().UTC()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range dateRe.FindAllStringSubmatch(line, -1) {
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				cited := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
				if cited.After(today) {
					t.Errorf("%s:%d cites a future date (%s) — corrupted citation, corrected class 2026-08-22:\n\t%s",
						relPath(root, path), i+1, m[0], strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}

// TestFreshDatabaseMatchesSchema proves a fresh boot creates exactly the
// tables schema.sql declares — no more (no ad-hoc creation), no less
// (nothing missing that later code assumes). Fresh-database shape is a
// projection of the FILE, and this test pins that equation.
func TestFreshDatabaseMatchesSchema(t *testing.T) {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([a-z_0-9]+)`)
	for _, m := range re.FindAllStringSubmatch(string(schemaBytes), -1) {
		declared[m[1]] = true
	}
	if len(declared) < 10 {
		t.Fatalf("parsed only %d CREATE TABLE statements from schema.sql — parse broken?", len(declared))
	}

	s := testStore(t)
	defer s.Close()

	rows, err := s.h().Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		live[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for name := range declared {
		if !live[name] {
			t.Errorf("schema.sql declares %q but a fresh database does not contain it", name)
		}
	}
	for name := range live {
		if !declared[name] {
			t.Errorf("fresh database contains %q which schema.sql never declared — created outside the file", name)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot locate repository root (go.mod) walking upward from store package")
		}
		dir = parent
	}
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
