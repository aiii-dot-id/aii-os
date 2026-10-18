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

const atomicfilePkg = "github.com/aiii-dot-id/aii-os/internal/atomicfile"

// publishing names the calls whose first result must not be dropped.
var publishing = map[string]bool{
	"Replace":           true,
	"ReplaceExecutable": true,
	"PublishNew":        true,
	"WriteReplace":      true,
	"WriteNew":          true,
}

// guard:tree
func TestNoCallerDiscardsPublished(t *testing.T) {
	fset := token.NewFileSet()
	files, sites := 0, 0

	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// ".." is the ROOT of this walk and its own name begins
			// with a dot, so a bare HasPrefix(".") guard skips
			// everything and this test passes having read nothing. It
			// did exactly that once; only a mutation caught it.
			if n := d.Name(); n != "." && n != ".." &&
				(strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		// The package that DEFINES the API is exempt: its own tests
		// call it for effect.
		if strings.Contains(filepath.ToSlash(path), "/internal/atomicfile/") {
			return nil
		}

		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // not buildable Go on its own; the compiler owns that complaint
		}
		files++

		// Whatever this file calls the package — an alias, which a text
		// search cannot follow, or a DOT import, which erases the
		// qualifier entirely.
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
				// A dot import puts the name straight into this file's
				// scope. Go rejects a package-level name that collides
				// with a dot-imported one, so an unqualified Replace
				// here is unambiguously the package's.
				return dotImported && publishing[fun.Name]
			}
			return false
		}
		report := func(n ast.Node, why string) {
			pos := fset.Position(n.Pos())
			t.Errorf("%s:%d %s\n\t`published` answers whether the target already holds the new content; a caller that cannot tell that from a failed publish cannot choose the right recovery.",
				filepath.Clean(pos.Filename), pos.Line, why)
		}

		// Every node that appears in call position. A publishing name
		// NOT in this set is being taken as a value.
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
				// Counted here and ONLY here, so the total is every
				// publishing call however it is consumed — including
				// `return atomicfile.Replace(...)`, which passes both
				// values through and can never discard anything. The
				// count is the vacuity guard; the checks below are the
				// invariant.
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
				// `var _, err = atomicfile.Replace(...)` is a
				// declaration, not an assignment, and reaches none of
				// the cases above.
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
				// The dot-imported half of the value check.
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

	// A walk that reads nothing must not be able to pass. That was this
	// test's own first failure mode.
	if files == 0 {
		t.Fatal("parsed no Go files at all — this test cannot have proven anything")
	}
	if sites == 0 {
		t.Fatal("found no atomicfile publishing call sites — the API moved, or the walk is looking in the wrong place")
	}
	t.Logf("checked %d publishing call site(s) across %d parsed Go files; all direct, none taken as a value", sites, files)
}
