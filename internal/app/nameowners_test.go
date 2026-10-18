package app

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestEveryCallableNameHasOneOwner(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	sets := staticNames(reg)

	// It is looking at the real sets: each owner's names, in its own place.
	for i, want := range []struct {
		owner, member string
		atLeast       int
	}{
		{"an offered verb", "note", 6},
		{"an absorbed operation", "timer", 6},
		{"a registry builtin", "read", 4},
		{"a category door", "memory", 3},
	} {
		s := sets[i]
		if !strings.HasPrefix(s.owner, want.owner) || !slices.Contains(s.names, want.member) || len(s.names) < want.atLeast {
			t.Fatalf("set %d is %q with %v: this check is not looking at %s", i, s.owner, s.names, want.owner)
		}
	}
	if err := checkNameOwners(reg); err != nil {
		t.Fatalf("the names this build holds do not have one owner each: %v", err)
	}

	refused := func(t *testing.T, err error, name, owner, other string) {
		t.Helper()
		var c *nameCollision
		if !errors.As(err, &c) {
			t.Fatalf("%s: want a *nameCollision, got %v", name, err)
		}
		got := []string{c.Owner, c.Other}
		if c.Name != name || !slices.ContainsFunc(got, func(o string) bool { return strings.HasPrefix(o, owner) }) ||
			!slices.ContainsFunc(got, func(o string) bool { return strings.HasPrefix(o, other) }) {
			t.Errorf("%s: refused as %+v, want %s held by %s and %s", name, c, name, owner, other)
		}
	}

	// A builtin that takes another owner's name, on a real registry — the
	// same call the boot makes.
	for _, c := range []struct{ name, other string }{
		{"note", "an offered verb"},
		{"timer", "an absorbed operation"},
		{"memory", "a category door"},
	} {
		r := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
		r.Register(declaredOp{name: c.name, op: c.name})
		refused(t, checkNameOwners(r), c.name, "a registry builtin", c.other)
	}

	// The static sets taking each other's names, and one owner declaring a
	// name twice: shown through a copy of the real sets.
	for _, c := range []struct {
		into        int
		name, other string
	}{
		{3, "work", "an offered verb"},
		{3, "measure", "an absorbed operation"},
		{1, "recall", "an offered verb"},
		{3, "voice", "a category door"},
	} {
		doctored := slices.Clone(sets)
		doctored[c.into].names = append(slices.Clone(sets[c.into].names), c.name)
		refused(t, oneOwnerEach(doctored), c.name, strings.SplitN(sets[c.into].owner, " (", 2)[0], c.other)
	}
}

// Both boots ask. The real sets are clean, so the check above cannot tell
// whether a boot runs it, and the offered verbs are identity's to declare:
// no test may rewrite them. So this reads the boots themselves. Every tool
// registry built in this package's production code is held to the check
// in the very next statement, and a collision ends that boot: today the
// one newToolRegistry the live and SAFE boots share, and any boot that
// builds one later.
func TestEveryBootChecksTheRegistryItBuilds(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	built := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			block, ok := n.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i, st := range block.List {
				reg := newRegistryAssigned(st)
				if reg == "" {
					continue
				}
				built++
				if i+1 >= len(block.List) || !checksAndReturns(block.List[i+1], reg) {
					t.Errorf("%s:%d builds a tool registry and does not hold it to checkNameOwners in the next statement, returning its error",
						name, fset.Position(st.Pos()).Line)
				}
			}
			return true
		})
	}
	if built < 1 {
		t.Fatal("found no tool registry built in production code — the live and SAFE boots build theirs in newToolRegistry; the walk is not reading them")
	}
}

// newRegistryAssigned names X in `X := tools.NewRegistry(...)`.
func newRegistryAssigned(st ast.Stmt) string {
	as, ok := st.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return ""
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewRegistry" {
		return ""
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "tools" {
		return ""
	}
	id, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return ""
	}
	return id.Name
}

// checksAndReturns is `if err := checkNameOwners(reg); err != nil { return ..., err }`.
func checksAndReturns(st ast.Stmt, reg string) bool {
	ifs, ok := st.(*ast.IfStmt)
	if !ok || ifs.Init == nil {
		return false
	}
	as, ok := ifs.Init.(*ast.AssignStmt)
	if !ok || len(as.Rhs) != 1 || len(as.Lhs) != 1 {
		return false
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	fn, ok := call.Fun.(*ast.Ident)
	arg, ok2 := call.Args[0].(*ast.Ident)
	if !ok || !ok2 || fn.Name != "checkNameOwners" || arg.Name != reg {
		return false
	}
	errName := as.Lhs[0].(*ast.Ident).Name
	if len(ifs.Body.List) == 0 {
		return false
	}
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) == 0 {
		return false
	}
	last, ok := ret.Results[len(ret.Results)-1].(*ast.Ident)
	return ok && last.Name == errName
}
