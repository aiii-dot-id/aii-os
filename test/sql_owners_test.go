package test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// sqlOwners are the only packages whose non-test code imports
// database/sql, each with its reason. Anything else reaches the database
// through a store method, under the store's lock and its freeze (deep
// dive T40).
var sqlOwners = map[string]string{
	"internal/store":  "owns the database",
	"internal/memory": "its instruments compose their reads from descriptors and run them through the store's Reader",
	"test/acceptance": "the acceptance driver (-tags acceptance) observes a slot's database through its read-only mount",
}

// guard:tree
func TestOnlyTheStoresOwnersImportDatabaseSQL(t *testing.T) {
	root := repoRoot(t)
	importers := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "database/sql" {
				rel, _ := filepath.Rel(root, filepath.Dir(path))
				importers[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var stray []string
	for pkg := range importers {
		if _, ok := sqlOwners[pkg]; !ok {
			stray = append(stray, pkg)
		}
	}
	sort.Strings(stray)
	for _, pkg := range stray {
		t.Errorf("%s imports database/sql; reach the database through a store method", pkg)
	}
	for pkg, why := range sqlOwners {
		if !importers[pkg] {
			t.Errorf("%s (%s) no longer imports database/sql; delete it from sqlOwners", pkg, why)
		}
	}
}
