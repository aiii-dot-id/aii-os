package test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// EVERY TEST A RULING CITES AS ITS PROOF IS A TEST IN THE TREE. Where a
// rulings document is kept beside the source, each test a ruling names as
// its proof must be a func some _test.go in the tree defines, or be listed
// with where its proof is kept. "Defines" is Go's word: a func declaration
// go/parser finds in this repository's own _test.go files, outside
// testdata, and not in another checkout nested in this one.
type rulingProofElsewhere struct{ ruling, name, why string }

var (
	rulingHeading  = regexp.MustCompile(`^## (R[0-9]+) `)
	rulingCitation = regexp.MustCompile(`\b((?:Test|Benchmark|Fuzz|Example)[A-Z0-9_][A-Za-z0-9_]*)(\*)?`)
)

// guard:tree
func TestEveryTestARulingCitesIsInTheTree(t *testing.T) {
	t.Skip("this tree carries no rulings document; the guard's own refusals are held by the tests below")
}

// guard:tree
func TestTheRulingProofGuardRefusesWhatItIsFor(t *testing.T) {
	cites := rulingCitations([]byte("Before any ruling, `TestInThePreamble`.\n" +
		"## R1 — One\nHeld by `TestPresent`, by TestGone, by the TestFamily* family, and `TestPresent/a_subtest`.\n" +
		"## R2 — Two (2026-01-01)\n`TestListedElsewhere` and `TestListedButBack`. Tests in prose, and `Test` alone, are no citation.\n" +
		"## R3 — Three\n`TestNoFamily*`, and `BenchmarkGone`.\n"))
	defined := map[string]bool{}
	if err := definedTestsIn("p_test.go", []byte("package p\n\nfunc TestPresent(t *testing.T) {}\n\nfunc (s *suite) TestFamilyOne(t *testing.T) {}\n\nfunc TestListedButBack(t *testing.T) {}\n\n// func TestGone( is a comment, not a definition\n"), defined); err != nil {
		t.Fatal(err)
	}
	listed := []rulingProofElsewhere{
		{"R2", "TestListedElsewhere", "kept elsewhere"},
		{"R2", "TestListedButBack", "kept elsewhere"},
		{"R9", "TestNotCited", "kept elsewhere"},
	}
	got := strings.Join(judgeRulingProofs(cites, defined, listed), "\n")
	for _, want := range []string{
		"(before R1) cites TestInThePreamble",
		"R1 cites TestGone",
		"R3 cites TestNoFamily*",
		"R3 cites BenchmarkGone",
		"R2 TestListedButBack is listed as proof kept elsewhere, and the tree defines it",
		"R9 TestNotCited is listed as proof kept elsewhere, and R9 does not cite it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the guard did not refuse %q; it said:\n%s", want, got)
		}
	}
	for _, wrong := range []string{"TestPresent", " TestFamily*", "TestListedElsewhere", "Tests ", "a_subtest"} {
		if strings.Contains(got, wrong) {
			t.Errorf("the guard refused %q, which holds; it said:\n%s", wrong, got)
		}
	}
	if n := strings.Count(got, "\n") + 1; n != 6 {
		t.Errorf("the guard said %d things, and 6 are wrong; it said:\n%s", n, got)
	}
}

// guard:tree
func TestTheRulingProofGuardReadsOnlyThisTreesDeclarations(t *testing.T) {
	// A deleted test left in a block comment or a raw string defines nothing.
	defined := map[string]bool{}
	src := "package p\n\nfunc TestDeclared(t *testing.T) {}\n\n/*\nfunc TestInABlockComment(t *testing.T) {}\n*/\n\nvar kept = `\nfunc TestInARawString(t *testing.T) {}\n`\n"
	if err := definedTestsIn("p_test.go", []byte(src), defined); err != nil {
		t.Fatal(err)
	}
	if !defined["TestDeclared"] {
		t.Errorf("the guard did not read a declared test; it read %v", defined)
	}
	for _, gone := range []string{"TestInABlockComment", "TestInARawString"} {
		if defined[gone] {
			t.Errorf("the guard counted %s, which no func declares, as defined", gone)
		}
	}

	// Another checkout nested in this one, testdata, and what go's ./... skips
	// are not this tree, in a source archive with no .git and in a checkout.
	write := func(t *testing.T, root, rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	census := func(t *testing.T, root string, foreign ...string) {
		got, err := treeTestDefinitions(root)
		if err != nil {
			t.Fatal(err)
		}
		if !got["TestOurs"] {
			t.Errorf("the guard did not read this tree's own test; it read %v", got)
		}
		for _, f := range foreign {
			if got[f] {
				t.Errorf("the guard counted %s, which is not this tree's test, as defined", f)
			}
		}
	}
	t.Run("archive", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "a/ours_test.go", "package a\n\nfunc TestOurs(t *testing.T) {}\n")
		write(t, root, "a/testdata/fixture_test.go", "package a\n\nfunc TestInTestdata(t *testing.T) {}\n")
		write(t, root, "nested/other/.git/HEAD", "ref: refs/heads/main\n")
		write(t, root, "nested/other/b/theirs_test.go", "package b\n\nfunc TestTheirs(t *testing.T) {}\n")
		write(t, root, ".claude/worktrees/w/c/hidden_test.go", "package c\n\nfunc TestHidden(t *testing.T) {}\n")
		census(t, root, "TestTheirs", "TestInTestdata", "TestHidden")
	})
	t.Run("git", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("no git on this host; the archive census above is the one it reads")
		}
		gitInit := func(dir string) {
			if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
				t.Fatalf("git init %s: %v %s", dir, err, out)
			}
		}
		root := t.TempDir()
		gitInit(root)
		write(t, root, "a/ours_test.go", "package a\n\nfunc TestOurs(t *testing.T) {}\n")
		write(t, root, "a/testdata/fixture_test.go", "package a\n\nfunc TestInTestdata(t *testing.T) {}\n")
		gitInit(filepath.Join(root, ".claude", "worktrees", "other"))
		write(t, root, ".claude/worktrees/other/b/theirs_test.go", "package b\n\nfunc TestTheirs(t *testing.T) {}\n")
		census(t, root, "TestTheirs", "TestInTestdata")
	})
}

