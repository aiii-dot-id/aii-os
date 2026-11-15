package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	fixtureModule = "example.com/m"
	fixtureFinder = fixtureModule + finderSuffix
)

// write lays files out under dir.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The rig of a package whose tests drive a browser, and the tests that
// reach it each way this reads, beside tests that do not.
var packageA = map[string]string{
	"rig_test.go": `package a

import (
	"testing"

	bt "example.com/m/internal/browsertest"
)

func needChrome(t *testing.T) string { p, _ := bt.Find(bt.Chrome); return p }

func allowed() bool { return bt.Allowed(bt.Chrome) }

type page struct{}

func (p *page) Run(js string) { needChrome(nil) }

type driver struct{}

func (d driver) Serve() { needChrome(nil) }

var launchers = map[string]func(*testing.T) string{"chrome": needChrome}
`,
	"export_test.go": `package a

import "testing"

func Exported(t *testing.T) { needChrome(t) }
`,
	"a_test.go": `package a

import "testing"

func TestDirect(t *testing.T) { needChrome(t) }

func TestThroughAVariable(t *testing.T) { launchers["chrome"](t) }

func TestThroughAMethodsName(t *testing.T) { var p *page; p.Run("x") }

func TestNamesATypeWhoseMethodReachesIt(t *testing.T) { _ = driver{} }

func TestRunsSubtestsAndNothingElse(t *testing.T) { t.Run("x", func(t *testing.T) { t.Helper() }) }

func TestAsksOnlyWhetherAnEngineIsAllowed(t *testing.T) { _ = allowed() }

func TestPlain(t *testing.T) {}

func ExampleBrowser() {
	needChrome(nil)
	// Output:
}
`,
	"ext_test.go": `package a_test

import (
	"testing"

	"example.com/m/a"
)

func TestFromTheExternalPackage(t *testing.T) { a.Exported(t) }

func TestExternalPlain(t *testing.T) {}
`,
}

var packageAListed = []string{
	"TestDirect", "TestThroughAVariable", "TestThroughAMethodsName", "TestNamesATypeWhoseMethodReachesIt",
	"TestRunsSubtestsAndNothingElse", "TestAsksOnlyWhetherAnEngineIsAllowed", "TestPlain", "ExampleBrowser",
	"TestFromTheExternalPackage", "TestExternalPlain",
}

func fixturePkg(dir, importPath string) pkg {
	p := pkg{ImportPath: importPath, Dir: dir}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		switch {
		case !strings.HasSuffix(name, "_test.go"):
			p.GoFiles = append(p.GoFiles, name)
		case strings.HasPrefix(string(raw), "package "+filepath.Base(dir)+"_test"):
			p.XTestGoFiles = append(p.XTestGoFiles, name)
		default:
			p.TestGoFiles = append(p.TestGoFiles, name)
		}
	}
	return p
}

func reached(t *testing.T, p pkg, names []string) []string {
	t.Helper()
	reach, declared, err := read(p, fixtureFinder)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range names {
		if !declared[n] {
			t.Fatalf("%s is not declared", n)
		}
		if reach(n) {
			got = append(got, n)
		}
	}
	sort.Strings(got)
	return got
}

// A TEST DRIVES A BROWSER WHEN IT REACHES THE FINDER, BY ANY ROAD THIS
// READS, AND NOT OTHERWISE. Directly; through a variable holding a
// function that does; through a method called by its name; by naming a
// type whose method does (a value handed to code elsewhere is called back
// through an interface); from the external test package through a name
// the internal one exports. A test that only runs subtests on its
// *testing.T does not reach a method of the rig that shares the name Run,
// and asking whether an engine is allowed is not looking for one.
func TestATestDrivesABrowserWhenItReachesTheFinder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a")
	write(t, dir, packageA)
	got := reached(t, fixturePkg(dir, fixtureModule+"/a"), packageAListed)
	want := []string{"ExampleBrowser", "TestDirect", "TestFromTheExternalPackage", "TestNamesATypeWhoseMethodReachesIt", "TestThroughAMethodsName", "TestThroughAVariable"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the tests that reach the finder are %v, want %v", got, want)
	}
}

// A TESTMAIN OR AN INIT THAT REACHES THE FINDER COUNTS EVERY TEST: what it
// does, it does before each of them.
func TestATestMainThatReachesTheFinderCountsEveryTest(t *testing.T) {
	for name, body := range map[string]string{
		"TestMain": "func TestMain(m *testing.M) { needChrome(nil); m.Run() }",
		"init":     "func init() { _ = launchers }",
	} {
		dir := filepath.Join(t.TempDir(), "a")
		files := map[string]string{}
		for k, v := range packageA {
			files[k] = v
		}
		files["main_test.go"] = "package a\n\nimport \"testing\"\n\n" + body + "\n"
		write(t, dir, files)
		if got := reached(t, fixturePkg(dir, fixtureModule+"/a"), packageAListed); len(got) != len(packageAListed) {
			t.Errorf("%s reaches the finder and %d of %d tests were counted: %v", name, len(got), len(packageAListed), got)
		}
	}
	// A TestMain that does not reach it counts nothing by itself: m.Run is a
	// method of testing.M, not the rig's Run.
	dir := filepath.Join(t.TempDir(), "a")
	files := map[string]string{}
	for k, v := range packageA {
		files[k] = v
	}
	files["main_test.go"] = "package a\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"
	write(t, dir, files)
	if got := reached(t, fixturePkg(dir, fixtureModule+"/a"), packageAListed); len(got) != 6 {
		t.Errorf("a TestMain that only runs the tests counted %v", got)
	}
}

