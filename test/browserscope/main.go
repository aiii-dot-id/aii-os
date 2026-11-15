// browserscope says which tests drive a browser: the top-level tests that
// reach browsertest.Find, the one finder every test's browser comes
// through (internal/browsertest). test/run_race_scope.sh runs them in a
// scope of their own, alone, once every other scope has ended (and in a
// gate, the read-only stages too), so a page's waits are measured on a
// machine nothing else is loading. Every other scope runs with
// AII_TEST_BROWSERS=elsewhere, under which Find refuses: a test this
// misses fails where it runs, by the launch itself, not by this reading.
//
//	go run ./test/browserscope -go GO -out FILE -packages PATTERN...
//	    FILE: "<import path> <directory>" for each package whose tests
//	    can reach the finder, the directory from the module's root: the
//	    finder's own package, which must be among them, and each package
//	    a test file of which imports it.
//	go run ./test/browserscope -go GO -out FILE -list LIST PACKAGE
//	    FILE: the tests LIST names (what `go test -list` printed for
//	    PACKAGE, built as the runner builds it) that reach the finder, one
//	    a line.
//
// -packages refuses, with the reason, any package that imports the finder
// from a file that is not a test: it is test scaffolding, and a package
// that reached it outside its tests would carry it to every test that
// imports that package, where nothing here reads. Only the module's own
// packages can import an internal one, so the rest and browser scopes,
// which ask -packages of ./..., hold this for every scope. -list refuses
// the same of the one package, a dot import of the finder, and a LIST
// that names a test the package's test files do not declare: then this
// read other files than the build did.
//
// What "reach" is, read without types and so read wide: from a test's
// body, every name it uses that the package declares at its top level (a
// function, a variable, a constant, a type), every method by its name
// alone whatever its receiver, and every method of every type it names,
// so a value handed to code outside the package and called back through
// an interface is counted; through what those use, to any use of the
// finder. A TestMain or an init that reaches it counts every test. What
// this cannot see (a function value one test stores and another calls)
// is what the finder's own refusal is for.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// finderSuffix is the finder's package, under the module's path.
const finderSuffix = "/internal/browsertest"

// pkg is what `go list -json` says of a package that this reads.
type pkg struct {
	ImportPath   string
	Dir          string
	Module       *struct{ Path, Dir string }
	GoFiles      []string
	CgoFiles     []string
	TestGoFiles  []string
	XTestGoFiles []string
	Imports      []string
	TestImports  []string
	XTestImports []string
	Error        *struct{ Err string }
}

func main() {
	goBin := flag.String("go", "go", "the toolchain the runner uses")
	out := flag.String("out", "", "where to write the answer")
	packages := flag.Bool("packages", false, "list the packages whose tests can reach the finder")
	list := flag.String("list", "", "what go test -list printed for the one package named")
	flag.Parse()
	var answer []string
	var err error
	switch {
	case *out == "":
		err = errors.New("-out is required")
	case *packages && *list == "" && flag.NArg() > 0:
		answer, err = finderPackages(*goBin, flag.Args())
	case !*packages && *list != "" && flag.NArg() == 1:
		answer, err = browserTests(*goBin, *list, flag.Arg(0))
	default:
		err = errors.New("usage: browserscope -go GO -out FILE (-packages PATTERN... | -list LIST PACKAGE)")
	}
	if err == nil {
		err = os.WriteFile(*out, []byte(strings.Join(answer, "")), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "browserscope:", err)
		os.Exit(1)
	}
}

// golist is `go list -race -json` of the patterns: the files the race
// build selects, which are the files the runner's tests are built from
// (its race_args add -race and compiler flags; only -race selects files).
func golist(goBin string, patterns []string) ([]pkg, error) {
	cmd := exec.Command(goBin, append([]string{"list", "-race", "-json"}, patterns...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %v\n%s", strings.Join(patterns, " "), err, stderr.String())
	}
	var all []pkg
	dec := json.NewDecoder(bytes.NewReader(raw))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("go list's answer: %v", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("go list: %s: %s", p.ImportPath, p.Error.Err)
		}
		all = append(all, p)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("go list %s named no package", strings.Join(patterns, " "))
	}
	return all, nil
}

