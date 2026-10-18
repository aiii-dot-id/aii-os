package test

import (
	"go/build"
	"go/build/constraint"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// selectedFiles is exactly the set the compiler would compile for dir
// on this row: go/build evaluates constraints the same way the build
// does, so this is the authority, not a re-implementation of it.
func selectedFiles(t *testing.T, dir, goos, goarch string) []string {
	t.Helper()
	c := build.Default
	c.GOOS, c.GOARCH, c.CgoEnabled = goos, goarch, false
	p, err := c.ImportDir(dir, 0)
	if err != nil {
		t.Fatalf("%s on %s/%s: %v", dir, goos, goarch, err)
	}
	return p.GoFiles
}

func withPrefix(files []string, prefix string) []string {
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// The service installer is the sharp case. Register() shells out —
// systemctl on linux, launchctl on darwin — and neither binary exists
// on a mobile host. hostcap_mobile.go states the law that forbids even
// trying: "this topology cannot spawn subprocesses". The honest answer
// is already written: service_other.go reports "not automated on
// <GOOS> yet — the slot is ready; start it with the command above".
// It was simply unreachable from a mobile row.
func TestServiceInstallerSelectedPerPlatform(t *testing.T) {
	want := map[string]string{
		"linux":   "service_linux.go",
		"darwin":  "service_darwin.go",
		"windows": "service_windows.go",
		"android": "service_other.go",
		"ios":     "service_other.go",
	}
	dir := filepath.Join(repoRoot(t), "internal", "install")
	for _, r := range platformRows {
		got := withPrefix(selectedFiles(t, dir, r.goos, r.goarch), "service_")
		if len(got) != 1 {
			t.Errorf("%s: %d service implementations selected (%v) — exactly one must define Register/Unregister/StartCommand",
				r.goos, len(got), got)
			continue
		}
		if got[0] != want[r.goos] {
			t.Errorf("%s selects %s, want %s — %s cannot run what %s does",
				r.goos, got[0], want[r.goos], r.goos, got[0])
		}
	}
}

// guard:tree
func TestFilenameSuffixNeverLeaksAcrossTheAliasRule(t *testing.T) {
	for _, leak := range aliasLeaks(t, repoRoot(t)) {
		t.Error(leak.path + leak.why)
	}
}

// Every platform-conditional file compiles somewhere the repo ships.
// `!linux && !darwin && !windows` reads like "the rest", and selects
// nothing: android satisfies linux and ios satisfies darwin. Three files
// sat behind that line, never compiled, one of them describing rows it
// never reached.
//
// guard:tree
func TestEveryPlatformFileIsSelectedOnAShippedRow(t *testing.T) {
	for _, dead := range zeroRowFiles(t, repoRoot(t)) {
		t.Error(dead.path + dead.why)
	}
}

// Both rules refuse what they are for, on a planted tree, and pass its
// decided control files.
//
// guard:tree
func TestThePlatformSelectionGuardsRefuseWhatTheyAreFor(t *testing.T) {
	root := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module planted\n")
	write("bare_linux.go", "package p\n")
	write("restated_darwin.go", "//go:build darwin\n\npackage p\n")
	write("mobile_linux.go", "//go:build linux || android\n\npackage p\n")
	write("desktop_darwin.go", "//go:build darwin && !ios\n\npackage p\n")
	write("restated_linux_test.go", "//go:build linux\n\npackage p\n")
	write("rest_other.go", "//go:build !linux && !darwin && !windows\n\npackage p\n")
	write("unshipped_freebsd.go", "package p\n")
	write("acceptance.go", "//go:build acceptance\n\npackage p\n")
	write("mobile_other.go", "//go:build android || ios\n\npackage p\n")
	// Another module's files, in a directory of this tree, are not this
	// module's to decide: neither rule reads them.
	write("copied/go.mod", "module copied\n")
	write("copied/bare_linux.go", "package q\n")
	write("copied/with_cgo.go", "//go:build android && cgo\n\npackage q\n")

	if got, want := names(aliasLeaks(t, root)), "bare_linux.go restated_darwin.go"; got != want {
		t.Errorf("alias rule refused [%s], want [%s]", got, want)
	}
	if got, want := names(zeroRowFiles(t, root)), "rest_other.go unshipped_freebsd.go"; got != want {
		t.Errorf("zero-row rule refused [%s], want [%s]", got, want)
	}
}

// buildModeTags are the tree's tags that are not a platform: a file
// behind one is selected when its build asks for it, on the rows its
// other terms allow.
var buildModeTags = []string{"acceptance"}

// selectionFinding is one refused file and why.
type selectionFinding struct{ path, why string }

// aliasLeaks names every non-test _linux.go or _darwin.go file whose
// mobile row is an alias rather than a decision.
func aliasLeaks(t *testing.T, root string) []selectionFinding {
	t.Helper()
	alsoSelectedOn := map[string]string{"linux": "android", "darwin": "ios"}
	var found []selectionFinding
	walkGo(t, root, func(path, name string) {
		if strings.HasSuffix(name, "_test.go") {
			return
		}
		for host, mobile := range alsoSelectedOn {
			if !strings.HasSuffix(name, "_"+host+".go") {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expr := buildConstraint(t, path, src)
			if tag, ok := expr.(*constraint.TagExpr); expr != nil && !(ok && tag.Tag == host) {
				continue // explicit: the author said what they meant
			}
			found = append(found, selectionFinding{path, " is selected on GOOS=" + mobile + " as well as " + host +
				" — its name is its only constraint, and " + mobile + " satisfies the " + host + " tag. Say which way it goes: " +
				"`//go:build " + host + " || " + mobile + "` if " + mobile + " is meant to compile it, or `//go:build " + host + " && !" + mobile +
				"` and a sibling for " + mobile + " if not."})
		}
	})
	return found
}

// zeroRowFiles names every Go file that no shipped row compiles, with
// the build-mode tags off and on: go/build's MatchFile asks the
// compiler's own question of the name and the constraint together.
func zeroRowFiles(t *testing.T, root string) []selectionFinding {
	t.Helper()
	var found []selectionFinding
	walkGo(t, root, func(path, name string) {
		for _, r := range platformRows {
			for _, tags := range [][]string{nil, buildModeTags} {
				c := build.Default
				c.GOOS, c.GOARCH, c.CgoEnabled, c.BuildTags = r.goos, r.goarch, false, tags
				ok, err := c.MatchFile(filepath.Dir(path), name)
				if err != nil {
					t.Fatal(err)
				}
				if ok {
					return
				}
			}
		}
		found = append(found, selectionFinding{path, " is compiled on none of the rows the repo ships " +
			"(android satisfies linux and ios satisfies darwin) — delete it, or give it the rows it is for"})
	})
	return found
}

// walkGo calls fn for every Go file in the tree, test files included,
// skipping testdata, vendor and dot directories (other agents' worktrees
// nest under .claude), and a directory with a go.mod of its own: that is
// another module, whose files no row builds as this module's and whose
// build lines are its author's (third_party/anet has a file for a cgo
// build, which the shipped Android program is and no row here is).
func walkGo(t *testing.T, root string, fn func(path, name string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (strings.HasPrefix(n, ".") || n == "vendor" || n == "testdata") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); path != root && err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			fn(path, d.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// buildConstraint parses the file's //go:build line from its header —
// the region before the package clause, which is the only place the
// toolchain honours one — or returns nil when it has none.
func buildConstraint(t *testing.T, path string, src []byte) constraint.Expr {
	t.Helper()
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return nil
		}
		if constraint.IsGoBuild(line) {
			expr, err := constraint.Parse(line)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			return expr
		}
	}
	return nil
}

// names reduces findings to their files' base names, sorted.
func names(found []selectionFinding) string {
	var out []string
	for _, f := range found {
		out = append(out, filepath.Base(f.path))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
