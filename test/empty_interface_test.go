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
func TestTheEmptyInterfaceIsSpelledAny(t *testing.T) {
	root := repoRoot(t)
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		lines, err := emptyInterfaceLines(path, nil)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, line := range lines {
			t.Errorf("%s:%d spells the empty interface interface{}; write any (gofmt -r 'interface{} -> any' -w %s)", filepath.ToSlash(rel), line, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The guard is shown what it is for, so it cannot rot into passing: each
// place a type can stand, spelled the old way, is found on its line; any,
// an interface that holds something, and the old spelling inside a comment
// or a string are not.
//
// guard:tree
func TestTheEmptyInterfaceGuardRefusesWhatItIsFor(t *testing.T) {
	const refused = `package p

var a interface{}

func f(x interface{}) (interface{}, error) { return x.(interface{}), nil }

type m map[string]interface{}

type s struct{ v []interface{} }

func g[T interface{}](t T) {}

var b interface {
}
`
	got, err := emptyInterfaceLines("refused.go", refused)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{3, 5, 5, 5, 7, 9, 11, 13}
	if len(got) != len(want) {
		t.Fatalf("the old spelling on lines %v was found on lines %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the old spelling on lines %v was found on lines %v", want, got)
		}
	}

	const admitted = `package p

// a comment may say interface{}
var a any

const text = "interface{}"

type r interface{ Read() }

type c interface{ ~int | ~string }

func g[T any](t T) map[string]any { return nil }
`
	got, err = emptyInterfaceLines("admitted.go", admitted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("nothing here is an empty interface type, yet lines %v were refused", got)
	}
}

// emptyInterfaceLines parses one Go file (src as go/parser takes it; nil
// reads the file) and returns the line of every interface type that holds
// no method and no embedded type, in source order.
func emptyInterfaceLines(filename string, src any) ([]int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		if it, ok := n.(*ast.InterfaceType); ok && (it.Methods == nil || len(it.Methods.List) == 0) {
			lines = append(lines, fset.Position(it.Pos()).Line)
		}
		return true
	})
	return lines, nil
}