func finderOf(p pkg) (string, error) {
	if p.Module == nil || p.Module.Path == "" {
		return "", fmt.Errorf("%s is in no module", p.ImportPath)
	}
	return p.Module.Path + finderSuffix, nil
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// outsideTests refuses a package that imports the finder from a file
// that is not a test.
func outsideTests(p pkg, finder string) error {
	if p.ImportPath != finder && has(p.Imports, finder) {
		return fmt.Errorf("%s imports %s outside its tests: the finder is test scaffolding, and what reaches it from a package's own files reaches it from every test that imports the package, which this does not read", p.ImportPath, finder)
	}
	return nil
}

func finderPackages(goBin string, patterns []string) ([]string, error) {
	all, err := golist(goBin, patterns)
	if err != nil {
		return nil, err
	}
	var answer []string
	found := false
	for _, p := range all {
		finder, err := finderOf(p)
		if err != nil {
			return nil, err
		}
		if err := outsideTests(p, finder); err != nil {
			return nil, err
		}
		if p.ImportPath == finder || has(p.TestImports, finder) || has(p.XTestImports, finder) {
			found = found || p.ImportPath == finder
			dir, err := filepath.Rel(p.Module.Dir, p.Dir)
			if err != nil || dir == "." || strings.HasPrefix(dir, "..") || strings.ContainsAny(dir, " \t\n") {
				return nil, fmt.Errorf("%s: its directory %s cannot be named from the module's root %s", p.ImportPath, p.Dir, p.Module.Dir)
			}
			answer = append(answer, p.ImportPath+" "+filepath.ToSlash(dir)+"\n")
		}
	}
	if !found {
		return nil, fmt.Errorf("the finder's own package (%s) is not among %s: nothing here can say which tests reach it", "<module>"+finderSuffix, strings.Join(patterns, " "))
	}
	return answer, nil
}

var listed = regexp.MustCompile(`^(Test|Example|Fuzz)[[:alnum:]_]*$`)

func browserTests(goBin, listFile, pattern string) ([]string, error) {
	one, err := golist(goBin, []string{pattern})
	if err != nil {
		return nil, err
	}
	if len(one) != 1 {
		return nil, fmt.Errorf("%s names %d packages, not one", pattern, len(one))
	}
	p := one[0]
	finder, err := finderOf(p)
	if err != nil {
		return nil, err
	}
	if err := outsideTests(p, finder); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(listFile)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); listed.MatchString(line) {
			names = append(names, line)
		}
	}
	reach, declared, err := read(p, finder)
	if err != nil {
		return nil, err
	}
	var answer []string
	for _, name := range names {
		if !declared[name] {
			return nil, fmt.Errorf("go test -list names %s in %s, and none of the test files go list selected declares it: this read other files than the build did", name, p.ImportPath)
		}
		if reach(name) {
			answer = append(answer, name+"\n")
		}
	}
	sort.Strings(answer)
	return answer, nil
}

// The graph's nodes are names. "0:f" is the package's own top-level f
// (its files and its internal tests), "1:f" the external test package's,
// "m:f" every method called f, and root the finder itself.
const root = "root"

type graph struct {
	edges map[string]map[string]bool
}

func (g *graph) edge(from, to string) {
	if g.edges[from] == nil {
		g.edges[from] = map[string]bool{}
	}
	g.edges[from][to] = true
}

// unit is one of the two packages a test binary holds, as parsed.
type unit struct {
	scope   string // "0" or "1"
	files   []*ast.File
	decl    map[string]bool // its top-level names
	methods map[string][]string
}

// read parses the files the package's tests are built from and returns
// whether each top-level test reaches the finder, and the tests declared.
func read(p pkg, finder string) (func(string) bool, map[string]bool, error) {
	fset := token.NewFileSet()
	parse := func(names []string) ([]*ast.File, error) {
		var files []*ast.File
		for _, name := range names {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			files = append(files, f)
		}
		return files, nil
	}
	// The package's own files are read only where they hold the finder:
	// elsewhere they cannot name it, nor any name a test file declares,
	// and what they call back is counted from the test that named it.
	own := p.TestGoFiles
	if p.ImportPath == finder {
		own = append(append(append([]string(nil), p.GoFiles...), p.CgoFiles...), p.TestGoFiles...)
	}
	in, err := parse(own)
	if err != nil {
		return nil, nil, err
	}
	ex, err := parse(p.XTestGoFiles)
	if err != nil {
		return nil, nil, err
	}
	units := []*unit{{scope: "0", files: in}, {scope: "1", files: ex}}
	g := &graph{edges: map[string]map[string]bool{}}
	declared := map[string]bool{}
	for _, u := range units {
		u.decl, u.methods = map[string]bool{}, map[string][]string{}
		for _, f := range u.files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						u.decl[d.Name.Name] = true
						if listed.MatchString(d.Name.Name) {
							declared[d.Name.Name] = true
						}
					} else if t := recvType(d.Recv); t != "" {
						u.methods[t] = append(u.methods[t], d.Name.Name)
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.ValueSpec:
							for _, n := range s.Names {
								u.decl[n.Name] = true
							}
						case *ast.TypeSpec:
							u.decl[s.Name.Name] = true
						}
					}
				}
			}
		}
		// A type reaches its methods: a value of it can be called back
		// through an interface by code this does not read.
		for t, ms := range u.methods {
			for _, m := range ms {
				g.edge(u.scope+":"+t, "m:"+m)
			}
		}
	}
	if p.ImportPath == finder {
		g.edge("0:Find", root)
	}
	for _, u := range units {
		for _, f := range u.files {
			imports := map[string]string{} // local name -> path
			dotSelf := false               // the external test package dot-imports the package
			for _, spec := range f.Imports {
				path, _ := strconv.Unquote(spec.Path.Value)
				name := ""
				if spec.Name != nil {
					name = spec.Name.Name
				} else {
					name = path[strings.LastIndex(path, "/")+1:]
				}
				if path == finder && name == "." {
					return nil, nil, fmt.Errorf("%s dot-imports %s: a use of the finder must be named to be read", fset.File(f.Pos()).Name(), finder)
				}
				if name == "." && path == p.ImportPath {
					dotSelf = true
				}
				if name == "_" || name == "." {
					continue
				}
				imports[name] = path
			}
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					from := u.scope + ":" + d.Name.Name
					if d.Recv != nil {
						from = "m:" + d.Name.Name
					}
					uses(g, from, d, u, units, imports, dotSelf, p.ImportPath, finder)
				case *ast.GenDecl:
					for _, s := range d.Specs {
						switch s := s.(type) {
						case *ast.ValueSpec:
							for _, n := range s.Names {
								if s.Type != nil {
									uses(g, u.scope+":"+n.Name, s.Type, u, units, imports, dotSelf, p.ImportPath, finder)
								}
								for _, v := range s.Values {
									uses(g, u.scope+":"+n.Name, v, u, units, imports, dotSelf, p.ImportPath, finder)
								}
							}
						case *ast.TypeSpec:
							uses(g, u.scope+":"+s.Name.Name, s.Type, u, units, imports, dotSelf, p.ImportPath, finder)
							if s.TypeParams != nil {
								uses(g, u.scope+":"+s.Name.Name, s.TypeParams, u, units, imports, dotSelf, p.ImportPath, finder)
							}
						}
					}
				}
			}
		}
	}
	reaching := reachers(g)
	for _, whole := range []string{"0:TestMain", "1:TestMain", "0:init", "1:init"} {
		if reaching[whole] {
			return func(string) bool { return true }, declared, nil
		}
	}
	return func(name string) bool { return reaching["0:"+name] || reaching["1:"+name] }, declared, nil
}

