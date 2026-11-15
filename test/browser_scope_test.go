package test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// browserDriver stands in for the toolchain in a copy of the runner with a
// browserscope beside it. Its packages: internal/app, whose TestPage and
// TestJourney reach the finder and TestPlain does not; internal/browsertest,
// the finder's own, which nobody shards; internal/other, in rest; every
// other sharded package, one plain test. Every test run is recorded with
// whether the finder was allowed (AII_TEST_BROWSERS), -parallel, its
// package and its tests. A run outside the browser scope holds a file for
// the second it takes; a browser-scope run looks for such a file ten times
// in its own second, and records an overlap when it finds one.
const browserDriver = `#!/bin/sh
case "$1" in
  version) echo 'go version browser-fixture'; exit 0 ;;
  list)
    if [ "$2" = ./... ]; then
      printf 'fixture/internal/app\nfixture/internal/browsertest\nfixture/internal/other\n'
    else
      echo "fixture/${2#./}"
    fi
    exit 0 ;;
  run)
    out=; mode=; pkg=
    while [ $# -gt 0 ]; do
      case "$1" in -out) out=$2; shift ;; -packages) mode=packages ;; -list) mode=list; shift ;; ./internal/*) pkg=$1 ;; esac
      shift
    done
    if [ "$mode" = packages ]; then
      printf 'fixture/internal/app internal/app\nfixture/internal/browsertest internal/browsertest\n' > "$out"
    elif [ "$pkg" = ./internal/app ]; then
      printf 'TestJourney\nTestPage\n' > "$out"
    else
      : > "$out"
    fi
    exit 0 ;;
esac
pkgs=; names=; parallel=default; listing=
for arg in "$@"; do
  case "$arg" in
    -list) listing=1 ;;
    ./internal/*|fixture/*) pkgs="$pkgs ${arg#./}" ;;
    '^('*')$') names=${arg#??}; names=${names%??} ;;
  esac
done
prev=
for arg in "$@"; do [ "$prev" = -parallel ] && parallel=$arg; prev=$arg; done
if [ -n "$listing" ]; then
  case "$pkgs" in
    *internal/app) printf 'TestJourney\nTestPage\nTestPlain\n' ;;
    *internal/browsertest) printf 'TestAllows\nTestFinds\n' ;;
    *) echo TestOne ;;
  esac
  echo "ok  fixture 0s"
  exit 0
fi
if [ "${AII_TEST_BROWSERS-allowed}" = allowed ]; then
  i=0
  while [ $i -lt 10 ]; do
    for f in "$AII_DRIVER_DIR"/running.*; do
      [ -e "$f" ] && echo "$pkgs began while $(cat "$f") ran" >> "$AII_DRIVER_DIR/overlaps"
    done
    sleep 0.1; i=$((i + 1))
  done
else
  echo "$pkgs" > "$AII_DRIVER_DIR/running.$$"
  sleep 1
  rm -f "$AII_DRIVER_DIR/running.$$"
fi
for name in $(printf '%s' "$names" | tr '|' ' '); do
  printf '=== RUN   %s\n--- PASS: %s (0.30s)\n' "$name" "$name"
done
printf 'PASS\n'
for p in $pkgs; do printf 'ok  \tfixture/%s\t0.300s\n' "${p#fixture/}"; done
echo "run browsers=${AII_TEST_BROWSERS-allowed} parallel=$parallel pkgs=$(echo $pkgs | tr ' ' ,) tests=$(printf '%s' "$names" | tr '|' ,)" >> "$AII_DRIVER_DIR/runs"
`

type driverRun struct{ browsers, parallel, pkgs, tests string }

func driverRuns(t *testing.T, dir string) ([]driverRun, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	overlaps, _ := os.ReadFile(filepath.Join(dir, "overlaps"))
	var runs []driverRun
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r driverRun
		for _, f := range strings.Fields(line)[1:] {
			k, v, _ := strings.Cut(f, "=")
			switch k {
			case "browsers":
				r.browsers = v
			case "parallel":
				r.parallel = v
			case "pkgs":
				r.pkgs = v
			case "tests":
				r.tests = v
			}
		}
		runs = append(runs, r)
	}
	return runs, string(overlaps)
}

