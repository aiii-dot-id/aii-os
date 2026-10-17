package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A REFUSAL IS CLASSIFIED BY ITS TYPE, NOT ITS WORDING (AGENTS §1.4; deep
// dive T47). Critical control flow must not depend on matching an error's
// prose: "does not resume" was produced in one package and matched in
// another, and a change of wording would have left a partial download
// stuck forever. That pair is a sentinel now. This walks every non-test Go
// file and refuses an error's text being matched: an err.Error() handed to
// a strings function, compared with == or !=, or switched on.
//
// The sites that remain read the text for display or for a log line, and
// decide nothing; each is named here with its reason, by file and
// function, and a name that no longer matches anything fails too.
//
// It is a tripwire, not a proof: text copied into a variable first
// (msg := err.Error()) and matched later passes it, which is exactly what
// recall's classify does — and why classify is named below rather than
// left unseen.
//
// guard:tree
func TestNoCodeDecidesByAnErrorsWording(t *testing.T) {
	allowed := map[string]string{
		"internal/app/schedule.go:reportScheduleFailure": "collapses the text's white space for one log line",
		"internal/app/boot.go:couldNotStart":             "puts a stopped step joined to a failed teardown on one line for the operator's reason",
		"internal/publicname/route.go:startRouteOwner":   "repeats a log line only when the text changed",
		"internal/app/speech_lists.go:listRefusal":       "trims the package prefix for display",
		"internal/app/continuation.go:causeLine":         "cuts the cause and its closing period for the operator's notice",
		"internal/ledger/ledger.go:verifyChain":          "trims the record prefix for display",
		"internal/memory/recall.go:classify":             "names an SQLite failure; every reader treats both outcomes alike (T47: no decision rides on it)",
	}
	root := repoRoot(t)
	seen := map[string]bool{}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && rel != ".") {
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
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			where := filepath.ToSlash(rel) + ":" + fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				var hit ast.Node
				switch x := n.(type) {
				case *ast.CallExpr:
					if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "strings" && anyErrorText(x.Args) {
							hit = x
						}
					}
				case *ast.BinaryExpr:
					if (x.Op == token.EQL || x.Op == token.NEQ) && (isErrorText(x.X) || isErrorText(x.Y)) {
						hit = x
					}
				case *ast.SwitchStmt:
					if x.Tag != nil && isErrorText(x.Tag) {
						hit = x
					}
				}
				if hit == nil {
					return true
				}
				seen[where] = true
				if _, ok := allowed[where]; !ok {
					found = append(found, fset.Position(hit.Pos()).String()+" ("+where+")")
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("%s matches an error's text — give the refusal a sentinel or a code and test it with errors.Is/As", f)
	}
	for where, why := range allowed {
		if !seen[where] {
			t.Errorf("%s (%s) no longer reads an error's text — delete its line", where, why)
		}
	}
}

// isErrorText reports an x.Error() call: a selector named Error, called
// with no arguments.
func isErrorText(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Error"
}

// anyErrorText reports an argument that is, or contains, an x.Error() call.
func anyErrorText(args []ast.Expr) bool {
	for _, a := range args {
		hit := false
		ast.Inspect(a, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok && isErrorText(e) {
				hit = true
			}
			return !hit
		})
		if hit {
			return true
		}
	}
	return false
}
