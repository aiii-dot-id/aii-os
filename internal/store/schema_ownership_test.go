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
var ddlAppliers = map[string]bool{
	"internal/store/reconcile.go":       true,
	"internal/store/reconcile_parse.go": true,
}

// .
func TestSchemaSoleOwner(t *testing.T) {
	root := repoRoot(t)
	// .
	// .
	dd1 := regexp.MustCompile(`(?i)\b(CREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX|TRIGGER|VIRTUAL\s+TABLE)|ALTER\s+TABLE|DROP\s+(?:TABLE|INDEX|TRIGGER))\b`)

	// .
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
		if ddlAppliers[relPath(root, path)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// .
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

	// .
	if _, err := schemaFS.ReadFile("schema.sql"); err != nil {
		t.Fatalf("schema.sql missing from embed: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
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
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// .
		// .
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

// .
// .
// .
// .
// .
// .
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
var futureDateOK = map[string]map[string]bool{}

// .
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
		// .
		// .
		// .
		// .
		// .
		isGo := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		isDoc := strings.HasSuffix(path, ".md")
		if !isGo && !isDoc {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := relPath(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range dateRe.FindAllStringSubmatch(line, -1) {
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				cited := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
				if !cited.After(today) {
					continue
				}
				if futureDateOK[rel][m[0]] {
					continue
				}
				t.Errorf("%s:%d cites a future date (%s) — corrupted citation, corrected class 2026-08-22:\n\t%s\n\t(if this is a deliberate DEADLINE rather than a citation, add it to futureDateOK)",
					rel, i+1, m[0], strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
}

// .
// .
// .
// .
func TestFreshDatabaseMatchesSchema(t *testing.T) {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	declared := map[string]bool{}
	for key := range parseDeclarations(string(schemaBytes)) {
		if strings.HasPrefix(key, "table:") {
			declared[strings.TrimPrefix(key, "table:")] = true
		}
	}
	if len(declared) < 10 {
		t.Fatalf("parsed only %d table declarations from schema.sql — parse broken?", len(declared))
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
		return filepath.ToSlash(path)
	}
	// .
	// .
	// .
	// .
	// .
	return filepath.ToSlash(rel)
}
