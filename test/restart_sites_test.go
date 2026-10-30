package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var restartSites = []restartSite{
	// The operator's doors, and the two handlers that hand them Restart.
	{"internal/app/hooks.go", "App.buildLiveHandler", 1, "hands Restart to the dashboard's operator doors, live and in SAFE"},
	{"internal/app/ceremony.go", "App.buildFirstbootHandler", 1, "hands Restart to the first boot's dashboard's operator doors"},
	{"internal/dashboard/ws_config.go", "Server.wsRestart", 1, "the operator's Restart"},
	{"internal/dashboard/continuity.go", "Server.answerBackups", 3, "the restart after the operator's restore, choice of the identity's files, and key put back"},
	// DEGRADED, not SAFE.
	{"internal/app/ring5_wait.go", "App.askForRing5", 1, "DEGRADED (Ring 5): a start no Ring 5 server answered is made again once one answers"},
	{"internal/app/startretry.go", "App.planStartRetry", 1, "a SAFE page whose checks could not run, held by a cause that may pass with time, tries the start again by itself within its bound"},
}

type restartSite struct {
	file, fn string
	n        int
	why      string
}

// guard:tree
func TestOnlyTheListedSitesAskForARestart(t *testing.T) {
	root := repoRoot(t)
	sources := map[string][]byte{}
	for _, pkg := range []string{"internal/app", "internal/dashboard"} {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(pkg)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pkg), name))
			if err != nil {
				t.Fatal(err)
			}
			sources[pkg+"/"+name] = src
		}
	}
	if len(sources) < 150 {
		t.Fatalf("read %d files of internal/app and internal/dashboard — the guard is not reading the packages", len(sources))
	}
	found := restartUsesIn(t, sources)
	if len(found) < len(restartSites) {
		t.Fatalf("found %d sites in the tree and the list names %d — the guard is not reading the calls", len(found), len(restartSites))
	}
	for _, p := range judgeRestartSites(found, restartSites) {
		t.Error(p)
	}
}

// guard:tree
func TestTheRestartGuardRefusesWhatItIsFor(t *testing.T) {
	listed := []restartSite{
		{"a.go", "App.door", 1, "listed"},
		{"a.go", "gone", 1, "listed, no longer in the tree"},
	}
	found := restartUsesIn(t, map[string][]byte{"a.go": []byte(`package app

type App struct{ hooks struct{ Restart func() error } }

func (a *App) Restart() error { return nil } // the method itself is no use of it

func (a *App) door() {
	if a.hooks.Restart == nil { // a nil check is no use
		return
	}
	_ = a.Restart() // listed
	_ = a.Restart() // a second use in a listed function
}

func (a *App) wait() {
	go func() { _ = a.Restart() }() // inside a closure: its function's
}

var handed = struct{ Restart func() error }{Restart: (&App{}).Restart} // handed on as a value
`)})
	got := strings.Join(judgeRestartSites(found, listed), "\n")
	for _, want := range []string{
		"a.go App.door: 2 uses of Restart, and the list allows 1",
		"a.go App.wait: 1 use of Restart",
		"a.go (package level): 1 use of Restart",
		"a.go gone is listed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the guard did not refuse %q; it said:\n%s", want, got)
		}
	}
	if strings.Contains(got, "App.Restart") {
		t.Errorf("the guard counted the method's own declaration:\n%s", got)
	}
}

// restartUsesIn counts, per file and enclosing function, every selector
// named Restart — a call, or a method or hook taken as a value — except one
// compared with nil. Every file is read, whatever its build constraint.
func restartUsesIn(t *testing.T, sources map[string][]byte) map[[2]string]int {
	t.Helper()
	found := map[[2]string]int{}
	fset := token.NewFileSet()
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		count := func(fn string, n ast.Node) {
			nilChecked := map[*ast.SelectorExpr]bool{}
			ast.Inspect(n, func(n ast.Node) bool {
				if b, ok := n.(*ast.BinaryExpr); ok && (b.Op == token.EQL || b.Op == token.NEQ) {
					for _, pair := range [][2]ast.Expr{{b.X, b.Y}, {b.Y, b.X}} {
						sel, isSel := pair[0].(*ast.SelectorExpr)
						if id, isNil := pair[1].(*ast.Ident); isSel && isNil && id.Name == "nil" {
							nilChecked[sel] = true
						}
					}
				}
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Restart" && !nilChecked[sel] {
					found[[2]string{name, fn}]++
				}
				return true
			})
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				count("(package level)", decl)
				continue
			}
			count(funcName(fd), fd)
		}
	}
	return found
}

// judgeRestartSites answers every way the tree and the list disagree.
func judgeRestartSites(found map[[2]string]int, listed []restartSite) []string {
	allowed := map[[2]string]int{}
	for _, r := range listed {
		allowed[[2]string{r.file, r.fn}] += r.n
	}
	var problems []string
	for k, n := range found {
		switch want, ok := allowed[k]; {
		case !ok:
			problems = append(problems, k[0]+" "+k[1]+": "+strconv.Itoa(n)+" use of Restart on no list — SAFE is left only by a restart, and only the listed ones ask for it: list the site in test/restart_sites_test.go with the act or ruling it serves")
		case n > want:
			problems = append(problems, k[0]+" "+k[1]+": "+strconv.Itoa(n)+" uses of Restart, and the list allows "+strconv.Itoa(want))
		}
	}
	for k, want := range allowed {
		if n := found[k]; n < want {
			problems = append(problems, k[0]+" "+k[1]+" is listed for "+strconv.Itoa(want)+" use(s) of Restart and the tree has "+strconv.Itoa(n)+": prune the list")
		}
	}
	return problems
}