// THE FINDER'S OWN PACKAGE: a test that calls Find drives the search for a
// browser, and is counted; one that asks only whether an engine is allowed
// is not.
func TestTheFindersOwnTestsAreReadToo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "browsertest")
	write(t, dir, map[string]string{
		"browsertest.go": `package browsertest

const Chrome = "blink-chrome"

func Find(engine string) (string, []string) { return look(engine), nil }

func look(engine string) string { return "" }

func Allowed(engine string) bool { return true }
`,
		"browsertest_test.go": `package browsertest

import "testing"

func TestFinds(t *testing.T) { Find(Chrome) }

func TestAllows(t *testing.T) { Allowed(Chrome) }
`,
	})
	if got := reached(t, fixturePkg(dir, fixtureFinder), []string{"TestFinds", "TestAllows"}); !reflect.DeepEqual(got, []string{"TestFinds"}) {
		t.Errorf("in the finder's own package, %v were counted; want TestFinds alone", got)
	}
}

// WHAT CANNOT BE READ IS REFUSED, WITH THE REASON. A dot import of the
// finder hides its uses; a package that imports it outside its tests
// carries it to tests nothing here reads.
func TestWhatCannotBeReadIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a")
	write(t, dir, map[string]string{"a_test.go": `package a

import (
	"testing"

	. "example.com/m/internal/browsertest"
)

func TestDot(t *testing.T) { Find(Chrome) }
`})
	if _, _, err := read(fixturePkg(dir, fixtureModule+"/a"), fixtureFinder); err == nil || !strings.Contains(err.Error(), "dot-imports") {
		t.Errorf("a dot import of the finder was read: %v", err)
	}
	p := pkg{ImportPath: fixtureModule + "/helper", Imports: []string{"testing", fixtureFinder}}
	if err := outsideTests(p, fixtureFinder); err == nil || !strings.Contains(err.Error(), "outside its tests") {
		t.Errorf("a package importing the finder outside its tests was let through: %v", err)
	}
	if err := outsideTests(pkg{ImportPath: fixtureFinder, Imports: []string{"os"}}, fixtureFinder); err != nil {
		t.Errorf("the finder's own package was refused: %v", err)
	}
}

// THE TWO MODES THROUGH THE TOOLCHAIN, ON A MODULE OF THEIR OWN: which
// packages can reach the finder, and which of one package's listed tests
// do; a list that names a test no file declares is refused, because then
// this read other files than the build did.
func TestTheModesAnswerThroughTheToolchain(t *testing.T) {
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no Go toolchain")
		}
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module " + fixtureModule + "\n\ngo 1.22\n",
		"internal/browsertest/browsertest.go": `package browsertest

const Chrome = "blink-chrome"

func Find(engine string) (string, []string) { return "", nil }

func Allowed(engine string) bool { return true }
`,
		"b/b.go":      "package b\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	}
	for k, v := range packageA {
		files["a/"+k] = v
	}
	files["a/a.go"] = "package a\n"
	write(t, root, files)
	t.Chdir(root)
	t.Setenv("GOFLAGS", "-mod=mod")
	if out, err := exec.Command(goBin, "list", "-race", "./...").CombinedOutput(); err != nil {
		t.Skipf("this toolchain cannot list a race build here: %v\n%s", err, out)
	}

	got, err := finderPackages(goBin, []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fixtureModule + "/a a\n", fixtureFinder + " internal/browsertest\n"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the packages that can reach the finder are %q, want %q", got, want)
	}

	list := filepath.Join(t.TempDir(), "list")
	write(t, filepath.Dir(list), map[string]string{"list": strings.Join(packageAListed, "\n") + "\nok  \texample.com/m/a\t0.01s\n"})
	tests, err := browserTests(goBin, list, "./a")
	if err != nil {
		t.Fatal(err)
	}
	if want := "ExampleBrowser\nTestDirect\nTestFromTheExternalPackage\nTestNamesATypeWhoseMethodReachesIt\nTestThroughAMethodsName\nTestThroughAVariable\n"; strings.Join(tests, "") != want {
		t.Errorf("-list answered %q, want %q", strings.Join(tests, ""), want)
	}

	write(t, filepath.Dir(list), map[string]string{"list": "TestDirect\nTestNobodyDeclared\n"})
	if _, err := browserTests(goBin, list, "./a"); err == nil || !strings.Contains(err.Error(), "TestNobodyDeclared") {
		t.Errorf("a listed test no file declares was let through: %v", err)
	}

	write(t, root, map[string]string{"b/b.go": "package b\n\nimport _ \"" + fixtureFinder + "\"\n"})
	if _, err := finderPackages(goBin, []string{"./..."}); err == nil || !strings.Contains(err.Error(), fixtureModule+"/b imports") {
		t.Errorf("a package importing the finder outside its tests was let through: %v", err)
	}
}