// THE TESTS THAT DRIVE A BROWSER RUN IN THEIR OWN SCOPE, ONCE, AFTER EVERY
// OTHER SCOPE HAS ENDED, AND NOWHERE ELSE IS THE FINDER ALLOWED. The real
// runner over a stand-in toolchain: internal/app's two tests that reach the
// finder leave its scope, which runs its third with the finder refused;
// rest runs its package with the finder refused and leaves the finder's own
// package, which nobody shards, whole to the browser scope; the browser
// scope runs those, the finder allowed, one test at a time in each package,
// and begins only when every other scope has ended. A runner that starts
// the browser scope beside the others is caught here: the control below
// runs one, and must see the overlap.
func TestTheBrowserScopeTakesWhatReachesTheFinderAndRunsItLast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runner is a POSIX shell script")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	runner, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, script string) (string, []driverRun, string, error) {
		root, state := t.TempDir(), t.TempDir()
		write(t, root, "test/run_race_scope.sh", script)
		if err := os.Chmod(filepath.Join(root, "test", "run_race_scope.sh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "test", "browserscope"), 0o755); err != nil {
			t.Fatal(err)
		}
		driver := filepath.Join(state, "go")
		if err := os.WriteFile(driver, []byte(browserDriver), 0o700); err != nil {
			t.Fatal(err)
		}
		records := t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bash", "test/run_race_scope.sh", "all")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "AII_GO="+driver, "AII_TEST_SHARDS=1", "AII_DRIVER_DIR="+records, "TMPDIR="+state)
		out, err := cmd.CombinedOutput()
		runs, overlaps := driverRuns(t, records)
		return string(out), runs, overlaps, err
	}

	out, runs, overlaps, err := run(t, string(runner))
	if err != nil {
		t.Fatalf("the runner failed over the fixture: %v\n%s", err, out)
	}
	var browser, others []driverRun
	ran := map[string]int{}
	for _, r := range runs {
		for _, name := range strings.Split(r.tests, ",") {
			if name != "" {
				ran[r.pkgs+" "+name]++
			}
		}
		if r.browsers == "allowed" {
			browser = append(browser, r)
		} else {
			others = append(others, r)
			if r.browsers != "elsewhere" {
				t.Errorf("a run outside the browser scope had AII_TEST_BROWSERS=%q: %+v", r.browsers, r)
			}
		}
	}
	// Each test once, in the scope that is its own.
	for key, n := range ran {
		if n != 1 {
			t.Errorf("%s ran %d times", key, n)
		}
	}
	for _, want := range []string{"internal/app TestPlain", "internal/app TestPage", "internal/app TestJourney", "internal/browsertest TestFinds", "internal/browsertest TestAllows"} {
		if ran[want] != 1 {
			t.Errorf("%s did not run once: %v", want, ran)
		}
	}
	var browserTests []string
	for _, r := range browser {
		if r.parallel != "1" {
			t.Errorf("the browser scope ran %s at -parallel %s, not one test at a time", r.pkgs, r.parallel)
		}
		browserTests = append(browserTests, r.pkgs+":"+r.tests)
	}
	sort.Strings(browserTests)
	if want := []string{"internal/app:TestJourney,TestPage", "internal/browsertest:TestAllows,TestFinds"}; strings.Join(browserTests, " ") != strings.Join(want, " ") {
		t.Errorf("the browser scope ran %v, want %v", browserTests, want)
	}
	for _, r := range others {
		if strings.Contains(r.pkgs, "browsertest") {
			t.Errorf("the finder's own package ran outside the browser scope: %+v", r)
		}
	}
	// After every other scope, not beside it.
	if overlaps != "" {
		t.Errorf("the browser scope ran beside another scope:\n%s", overlaps)
	}
	for _, want := range []string{
		"race scope app: PASS (1 exact-once shards; 2 tests that drive a browser are the browser scope's)",
		"race scope browser: PASS (4 tests in 2 packages, 2 processes, one test at a time in each)",
		"race scope all: PASS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the runner did not say %q:\n%s", want, out)
		}
	}

	// The control: the same runner with the browser scope started beside
	// the others. The check above must see it.
	mutant := strings.Replace(string(runner), "\trun_scopes browser || return 1\n", "", 1)
	mutant = strings.Replace(mutant, "\tif ! run_scopes $(sharded_scopes) rest; then", "\tif ! run_scopes $(sharded_scopes) rest browser; then", 1)
	if mutant == string(runner) || !strings.Contains(mutant, "rest browser; then") {
		t.Fatal("the control could not be made: run_all no longer has the lines it changes")
	}
	if _, _, overlaps, err := run(t, mutant); err != nil || overlaps == "" {
		t.Errorf("a runner that starts the browser scope beside the others was not seen to (%v): the order check above proves nothing", err)
	}
}

