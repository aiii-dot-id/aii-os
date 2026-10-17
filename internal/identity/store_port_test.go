package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestStorePortsReachNoMaterializationReplayOrHandle(t *testing.T) {
	forbidden := storeMethodsNoPortCarries(t)
	for _, port := range []reflect.Type{
		reflect.TypeFor[storePort](),
		reflect.TypeFor[TimerStore](),
	} {
		if port.NumMethod() == 0 {
			t.Fatalf("%s has no methods; the check would inspect nothing", port)
		}
		for i := 0; i < port.NumMethod(); i++ {
			name := port.Method(i).Name
			if why, ok := forbidden[name]; ok {
				t.Errorf("%s carries %s (%s). A store port in identity must not reach materialization, replay, reconciliation, a raw handle or the freeze switch; the admission door, replay and SAFE's owner call those through the concrete store they own", port, name, why)
			}
		}
	}
}

// storeMethodsNoPortCarries parses internal/store and returns each
// forbidden method with the rule that forbids it. A rule that finds
// nothing fails the test: a moved file or a renamed import would
// otherwise make the check pass by inspecting nothing.
func storeMethodsNoPortCarries(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join("..", "store")
	byFile := derivedWriterFiles(t, dir)
	named := map[string]string{
		"SetFrozen": "the projection's freeze switch, the work queue's too",
		"ReadWith":  "a read snapshot lent through the store's own Reader type",
	}
	forbidden := map[string]string{}
	found := map[string]int{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the store's source: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		file := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse store/%s: %v", file, err)
		}
		sqlName := importName(f, "database/sql")
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !onStore(fn) {
				continue
			}
			name := fn.Name.Name
			if why, ok := named[name]; ok {
				forbidden[name] = why + ", declared in store/" + file
				found["named:"+name]++
				continue
			}
			switch {
			case byFile[file] != "":
				forbidden[name] = byFile[file] + ", declared in store/" + file
				found[file]++
			case strings.HasPrefix(name, "Materialize"):
				forbidden[name] = "materialization, declared in store/" + file
			case strings.HasPrefix(name, "Replay"):
				forbidden[name] = "replay, declared in store/" + file
			case sqlName != "" && mentionsPackage(fn.Type, sqlName):
				forbidden[name] = "a database handle in its signature, declared in store/" + file
				found["database/sql"]++
			}
		}
	}
	for rule := range byFile {
		if found[rule] == 0 {
			t.Fatalf("store/%s declares no method on Store; the file moved or was renamed, so name its successor here", rule)
		}
	}
	if found["database/sql"] == 0 {
		t.Fatalf("no method on Store carries a database/sql type in its signature; the handle rule matched nothing, so check how the handle is lent")
	}
	for name := range named {
		if found["named:"+name] == 0 {
			t.Fatalf("no method on Store is named %s any more; the rule that refused it would refuse nothing, so name its successor here", name)
		}
	}
	return forbidden
}

func derivedWriterFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, "catalog.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse store/catalog.go: %v", err)
	}
	files := map[string]string{"materialize.go": "the materializer"}
	var lit *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "writers" || len(vs.Values) != 1 {
			return true
		}
		lit, _ = vs.Values[0].(*ast.CompositeLit)
		return false
	})
	if lit == nil {
		t.Fatal("store/catalog.go declares no writers map literal; the contract writer list moved, so read it from where it is now")
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		table, _ := strconv.Unquote(kv.Key.(*ast.BasicLit).Value)
		inner, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, e := range inner.Elts {
			if ikv, ok := e.(*ast.KeyValueExpr); ok {
				if lit, ok := ikv.Key.(*ast.BasicLit); ok {
					file, _ := strconv.Unquote(lit.Value)
					if files[file] == "" {
						files[file] = "a writer of " + table + " in the contract writer list"
					}
				}
			}
		}
	}
	if len(files) < 2 {
		t.Fatalf("the contract writer list named no file (%v); the parse matched nothing", files)
	}
	return files
}

// The ports hold only if nothing beside them keeps the store: no identity
// type the store reaches through may hold a *store.Store, or an interface
// *store.Store satisfies other than the ports the test above checks.
func TestTheEngineHoldsTheStoreOnlyThroughItsPorts(t *testing.T) {
	concrete := reflect.TypeFor[*store.Store]()
	checked := map[reflect.Type]bool{reflect.TypeFor[storePort](): true, reflect.TypeFor[TimerStore](): true}
	for _, holder := range []reflect.Type{reflect.TypeFor[Engine](), reflect.TypeFor[storeTimers]()} {
		stores := 0
		for i := 0; i < holder.NumField(); i++ {
			f := holder.Field(i)
			switch {
			case f.Type == concrete:
				t.Errorf("%s.%s is a *store.Store; hold it through a port the store-port test checks", holder.Name(), f.Name)
			case f.Type.Kind() == reflect.Interface && f.Type.NumMethod() > 0 && concrete.Implements(f.Type):
				if !checked[f.Type] {
					t.Errorf("%s.%s is a %s, which *store.Store satisfies and the store-port test does not check", holder.Name(), f.Name, f.Type)
				}
				stores++
			}
		}
		if stores == 0 {
			t.Errorf("%s holds no store port; this test inspects nothing there", holder.Name())
		}
	}
}

// onStore reports whether fn is a method on store.Store or *store.Store.
func onStore(fn *ast.FuncDecl) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return false
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	return ok && id.Name == "Store"
}

// importName is the name a file uses for the package at path, or "" if
// the file does not import it.
func importName(f *ast.File, path string) string {
	for _, spec := range f.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err != nil || p != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return path[strings.LastIndex(path, "/")+1:]
	}
	return ""
}

// mentionsPackage reports whether a signature names anything from the
// package the file imports as pkg, at any depth (func(db *sql.DB) error
// counts).
func mentionsPackage(sig *ast.FuncType, pkg string) bool {
	seen := false
	ast.Inspect(sig, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == pkg {
				seen = true
			}
		}
		return !seen
	})
	return seen
}
