package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// guard:tree
func TestNoCodeOutsideTheStoreWritesItsTables(t *testing.T) {
	root := repoRoot(t)
	write := regexp.MustCompile(`\b(INSERT(?: OR [A-Z]+)? INTO|UPDATE|DELETE FROM|REPLACE INTO)\s+["` + "`" + `]?([a-z_]+)`)
	store := filepath.Join(root, "internal", "store")
	seen := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path == store || (path != root && (strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata")) {
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
		seen++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range write.FindAllStringSubmatch(text, -1) {
				if _, known := Catalog[m[2]]; known {
					t.Errorf("%s writes the store's table %s (%s): a write belongs behind a store method", relPath(root, fset.Position(lit.Pos()).String()), m[2], m[1])
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 100 {
		t.Fatalf("read %d Go files outside the store; the walk is not reading the tree", seen)
	}
}
