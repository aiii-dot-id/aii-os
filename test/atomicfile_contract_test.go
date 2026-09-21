package test

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
// .
// .
// .

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const atomicfilePkg = "github.com/aiii-dot-id/aii-os/internal/atomicfile"

// .
var publishing = map[string]bool{
	"Replace":           true,
	"ReplaceExecutable": true,
	"PublishNew":        true,
}

// .
func TestNoCallerDiscardsPublished(t *testing.T) {
	fset := token.NewFileSet()
	files, sites := 0, 0

	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .
			// .
			// .
			// .
			if n := d.Name(); n != "." && n != ".." &&
				(strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		// .
		// .
		if strings.Contains(filepath.ToSlash(path), "/internal/atomicfile/") {
			return nil
		}

		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		files++

		// .
		// .
		// .
		local, dotImported := "", false
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) != atomicfilePkg {
				continue
			}
			local = "atomicfile"
			if imp.Name != nil {
				if imp.Name.Name == "." {
					dotImported = true
					continue
				}
				local = imp.Name.Name
			}
		}
		if !dotImported && (local == "" || local == "_") {
			return nil
		}

		isPublish := func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return false
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if !publishing[fun.Sel.Name] {
					return false
				}
				id, ok := fun.X.(*ast.Ident)
				return ok && id.Name == local
			case *ast.Ident:
				// .
				// .
				// .
				// .
				return dotImported && publishing[fun.Name]
			}
			return false
		}
		report := func(n ast.Node, why string) {
			pos := fset.Position(n.Pos())
			t.Errorf("%s:%d %s\n\t`published` answers whether the target already holds the new content; a caller that cannot tell that from a failed publish cannot choose the right recovery.",
				filepath.Clean(pos.Filename), pos.Line, why)
		}

		// .
		// .
		called := map[ast.Node]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				called[c.Fun] = true
			}
			return true
		})
		namesPublish := func(n ast.Node) bool {
			switch fun := n.(type) {
			case *ast.SelectorExpr:
				if !publishing[fun.Sel.Name] {
					return false
				}
				id, ok := fun.X.(*ast.Ident)
				return ok && id.Name == local
			case *ast.Ident:
				return dotImported && publishing[fun.Name]
			}
			return false
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.SelectorExpr:
				if namesPublish(s) && !called[ast.Node(s)] {
					report(s, "takes a publishing function as a VALUE; call it directly so the result cannot be dropped out of sight")
				}
			case *ast.CallExpr:
				// .
				// .
				// .
				// .
				// .
				// .
				if isPublish(s) {
					sites++
				}
			case *ast.AssignStmt:
				if len(s.Rhs) != 1 || !isPublish(s.Rhs[0]) {
					return true
				}
				if id, ok := s.Lhs[0].(*ast.Ident); ok && id.Name == "_" {
					report(s.Rhs[0], "discards `published`")
				}
			case *ast.ValueSpec:
				// .
				// .
				// .
				if len(s.Values) != 1 || !isPublish(s.Values[0]) || len(s.Names) == 0 {
					return true
				}
				if s.Names[0].Name == "_" {
					report(s.Values[0], "discards `published` in a var declaration")
				}
			case *ast.ExprStmt:
				if isPublish(s.X) {
					report(s.X, "calls a publishing function for effect, discarding BOTH results")
				}
			case *ast.GoStmt:
				if isPublish(s.Call) {
					report(s.Call, "publishes in a goroutine, discarding BOTH results — and nothing can wait for the answer")
				}
			case *ast.DeferStmt:
				if isPublish(s.Call) {
					report(s.Call, "publishes in a defer, discarding BOTH results")
				}
			case *ast.Ident:
				// .
				if dotImported && publishing[s.Name] && !called[ast.Node(s)] {
					report(s, "takes a publishing function as a VALUE; call it directly so the result cannot be dropped out of sight")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// .
	// .
	if files == 0 {
		t.Fatal("parsed no Go files at all — this test cannot have proven anything")
	}
	if sites == 0 {
		t.Fatal("found no atomicfile publishing call sites — the API moved, or the walk is looking in the wrong place")
	}
	t.Logf("checked %d publishing call site(s) across %d parsed Go files; all direct, none taken as a value", sites, files)
}
