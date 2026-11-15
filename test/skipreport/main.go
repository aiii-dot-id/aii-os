package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// A test is named by its package's import path and its own name, a
// subtest's with its parents'.
type test struct{ pkg, name string }

// results is what a run did with each test it started, and the packages
// it finished.
type results struct {
	passed   map[test]bool
	skipped  map[test]string // the reason the test gave, "" when it gave none
	packages map[string]bool
	fails    int // fail events: a test's or a package's, -json or -v
	damaged  int // lines shaped as an event that do not decode as one
}

func newResults() *results {
	return &results{passed: map[test]bool{}, skipped: map[test]string{}, packages: map[string]bool{}}
}

// event is the part of a `go test -json` line this reads.
type event struct {
	Action, Package, Test, Output string
}

// read takes one log. A line that is a -json event is read as one; every
// other line is read as -v text, where a test's lines come before the
// result line of the package they belong to.
func (r *results) read(in io.Reader) error {
	said := map[test]string{} // the last thing a test logged, by -json
	var open test             // -v: the test whose output is being printed
	textSaid := map[string]string{}
	var pendingPass, pendingSkip []string // -v: results waiting for their package's line
	pendingWhy := map[string]string{}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimLeft(line, " \t\r"), "{") {
			var e event
			if json.Unmarshal([]byte(line), &e) != nil || e.Action == "" {
				r.damaged++
			} else {
				t := test{e.Package, e.Test}
				if e.Action == "fail" {
					r.fails++
				}
				switch {
				case e.Test == "":
					if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
						r.packages[e.Package] = true
					}
				case e.Action == "output":
					if why, ok := logLine(e.Output); ok {
						said[t] = why
					}
				case e.Action == "pass":
					r.passed[t] = true
				case e.Action == "skip":
					r.skipped[t] = said[t]
				}
				continue
			}
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 3 && strings.HasPrefix(line, "=== ") && (fields[1] == "RUN" || fields[1] == "CONT" || fields[1] == "NAME"):
			open = test{name: fields[2]}
		case len(fields) >= 3 && fields[0] == "---" && fields[1] == "PASS:":
			pendingPass = append(pendingPass, fields[2])
		case len(fields) >= 3 && fields[0] == "---" && fields[1] == "FAIL:":
			r.fails++
		case len(fields) >= 3 && fields[0] == "---" && fields[1] == "SKIP:":
			pendingSkip = append(pendingSkip, fields[2])
			pendingWhy[fields[2]] = textSaid[fields[2]]
		case len(fields) >= 2 && (strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "FAIL\t")) && strings.Contains(fields[1], "/"):
			pkg := fields[1]
			if strings.HasPrefix(line, "FAIL\t") {
				r.fails++
			}
			r.packages[pkg] = true
			for _, name := range pendingPass {
				r.passed[test{pkg, name}] = true
			}
			for _, name := range pendingSkip {
				r.skipped[test{pkg, name}] = pendingWhy[name]
			}
			pendingPass, pendingSkip = nil, nil
			pendingWhy, textSaid = map[string]string{}, map[string]string{}
			open = test{}
		default:
			if why, ok := logLine(line); ok && open.name != "" {
				textSaid[open.name] = why
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if len(pendingPass)+len(pendingSkip) > 0 {
		return fmt.Errorf("%d test results are followed by no package result: the log is cut short", len(pendingPass)+len(pendingSkip))
	}
	return nil
}

// logLine is the text of a line a test logged ("    file_test.go:12:
// why"), without its indent and its place in the source.
func logLine(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(s, "    ") || strings.HasPrefix(t, "---") || strings.HasPrefix(t, "===") {
		return "", false
	}
	if i := strings.Index(t, ".go:"); i >= 0 {
		if j := strings.Index(t[i:], ": "); j >= 0 {
			return t[i+j+2:], true
		}
	}
	return t, true
}

// A critical entry is one line of the list: a package, a test in it, and
// the platforms on which a release needs that test to have run.
type critical struct {
	pkg, name string
	platforms []string
}

var platforms = []string{"linux", "darwin", "windows"}

func readCritical(path string) ([]critical, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []critical
	seen := map[test]bool{}
	for n, line := range strings.Split(string(raw), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 {
			return nil, fmt.Errorf("%s:%d: want a package, a test and at least one platform", path, n+1)
		}
		for _, p := range f[2:] {
			if !contains(platforms, p) {
				return nil, fmt.Errorf("%s:%d: %q is not a platform (%s)", path, n+1, p, strings.Join(platforms, ", "))
			}
		}
		if seen[test{f[0], f[1]}] {
			return nil, fmt.Errorf("%s:%d: %s %s is listed twice", path, n+1, f[0], f[1])
		}
		seen[test{f[0], f[1]}] = true
		list = append(list, critical{f[0], f[1], f[2:]})
	}
	return list, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// in finds the package a list entry names among those a run finished: the
// list says "internal/updates", a run says the whole import path.
func (r *results) in(short string) (string, bool) {
	for pkg := range r.packages {
		if pkg == short || strings.HasSuffix(pkg, "/"+short) {
			return pkg, true
		}
	}
	return "", false
}

// readElsewhere reads the tests given to another run: "<package>
// <TopLevelTest>" a line, the package as the critical list names it.
func readElsewhere(path string) (map[test]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	given := map[test]bool{}
	for n, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case len(f) != 2 || strings.Contains(f[1], "/"):
			return nil, fmt.Errorf("%s:%d: want a package and a top-level test", path, n+1)
		default:
			given[test{f[0], f[1]}] = true
		}
	}
	return given, nil
}

