package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// guard:tree
func TestNoProductionCodeReachesTheStoresTestSeams(t *testing.T) {
	root := repoRoot(t)
	seams := map[string]bool{"DB": true, "QueryRowForTest": true, "BumpOutboxCreatedMs": true}
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == "test" || rel == filepath.Join("internal", "store") || d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && rel != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		packages := map[string]bool{} // the names imported packages go by: sql.DB is a type, not a seam
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := p[strings.LastIndex(p, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			packages[name] = true
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !seams[sel.Sel.Name] {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && packages[x.Name] {
				return true
			}
			t.Errorf("%s: %s reaches the store's test seam — production goes through the store's own methods", fset.Position(sel.Pos()), sel.Sel.Name)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 300 {
		t.Fatalf("checked %d files — the walk is not reading the tree", checked)
	}
}
