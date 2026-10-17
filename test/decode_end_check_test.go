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

// guard:tree
func TestADecodersMoreIsAskedOnlyInALoop(t *testing.T) {
	// internal/packagefmt is a finished trust boundary whose acceptance is
	// cross-implementation canon (the plan's section 9): what its
	// strictDecode takes is what the other implementations take, and it
	// changes with them or not at all.
	kept := map[string]bool{"internal/packagefmt/trust.go": false}
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		lines, err := moreOutsideALoop(path, nil)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if _, ok := kept[rel]; ok {
			kept[rel] = len(lines) > 0
			return nil
		}
		for _, line := range lines {
			t.Errorf("%s:%d asks More() outside a loop: as an end-of-input check a stray closer passes it; read the value with canonicaljson.DecodeStrict", rel, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for file, found := range kept {
		if !found {
			stale = append(stale, file)
		}
	}
	sort.Strings(stale)
	for _, file := range stale {
		t.Errorf("%s is kept by this guard and no longer asks More() outside a loop: take it off the list", file)
	}
}

// The guard is shown what it is for, so it cannot rot into passing: the
// check in each form it has been written in is found on its line, and a
// loop over a decoder's elements, a method of that name that takes
// something, a plain function, a comment and a string are not.
//
// guard:tree
func TestTheDecodeEndGuardRefusesWhatItIsFor(t *testing.T) {
	const refused = `package p

func a(dec *json.Decoder) error {
	if dec.More() {
		return errTrailing
	}
	return nil
}

func b(d *json.Decoder) bool { return !d.More() }

func c(d *json.Decoder) {
	more := d.More()
	for more {
		more = false
	}
}

func e(d *json.Decoder) {
	if err := d.Decode(&v); err != nil || d.More() {
		return
	}
}
`
	got, err := moreOutsideALoop("refused.go", refused)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{4, 10, 13, 20}
	if len(got) != len(want) {
		t.Fatalf("the check on lines %v was found on lines %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the check on lines %v was found on lines %v", want, got)
		}
	}

	const admitted = `package p

// a comment may say dec.More()
const text = "if dec.More() {"

func a(dec *json.Decoder) {
	for dec.More() {
		dec.Token()
	}
	for i := 0; dec.More() && i < 9; i++ {
		dec.Token()
	}
}

func b(p pager) bool { return p.More(1) }

func c() bool { return More() }
`
	got, err = moreOutsideALoop("admitted.go", admitted)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("nothing here asks More() outside a loop, yet lines %v were refused", got)
	}
}

// moreOutsideALoop parses one Go file (src as go/parser takes it; nil
// reads the file) and returns the line of every call to a method named
// More with no argument that is not within a for statement's condition,
// in source order.
func moreOutsideALoop(filename string, src any) ([]int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	asked := func(n ast.Node) *ast.CallExpr {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return nil
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "More" {
			return nil
		}
		return call
	}
	looped := map[*ast.CallExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if loop, ok := n.(*ast.ForStmt); ok && loop.Cond != nil {
			ast.Inspect(loop.Cond, func(n ast.Node) bool {
				if call := asked(n); call != nil {
					looped[call] = true
				}
				return true
			})
		}
		return true
	})
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		if call := asked(n); call != nil && !looped[call] {
			lines = append(lines, fset.Position(call.Pos()).Line)
		}
		return true
	})
	return lines, nil
}
