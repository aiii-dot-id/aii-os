package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var appContextRoots = []appContextRoot{
	// The lifetime's own root.
	{"app.go", "New", 1, "the lifetime's own root: Shutdown cancels it (lifetime())"},
	{"lifecycle.go", "App.lifetime", 1, "the lifetime of a zero-value App (a test fixture), which has none"},
	// The shutdown path's own roots: the lifetime is already cancelled.
	{"lifecycle.go", "App.Run", 1, "a shutdown root: the process leaves once the teardown has, each step of which carries its own bound"},
	{"lifecycle.go", "App.stop", 1, "a shutdown root: the lifetime is already cancelled when it runs"},
	{"lifecycle.go", "App.closeLiveResources", 2, "teardown roots bounded by pluginhost.StopBudget, serving Shutdown and a failed birth's teardown alike"},
	// Boot before the lifetime runs anything.
	{"boot.go", "App.startLive", 3, "boot roots: each step of the integrity ladder finishes or fails before the lifetime runs anything"},
	// Record writers that must complete, whose caller carries no context.
	{"grades.go", "App.gradeResult", 1, "a record writer that must complete: the grade was admitted, and the page's hook carries no ctx"},
	{"interaction_record.go", "App.wireProjectInteractionRecorder", 1, "a record writer that must complete: the manifest is saved, and project.Manager's recorder carries no ctx"},
	// Bounded fences that must outlive their caller.
	{"pluginruntime.go", "App.watchPinnedPredecessor", 2, "bounded fences (pluginhost.StopBudget): the pinned predecessor's stop and abort outlive every caller; its watcher is engine work, joined after the plugin teardown"},
	{"voice_session.go", "voiceHandle.fenceBounded", 1, "a bounded fence (voiceAdmissionBound): a cancelled caller still fences what it admitted; its caller's join joins it (the hush's fence: engine work)"},
	{"voice_session.go", "App.abortVoiceSessionsUnderSafe", 1, "a bounded fence (10 s): SAFE's abort outlives its caller; it is engine work, ended by the plugin teardown's close of the engine and joined after it"},
}

type appContextRoot struct {
	file, fn string
	n        int
	why      string
}

// guard:tree
func TestAppRootsWorkOnBackgroundOnlyWhereListed(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "app")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = src
	}
	if len(sources) < 100 {
		t.Fatalf("read %d files of internal/app — the guard is not reading the package", len(sources))
	}
	found := contextRootsIn(t, sources)
	if len(found) < 10 {
		t.Fatalf("found %d roots in internal/app — the guard is not reading the calls", len(found))
	}
	for _, p := range judgeContextRoots(found, appContextRoots) {
		t.Error(p)
	}
}

// guard:tree
func TestTheContextRootGuardRefusesWhatItIsFor(t *testing.T) {
	listed := []appContextRoot{
		{"a.go", "App.stop", 1, "listed"},
		{"a.go", "gone", 1, "listed, no longer in the tree"},
	}
	found := contextRootsIn(t, map[string][]byte{"a.go": []byte(`package app

import (
	"context"
	ctxpkg "context"
)

type App struct{}

func (a *App) stop() {
	_ = context.Background() // listed
	_ = context.Background() // a second root in a listed function
}

func job() context.Context { return ctxpkg.TODO() } // renamed import, TODO

var root = context.Background // taken as a value, not called

func (a *App) lifetime() context.Context { return context.WithoutCancel(context.Context(nil)) } // not a root
`)})
	got := strings.Join(judgeContextRoots(found, listed), "\n")
	for _, want := range []string{
		"a.go App.stop: 2 context.Background()/TODO() roots, and the list allows 1",
		"a.go job: 1 context.Background()/TODO() root",
		"a.go (package level): 1 context.Background()/TODO() root",
		"a.go gone is listed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the guard did not refuse %q; it said:\n%s", want, got)
		}
	}
	if strings.Contains(got, "App.lifetime") {
		t.Errorf("the guard refused what is not a root:\n%s", got)
	}
}

// contextRootsIn counts, per file and enclosing function, every reference
// to context.Background or context.TODO — called or taken as a value —
// under whatever name the file imports "context" as. Every file is read,
// whatever its build constraint.
func contextRootsIn(t *testing.T, sources map[string][]byte) map[[2]string]int {
	t.Helper()
	found := map[[2]string]int{}
	fset := token.NewFileSet()
	for name, src := range sources {
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		local := map[string]bool{}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == "context" {
				if imp.Name != nil {
					local[imp.Name.Name] = true
				} else {
					local["context"] = true
				}
			}
		}
		count := func(fn string, n ast.Node) {
			ast.Inspect(n, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Background" && sel.Sel.Name != "TODO") {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && local[pkg.Name] {
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

// funcName is Recv.Name for a method, Name for a function.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	r := fd.Recv.List[0].Type
	if s, ok := r.(*ast.StarExpr); ok {
		r = s.X
	}
	if ix, ok := r.(*ast.IndexExpr); ok {
		r = ix.X
	}
	if id, ok := r.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// judgeContextRoots answers every way the tree and the list disagree.
func judgeContextRoots(found map[[2]string]int, listed []appContextRoot) []string {
	allowed := map[[2]string]int{}
	for _, r := range listed {
		allowed[[2]string{r.file, r.fn}] += r.n
	}
	var problems []string
	for k, n := range found {
		switch want, ok := allowed[k]; {
		case !ok:
			problems = append(problems, k[0]+" "+k[1]+": "+strconv.Itoa(n)+" context.Background()/TODO() root(s) on no list — take a.lifetime(), the caller's ctx, or context.WithoutCancel of it at a record writer; or list it in test/app_context_roots_test.go with its reason")
		case n > want:
			problems = append(problems, k[0]+" "+k[1]+": "+strconv.Itoa(n)+" context.Background()/TODO() roots, and the list allows "+strconv.Itoa(want)+" — a new one takes a.lifetime(), the caller's ctx, or WithoutCancel of it")
		}
	}
	for k, want := range allowed {
		if n := found[k]; n < want {
			problems = append(problems, k[0]+" "+k[1]+" is listed with "+strconv.Itoa(want)+" root(s) and the tree holds "+strconv.Itoa(n)+" — shrink or delete its line in appContextRoots")
		}
	}
	sort.Strings(problems)
	return problems
}