// treeTestDefinitions reads every test this repository's own _test.go files
// declare, none under testdata, which go test does not build. In a git
// checkout they are the files git lists (tracked, and new ones not ignored),
// so a checkout nested in this one is not read. A source archive has no .git,
// and the source guards run there too (repoRoot), so there
// the tree is walked and refuses the same: a directory holding its own .git is
// another checkout, and one whose name begins . or _ is one go's ./... skips.
func treeTestDefinitions(root string) (map[string]bool, error) {
	if _, err := os.Lstat(filepath.Join(root, ".git")); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return archiveTestDefinitions(root)
	}
	cmd := exec.Command("git", "-c", "core.quotePath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*_test.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	defined := map[string]bool{}
	seen := map[string]bool{}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || seen[rel] || !strings.HasSuffix(rel, "_test.go") || strings.Contains("/"+rel, "/testdata/") {
			continue
		}
		seen[rel] = true
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			if os.IsNotExist(err) {
				continue // staged, then deleted: nothing to read
			}
			return nil, err
		}
		if err := definedTestsIn(rel, src, defined); err != nil {
			return nil, err
		}
	}
	return defined, nil
}

// archiveTestDefinitions reads the tests of a tree with no git metadata.
func archiveTestDefinitions(root string) (map[string]bool, error) {
	defined := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir // another checkout
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return definedTestsIn(filepath.ToSlash(rel), src, defined)
	})
	if err != nil {
		return nil, err
	}
	return defined, nil
}

// rulingCitations reads every test a ruling cites, keyed by ruling and name;
// a name ending in * is a family named by its prefix.
func rulingCitations(doc []byte) map[[2]string]bool {
	cites := map[[2]string]bool{}
	ruling := "(before R1)"
	for _, line := range strings.Split(string(doc), "\n") {
		if m := rulingHeading.FindStringSubmatch(line); m != nil {
			ruling = m[1]
		}
		for _, m := range rulingCitation.FindAllStringSubmatch(line, -1) {
			cites[[2]string{ruling, m[1] + m[2]}] = true
		}
	}
	return cites
}

// definedTestsIn adds every test, benchmark, fuzz and example func a
// _test.go source declares, methods included, as go/parser reads it: a name
// in a comment or a string declares nothing.
func definedTestsIn(name string, src []byte, into map[string]bool) error {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
			if strings.HasPrefix(fn.Name.Name, p) {
				into[fn.Name.Name] = true
			}
		}
	}
	return nil
}

// judgeRulingProofs answers every way the rulings, the tree and the list disagree.
func judgeRulingProofs(cites map[[2]string]bool, defined map[string]bool, listed []rulingProofElsewhere) []string {
	resolves := func(name string) bool {
		if prefix, family := strings.CutSuffix(name, "*"); family {
			for d := range defined {
				if strings.HasPrefix(d, prefix) {
					return true
				}
			}
			return false
		}
		return defined[name]
	}
	elsewhere := map[[2]string]bool{}
	for _, l := range listed {
		elsewhere[[2]string{l.ruling, l.name}] = true
	}
	var problems []string
	for c := range cites {
		if !resolves(c[1]) && !elsewhere[c] {
			problems = append(problems, "the rulings: "+c[0]+" cites "+c[1]+" as proof, and no _test.go in the tree defines it — amend the ruling to the test that holds it now, or list it in test/ruling_proofs_test.go with where its proof is kept")
		}
	}
	for _, l := range listed {
		k := [2]string{l.ruling, l.name}
		switch {
		case !cites[k]:
			problems = append(problems, l.ruling+" "+l.name+" is listed as proof kept elsewhere, and "+l.ruling+" does not cite it: prune test/ruling_proofs_test.go")
		case resolves(l.name):
			problems = append(problems, l.ruling+" "+l.name+" is listed as proof kept elsewhere, and the tree defines it: prune test/ruling_proofs_test.go")
		}
	}
	sort.Strings(problems)
	return problems
}