// recvType is a method's receiver's type name, without its pointer or
// its type parameters.
func recvType(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	t := fl.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// uses adds an edge from one name to every name the node uses. A method
// is named by its name alone, except on a variable whose every declaration
// in the node gives it a type of another package: that package's methods
// are not ones a test file declares, and a value of a test's own type it
// holds was counted where the test named the type.
func uses(g *graph, from string, n ast.Node, u *unit, units []*unit, imports map[string]string, dotSelf bool, self, finder string) {
	foreign, local := foreignVars(n, imports, u)
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if path, ok := imports[id.Name]; ok && !u.decl[id.Name] && !local[id.Name] {
					switch path {
					case finder:
						if x.Sel.Name == "Find" {
							g.edge(from, root)
						}
					case self: // the external test package naming the package
						g.edge(from, "0:"+x.Sel.Name)
					}
					return false
				}
				if foreign[id.Name] {
					return false
				}
			}
			g.edge(from, "m:"+x.Sel.Name)
			ast.Inspect(x.X, visit)
			return false
		case *ast.Ident:
			if u.decl[x.Name] {
				g.edge(from, u.scope+":"+x.Name)
			}
			if dotSelf && units[0].decl[x.Name] {
				g.edge(from, "0:"+x.Name)
			}
		}
		return true
	}
	ast.Inspect(n, visit)
}

// foreignVars are the names declared in n only as parameters, results or
// vars of another package's type (t *testing.T, m *testing.M), and never
// otherwise: not by :=, not by range, not by a var of any other type, and
// not at the package's top level; and local, every name n declares, which
// is a variable and not an import of that name.
func foreignVars(n ast.Node, imports map[string]string, u *unit) (map[string]bool, map[string]bool) {
	foreign, other := map[string]bool{}, map[string]bool{}
	external := func(t ast.Expr) bool {
		for {
			switch x := t.(type) {
			case *ast.StarExpr:
				t = x.X
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					return false
				}
				_, imported := imports[id.Name]
				return imported && !u.decl[id.Name]
			default:
				return false
			}
		}
	}
	fields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, name := range f.Names {
				if external(f.Type) {
					foreign[name.Name] = true
				} else {
					other[name.Name] = true
				}
			}
		}
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncType:
			fields(x.Params)
			fields(x.Results)
		case *ast.FuncDecl:
			fields(x.Recv)
		case *ast.ValueSpec:
			for _, name := range x.Names {
				if x.Type != nil && external(x.Type) {
					foreign[name.Name] = true
				} else {
					other[name.Name] = true
				}
			}
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, l := range x.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						other[id.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			for _, e := range []ast.Expr{x.Key, x.Value} {
				if id, ok := e.(*ast.Ident); ok {
					other[id.Name] = true
				}
			}
		}
		return true
	})
	local := map[string]bool{}
	for name := range other {
		local[name] = true
	}
	for name := range foreign {
		local[name] = true
		if other[name] || u.decl[name] {
			delete(foreign, name)
		}
	}
	return foreign, local
}

// reachers is every name from which the finder can be reached.
func reachers(g *graph) map[string]bool {
	back := map[string][]string{}
	for from, tos := range g.edges {
		for to := range tos {
			back[to] = append(back[to], from)
		}
	}
	seen := map[string]bool{root: true}
	queue := []string{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, from := range back[n] {
			if !seen[from] {
				seen[from] = true
				queue = append(queue, from)
			}
		}
	}
	return seen
}
