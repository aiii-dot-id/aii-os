package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// guard:tree
func TestNoProductionCodeCallsAForTestHook(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == "test" || d.Name() == "testdata" || (rel != "." && strings.HasSuffix(d.Name(), "test")) || (strings.HasPrefix(d.Name(), ".") && rel != ".") {
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
		checked++
		for _, at := range forTestReaches(fset, f) {
			t.Errorf("%s — a ForTest hook is for tests; production asks the package's own methods", at)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 300 {
		t.Fatalf("checked %d files — the walk is not reading the tree", checked)
	}
}

// forTestReaches is every selector in f that names a ...ForTest.
func forTestReaches(fset *token.FileSet, f *ast.File) []string {
	var at []string
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && strings.HasSuffix(sel.Sel.Name, "ForTest") {
			at = append(at, fset.Position(sel.Pos()).String()+": "+sel.Sel.Name)
		}
		return true
	})
	return at
}

// The walk is shown a reach of each kind before it is trusted to find none.
func TestForTestReachesSeesEachKind(t *testing.T) {
	src := `package p
func f(r *R) {
	r.pn.ServeForTest(x)        // a method call through a field
	_ = r.CarryForTest          // a method value
	store.NewForTest(t, path)   // a package-qualified call
	SeedForTest(path)           // the package's own body: not a reach
	r.Serve(x)                  // an ordinary method
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	got := forTestReaches(fset, f)
	if len(got) != 3 {
		t.Fatalf("want the three reaches, got %d: %v", len(got), got)
	}
	for i, name := range []string{"ServeForTest", "CarryForTest", "NewForTest"} {
		if !strings.HasSuffix(got[i], ": "+name) {
			t.Errorf("reach %d: want %s, got %s", i, name, got[i])
		}
	}
}
