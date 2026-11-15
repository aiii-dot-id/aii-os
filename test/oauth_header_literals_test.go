package test

import (
	"fmt"
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

// NO VENDOR REQUEST HEADER VALUE IS COMPILED IN (the operator, 2026-11-11:
// "ALL OAuth headers are config file only. Any agent caught hard coding
// OAuth headers is immediately terminated"; ruling of 2026-11-11 §3). A
// 2.1.283 client version lent from compiled defaults locked a live identity
// out of Anthropic that morning, and two values — the API version's fallback
// and the cache-diagnostics beta — were Go literals on the OAuth paths.
//
// WHAT THIS GUARD IS (ruling of 2026-11-11 §2d): a syntax check, not the
// proof of the directive. It reads every non-test Go file on three paths
// (internal/llm: inference; internal/oauth: sign-in, token exchange and
// refresh; internal/app/provider_discovery.go: the model-list probe) and
// refuses a Header.Set/Header.Add whose value expression holds a string
// literal or a package constant holding one. It follows no data flow: a
// value returned by a helper, ranged out of a map literal, or set by a later
// layer without Header.Set/Add is not seen, and the request captures named
// in TestTheHeaderLiteralGuardRefusesWhatItIsFor are what cover those. The
// captures of the final request are the proof. Two kinds stay compiled
// because they are the protocol's, not the vendor's: the media types of
// Content-Type and Accept, and the "Bearer " that prefixes a token.
//
// guard:tree
func TestNoOAuthRequestHeaderValueIsCompiledIn(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, dir := range []string{"internal/llm", "internal/oauth"} {
		found, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil || len(found) == 0 {
			t.Fatalf("no Go files read in %s: %v", dir, err)
		}
		files = append(files, found...)
	}
	files = append(files, filepath.Join(root, "internal", "app", "provider_discovery.go"))
	checked := 0
	var findings []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, f)
		consts := packageStringConsts(t, filepath.Dir(f))
		// A constant read through its package's name: internal/app reads
		// internal/llm's (llm.DefaultAnthropicVersion was such a fallback).
		for name := range packageStringConsts(t, filepath.Join(root, "internal", "llm")) {
			consts["llm."+name] = true
		}
		got, err := compiledHeaderValues(rel, src, consts)
		if err != nil {
			t.Fatal(err)
		}
		findings = append(findings, got...)
		checked++
	}
	if checked < 20 {
		t.Fatalf("read %d files: the guard is not reading the request paths", checked)
	}
	for _, f := range findings {
		t.Error(f)
	}
}

