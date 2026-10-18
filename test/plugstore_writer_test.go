package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

var storeWriterNames = map[string][]string{
	"internal/plugstore":  {"OpenWriter", "OpenForRemoval"},
	"internal/pluginhost": {"EnsureEntry", "RepairEntry", "StoreRoom", "WorkMaterial", "GuardedFetch"},
}

// storeWriterOwners are the packages allowed to name them, outside the
// packages that define them.
var storeWriterOwners = map[string]string{
	"internal/machine": "the account's machine service: the store's one writer",
}

// guard:tree
func TestOnlyTheMachineServiceWritesThePluginStore(t *testing.T) {
	root := repoRoot(t)
	var stray []string
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
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		pkg := filepath.ToSlash(rel)
		if _, ok := storeWriterOwners[pkg]; ok {
			return nil
		}
		if _, defines := storeWriterNames[pkg]; defines {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		stray = append(stray, storeWriterCalls(f, path, root)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(stray)
	for _, s := range stray {
		t.Errorf("%s; only the machine service writes the plugin store", s)
	}
}

// storeWriterCalls names every use in f of a name storeWriterNames lists,
// through whatever name f imports its package under.
func storeWriterCalls(f *ast.File, path, root string) []string {
	local := map[string][]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		pkg, ok := strings.CutPrefix(p, modulePrefix)
		if !ok {
			continue
		}
		names, ok := storeWriterNames[pkg]
		if !ok {
			continue
		}
		as := filepath.Base(pkg)
		if imp.Name != nil {
			as = imp.Name.Name
		}
		local[as] = names
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		for _, name := range local[x.Name] {
			if sel.Sel.Name == name {
				rel, _ := filepath.Rel(root, path)
				out = append(out, filepath.ToSlash(rel)+" names "+x.Name+"."+name)
			}
		}
		return true
	})
	return out
}

// And it can fail: a file outside the machine service that opens the
// store's writer, under any import name, is named.
func TestTheStoreWriterGuardNamesAStrayWriter(t *testing.T) {
	src := `package app

import ps "github.com/aiii-dot-id/aii-os/internal/plugstore"

func open() { _, _ = ps.OpenWriter("/x") }

func remove() { _, _ = ps.OpenForRemoval("/x") }
`
	f, err := parser.ParseFile(token.NewFileSet(), "stray.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := storeWriterCalls(f, "/r/internal/app/stray.go", "/r")
	if len(got) != 2 || !strings.Contains(got[0], "ps.OpenWriter") || !strings.Contains(got[1], "ps.OpenForRemoval") {
		t.Fatalf("the guard did not name a stray writer and a stray removal: %v", got)
	}
}

// guard:tree
func TestAnIdentityCarriesNoMaterialFetcher(t *testing.T) {
	opts := reflect.TypeOf(pluginhost.Options{})
	fetcher := reflect.TypeOf(pluginhost.ModelFetcher(nil))
	for i := range opts.NumField() {
		if f := opts.Field(i); f.Type == fetcher {
			t.Errorf("pluginhost.Options.%s carries a material fetcher; only the machine service downloads", f.Name)
		}
	}
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
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		if pkg := filepath.ToSlash(rel); pkg == "internal/machine" || pkg == "internal/pluginhost" {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, s := range fetcherFields(f) {
			r, _ := filepath.Rel(root, path)
			t.Errorf("%s: an AcquirerConfig sets %s; an identity downloads nothing", filepath.ToSlash(r), s)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fetcherFields names each Fetcher an AcquirerConfig literal in f sets.
func fetcherFields(f *ast.File) []string {
	as := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == modulePrefix+"internal/pluginhost" {
			as = "pluginhost"
			if imp.Name != nil {
				as = imp.Name.Name
			}
		}
	}
	if as == "" {
		return nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "AcquirerConfig" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); !ok || x.Name != as {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Fetcher" {
					out = append(out, "Fetcher")
				}
			}
		}
		return true
	})
	return out
}

// And it can fail.
func TestTheFetcherGuardNamesAnIdentitysFetcher(t *testing.T) {
	src := `package app

import ph "github.com/aiii-dot-id/aii-os/internal/pluginhost"

var _ = ph.AcquirerConfig{Fetcher: nil}
`
	f, err := parser.ParseFile(token.NewFileSet(), "stray.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := fetcherFields(f); len(got) != 1 {
		t.Fatalf("the guard did not name an identity's fetcher: %v", got)
	}
}