// THE GATE BEGINS ITS BROWSER STAGE ONLY WHEN EVERY READ-ONLY STAGE HAS
// ENDED. The real gate over stand-in stages: one of the six read-only
// stages (deadcode) outlasts the scope by two seconds, and the browser
// stand-in fails unless that stage's end is already written. Then the
// control: the same gate with its browser stage moved before the settle,
// which must fail here, so the order is held by the gate's own lines and
// not by one run's timing.
func TestTheGateRunsItsBrowserStageAfterItsReadOnlyStages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate is a POSIX shell script")
	}
	for _, command := range []string{"sh", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	gate, err := os.ReadFile("gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, gateScript string) (string, string, error) {
		root, state, evidence := t.TempDir(), t.TempDir(), t.TempDir()
		write(t, root, "test/gate.sh", gateScript)
		for _, name := range []string{"gate-lock.sh", "toolchain.sh", "evidence-seal.sh"} {
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			write(t, root, "test/"+name, string(raw))
		}
		marker := filepath.Join(state, "deadcode-ended")
		stub := "#!/bin/sh\necho stand-in\n"
		for name, body := range map[string]string{
			"test/run_sdk_compat.sh":    stub,
			"test/run_staticcheck.sh":   stub,
			"test/run_crossvet.sh":      stub,
			"test/run_android_shell.sh": stub,
			"test/no_silent_revert.sh":  stub,
			"test/run_deadcode.sh":      "#!/bin/sh\nsleep 2\n: > '" + marker + "'\necho deadcode ended\n",
			"packaging/build-all.sh":    "#!/bin/sh\necho packaged > \"$OUT/build.log\"\n",
			"test/run_race_scope.sh": "#!/bin/sh\ncase \"$1\" in\n" +
				"  parallel) echo 'race scope parallel: PASS' ;;\n" +
				"  weights) echo '# Seconds per top-level test' ;;\n" +
				"  browser) if [ -e '" + marker + "' ]; then echo 'race scope browser: PASS'; else echo 'the browser stage began before deadcode ended'; exit 1; fi ;;\n" +
				"  *) exit 2 ;;\nesac\n",
		} {
			write(t, root, name, body)
			if err := os.Chmod(filepath.Join(root, name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"gate.sh", "gate-lock.sh", "toolchain.sh", "evidence-seal.sh"} {
			if err := os.Chmod(filepath.Join(root, "test", name), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for name, body := range map[string]string{
			"go":    "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'go version barrier-fixture'; fi\nexit 0\n",
			"gofmt": "#!/bin/sh\nexit 0\n",
		} {
			if err := os.WriteFile(filepath.Join(state, name), []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		git(t, root, "init", "-q")
		git(t, root, "add", "-A")
		git(t, root, "commit", "-qm", "fixture")
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "test/gate.sh")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "AII_GO="+filepath.Join(state, "go"), "EVIDENCE_DIR="+evidence,
			"AII_GATE_LOCK="+filepath.Join(state, "gate.lock"), "GATE_ONLY=", "TMPDIR="+state, "AII_OPERATOR_ADDRESS=")
		out, err := cmd.CombinedOutput()
		gateLog, _ := os.ReadFile(filepath.Join(evidence, "gate.log"))
		return string(out), string(gateLog), err
	}

	out, gateLog, err := run(t, string(gate))
	if err != nil || !strings.Contains(out, "GATE_RESULT_PASS") {
		t.Fatalf("the gate over stand-in stages did not pass: %v\n%s\n%s", err, out, gateLog)
	}
	order := []string{}
	for _, line := range strings.Split(gateLog, "\n") {
		if name, ok := strings.CutPrefix(line, "stage="); ok {
			order = append(order, strings.Fields(name)[0])
		}
	}
	got := strings.Join(order, " ")
	if want := "authorship fmt diffcheck revertcheck build vet scope sdkcompat matrix staticcheck crossvet deadcode android browser packaging"; got != want {
		t.Errorf("the gate's stages were recorded as %q, want %q", got, want)
	}

	// The control: the browser stage moved before the settle.
	const settle = "[ -n \"$ONLY\" ] || together_settle || fail\n"
	const browserStage = "stage browser run_browser\n"
	if !strings.Contains(string(gate), settle) || !strings.Contains(string(gate), browserStage) {
		t.Fatal("the control could not be made: gate.sh no longer has the lines it moves")
	}
	mutant := strings.Replace(string(gate), browserStage, "", 1)
	mutant = strings.Replace(mutant, settle, browserStage+settle, 1)
	out, gateLog, err = run(t, mutant)
	if err == nil || !strings.Contains(out, "the browser stage began before deadcode ended") {
		t.Errorf("a gate that starts its browser stage before its read-only stages end was not caught: %v\n%s\n%s", err, out, gateLog)
	}
}

// browserNames are the names and places a browser is started by, which
// only the finder (internal/browsertest) may hold: a test that named one
// itself would start a browser the finder never saw, in whatever scope it
// ran, and the runner's move of the tests that drive one would not hold.
var browserNames = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
	"microsoft-edge", "microsoft-edge-stable", "firefox", "firefox-esr",
	"chrome", "msedge", "chrome.exe", "msedge.exe", "firefox.exe",
}

var browserPlaces = []string{"Google Chrome.app", "Chromium.app", "Firefox.app", "Microsoft Edge.app"}

// browserNamesIn is every string literal of src that names a browser to
// start.
func browserNamesIn(path string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, name := range browserNames {
			if s == name {
				found = append(found, s)
			}
		}
		for _, place := range browserPlaces {
			if strings.Contains(s, place) {
				found = append(found, s)
			}
		}
		return true
	})
	return found, nil
}

// ONLY THE FINDER NAMES A BROWSER TO START. Every test's browser comes
// through browsertest.Find, which refuses in every race scope but the
// browser scope; a Go file anywhere else that names a browser's executable
// or bundle could start one around it.
//
// guard:tree
func TestOnlyTheFinderNamesABrowserToStart(t *testing.T) {
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
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || strings.HasPrefix(rel, "internal/browsertest/") || rel == "test/browser_scope_test.go" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		names, err := browserNamesIn(path, src)
		if err != nil {
			return err
		}
		for _, name := range names {
			t.Errorf("%s names %q: a browser is found by browsertest.Find alone, so that the race runner can say where it runs", rel, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// guard:tree
func TestTheBrowserNameGuardRefusesWhatItIsFor(t *testing.T) {
	for src, want := range map[string]int{
		"package p\nvar a = \"google-chrome\"\n":                              1,
		"package p\nvar a = `/Applications/Firefox.app/Contents/MacOS/x`\n":   1,
		"package p\nvar a = \"blink-chrome\" // google-chrome in a comment\n": 0,
		"package p\nvar a = \"chromedriver logs\"\n":                          0,
	} {
		got, err := browserNamesIn("p.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("%q: found %v, want %d", src, got, want)
		}
	}
}