// A guard that cannot fail is indistinguishable from one that passed: each
// planted literal is refused, and the protocol's own values are not. What it
// cannot see is planted too, and names the capture that covers it; that test
// must exist.
func TestTheHeaderLiteralGuardRefusesWhatItIsFor(t *testing.T) {
	consts := map[string]bool{"defaultVersion": true, "llm.DefaultAnthropicVersion": true}
	for _, tc := range []struct {
		name, body string
		refused    bool
		coveredBy  string // for a value the guard does not see: the capture that covers it
	}{
		{"a concatenation", `r.Header.Set("User-Agent", "AII-OS/"+version)`, true, ""},
		{"a helper's return value", `r.Header.Set("User-Agent", userAgent())`, false, "TestEveryAuthorityRequestCarriesTheContractsConfiguredHeaders"},
		{"a map literal", `for k, v := range map[string]string{"User-Agent": "AII-OS/1.0"} { r.Header.Set(k, v) }`, false, "TestTheCapabilityLookupSendsItsOwnHeadersAndNoCredential"},
		{"a header set by a later layer", `r.Header["Anthropic-Version"] = []string{"2023-06-01"}`, false, "TestAClaudeRequestWithoutAConfiguredVersionIsNotSent"},
		{"a version literal", `r.Header.Set("anthropic-version", "2023-06-01")`, true, ""},
		{"a version constant", `r.Header.Set("anthropic-version", defaultVersion)`, true, ""},
		{"another package's version constant", `req.Header.Set("anthropic-version", llm.DefaultAnthropicVersion)`, true, ""},
		{"a beta added to a header", `r.Header.Set("anthropic-beta", addBeta(r.Header.Get("anthropic-beta"), "cache-diagnosis-2026-04-07"))`, true, ""},
		{"a configured beta added to a header", `r.Header.Set("anthropic-beta", addBeta(r.Header.Get("anthropic-beta"), policy.DiagnosticsBeta))`, false, ""},
		{"a user agent", `r.Header.Add("User-Agent", "AII-OS/1.0")`, true, ""},
		{"a configured value", `r.Header.Set(k, v)`, false, ""},
		{"the protocol's media type", `r.Header.Set("Content-Type", "application/json")`, false, ""},
		{"the protocol's accept", `r.Header.Set("Accept", "text/event-stream")`, false, ""},
		{"a bearer token", `r.Header.Set("Authorization", "Bearer "+token)`, false, ""},
	} {
		src := "package p\nfunc f() {\n" + tc.body + "\n}\n"
		got, err := compiledHeaderValues("planted.go", []byte(src), consts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if (len(got) > 0) != tc.refused {
			t.Errorf("%s: findings %q, want refused=%v", tc.name, got, tc.refused)
		}
		if !tc.refused && tc.coveredBy != "" && !testDefined(t, tc.coveredBy) {
			t.Errorf("%s: the guard does not see it, and its capture %s is not in the tree", tc.name, tc.coveredBy)
		}
	}
}

// testDefined says whether a test of that name is declared in internal/.
func testDefined(t *testing.T, name string) bool {
	t.Helper()
	found := false
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || found || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = strings.Contains(string(src), "\nfunc "+name+"(t *testing.T)")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// packageStringConsts names the package-level constants of dir whose value
// is a string literal.
func packageStringConsts(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				v := spec.(*ast.ValueSpec)
				for i, name := range v.Names {
					if i < len(v.Values) {
						if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							out[name.Name] = true
						}
					}
				}
			}
		}
	}
	return out
}

// compiledHeaderValues lists each Header.Set/Header.Add in src whose value
// holds a compiled string: a literal, or a constant named in consts.
func compiledHeaderValues(name string, src []byte, consts map[string]bool) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Set" && sel.Sel.Name != "Add") {
			return true
		}
		recv, ok := sel.X.(*ast.SelectorExpr)
		if !ok || recv.Sel.Name != "Header" {
			return true
		}
		header := ""
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			header, _ = strconv.Unquote(lit.Value)
		}
		var compiled []string
		ast.Inspect(call.Args[1], func(m ast.Node) bool {
			switch v := m.(type) {
			case *ast.CallExpr:
				// Header.Get("name") reads a header by its name; a name is
				// not a value.
				if s, ok := v.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Get" {
					if r, ok := s.X.(*ast.SelectorExpr); ok && r.Sel.Name == "Header" {
						return false
					}
				}
			case *ast.BasicLit:
				if v.Kind != token.STRING {
					return true
				}
				s, _ := strconv.Unquote(v.Value)
				switch {
				case s == "":
				case strings.EqualFold(header, "Content-Type"), strings.EqualFold(header, "Accept"):
				case strings.EqualFold(header, "Authorization") && s == "Bearer ":
				default:
					compiled = append(compiled, strconv.Quote(s))
				}
			case *ast.Ident:
				if consts[v.Name] {
					compiled = append(compiled, "const "+v.Name)
				}
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && consts[x.Name+"."+v.Sel.Name] {
					compiled = append(compiled, "const "+x.Name+"."+v.Sel.Name)
					return false
				}
			}
			return true
		})
		if len(compiled) > 0 {
			sort.Strings(compiled)
			out = append(out, fmt.Sprintf("%s: header %q gets a compiled value %s: it must come from providers.json or the credential", fset.Position(call.Pos()), header, strings.Join(compiled, ", ")))
		}
		return true
	})
	return out, nil
}