// judge is every listed test this platform needs that the run did not
// run and pass, one line each, naming which of the two it was, leaving out
// those whose top-level test was given elsewhere.
func judge(r *results, list []critical, platform string, whole bool, elsewhere map[test]bool) []string {
	var wrong []string
	for _, c := range list {
		if !contains(c.platforms, platform) {
			continue
		}
		if top, _, _ := strings.Cut(c.name, "/"); elsewhere[test{c.pkg, top}] {
			continue
		}
		pkg, ran := r.in(c.pkg)
		switch t := (test{pkg, c.name}); {
		case !ran && whole:
			wrong = append(wrong, fmt.Sprintf("RELEASE_CRITICAL_MISSING %s %s: its package did not run", c.pkg, c.name))
		case !ran:
		case hasSkip(r, t):
			wrong = append(wrong, fmt.Sprintf("RELEASE_CRITICAL_SKIPPED %s %s: %s", c.pkg, c.name, orNoReason(r.skipped[t])))
		case !r.passed[t]:
			wrong = append(wrong, fmt.Sprintf("RELEASE_CRITICAL_MISSING %s %s: the package ran and this test did not", c.pkg, c.name))
		}
	}
	return wrong
}

func hasSkip(r *results, t test) bool { _, ok := r.skipped[t]; return ok }

func orNoReason(s string) string {
	if s == "" {
		return "(the test gave no reason)"
	}
	return s
}

func main() {
	platform := flag.String("platform", "", "the platform the run was made on: "+strings.Join(platforms, ", "))
	list := flag.String("critical", "", "the list of release-critical tests")
	whole := flag.Bool("whole", false, "the logs are a run of every package")
	given := flag.String("elsewhere", "", "the top-level tests given to another run, a package and a test a line")
	noFail := flag.Bool("no-fail", false, "the run said it passed: a fail event in the logs refuses it")
	flag.Parse()
	if !contains(platforms, *platform) || flag.NArg() == 0 || (*whole && *given != "") {
		fmt.Fprintln(os.Stderr, "usage: skipreport -platform linux|darwin|windows [-critical FILE] [-whole] [-no-fail] [-elsewhere FILE] LOG...")
		os.Exit(2)
	}
	r := newResults()
	for _, path := range flag.Args() {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "skipreport:", err)
			os.Exit(2)
		}
		err = r.read(f)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipreport: %s: %v\n", path, err)
			os.Exit(2)
		}
	}
	var lines []string
	for t, why := range r.skipped {
		lines = append(lines, fmt.Sprintf("SKIPPED %s %s: %s", t.pkg, t.name, orNoReason(why)))
	}
	sort.Strings(lines)
	fmt.Printf("skipped on %s: %d of the tests that ran in %d packages\n", *platform, len(lines), len(r.packages))
	for _, l := range lines {
		fmt.Println(l)
	}
	failed := *noFail && (r.fails > 0 || r.damaged > 0)
	if *noFail {
		fmt.Printf("fail events: %d; damaged events: %d\n", r.fails, r.damaged)
	}
	if *noFail && r.fails > 0 {
		fmt.Printf("the run said it passed, and its logs hold %d fail event(s)\n", r.fails)
	}
	if *noFail && r.damaged > 0 {
		fmt.Printf("the run said it passed, and its logs hold %d line(s) shaped as events that do not decode, so its events cannot all be read\n", r.damaged)
	}
	if *list == "" {
		if failed {
			os.Exit(1)
		}
		return
	}
	entries, err := readCritical(*list)
	if err != nil {
		fmt.Fprintln(os.Stderr, "skipreport:", err)
		os.Exit(2)
	}
	var elsewhere map[test]bool
	if *given != "" {
		if elsewhere, err = readElsewhere(*given); err != nil {
			fmt.Fprintln(os.Stderr, "skipreport:", err)
			os.Exit(2)
		}
	}
	wrong := judge(r, entries, *platform, *whole, elsewhere)
	for _, w := range wrong {
		fmt.Println(w)
	}
	if len(wrong) > 0 {
		fmt.Printf("release-critical tests: %d did not run and pass on %s\n", len(wrong), *platform)
		os.Exit(1)
	}
	fmt.Printf("release-critical tests: every one required on %s ran and passed\n", *platform)
	if failed {
		os.Exit(1)
	}
}
