package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
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
func TestASourceGuardNeverCarriesItsQueryInTheInitializer(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "recall.go", nil, 0)
	if err != nil {
		t.Fatalf("parse recall.go: %v", err)
	}

	guards := 0
	ast.Inspect(file, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Cond == nil {
			return true
		}
		var cond strings.Builder
		ast.Inspect(ifs.Cond, func(c ast.Node) bool {
			if id, ok := c.(*ast.Ident); ok {
				cond.WriteString(id.Name + " ")
			}
			if lit, ok := c.(*ast.BasicLit); ok {
				cond.WriteString(lit.Value + " ")
			}
			return true
		})
		text := cond.String()
		// .
		if !strings.Contains(text, "wants") && !strings.Contains(text, "source") {
			return true
		}
		guards++
		if ifs.Init != nil {
			t.Errorf("%s: a source guard carries an initializer — the query runs before the guard is read, so the unselected source is still queried and its error is discarded:\n\tif <init>; %s",
				fset.Position(ifs.Pos()), strings.TrimSpace(text))
		}
		return true
	})

	// .
	// .
	// .
	if guards < 6 {
		t.Fatalf("expected at least 6 source guards (beliefs, syntheses, intentions, experiences, conversation, ledger), found %d — has the guard vocabulary changed?", guards)
	}
}
