package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pkgUpdates = "github.com/aiii-dot-id/aii-os/internal/updates"

// What `go test -json` prints for a package in which one test passed, one
// skipped with a reason and one skipped without.
const asJSON = `native session=1 interactive=True
go version go1.27.1 windows/amd64
{"Action":"start","Package":"` + pkgUpdates + `"}
{"Action":"run","Package":"` + pkgUpdates + `","Test":"TestRan"}
{"Action":"output","Package":"` + pkgUpdates + `","Test":"TestRan","Output":"=== RUN   TestRan\n"}
{"Action":"output","Package":"` + pkgUpdates + `","Test":"TestRan","Output":"--- PASS: TestRan (0.01s)\n"}
{"Action":"pass","Package":"` + pkgUpdates + `","Test":"TestRan","Elapsed":0.01}
{"Action":"output","Package":"` + pkgUpdates + `","Test":"TestRig","Output":"    rig_test.go:48: the archive here is a zip\n"}
{"Action":"output","Package":"` + pkgUpdates + `","Test":"TestRig","Output":"--- SKIP: TestRig (0.00s)\n"}
{"Action":"skip","Package":"` + pkgUpdates + `","Test":"TestRig","Elapsed":0}
{"Action":"skip","Package":"` + pkgUpdates + `","Test":"TestSilent","Elapsed":0}
{"Action":"pass","Package":"` + pkgUpdates + `","Elapsed":1.2}
NATIVE_EXIT=0
`

// The same run as -v text, with a second package after it whose test has
// the first package's skipped test's name: a result belongs to the package
// whose line follows it.
const asText = `=== RUN   TestRan
--- PASS: TestRan (0.01s)
=== RUN   TestRig
    rig_test.go:48: the archive here is a zip
--- SKIP: TestRig (0.00s)
=== RUN   TestSilent
--- SKIP: TestSilent (0.00s)
PASS
ok  	` + pkgUpdates + `	1.2s
=== RUN   TestRig
=== PAUSE TestRig
=== RUN   TestOther
    other_test.go:9: said by another test
--- PASS: TestOther (0.00s)
=== CONT  TestRig
--- PASS: TestRig (0.00s)
PASS
ok  	github.com/aiii-dot-id/aii-os/internal/other	0.3s
`

func readAll(t *testing.T, logs ...string) *results {
	t.Helper()
	r := newResults()
	for _, log := range logs {
		if err := r.read(strings.NewReader(log)); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestBothFormsOfARunSayTheSameOfIt(t *testing.T) {
	for name, log := range map[string]string{"json": asJSON, "text": asText} {
		r := readAll(t, log)
		if !r.passed[test{pkgUpdates, "TestRan"}] {
			t.Errorf("%s: the test that passed is not read as passed", name)
		}
		if why, ok := r.skipped[test{pkgUpdates, "TestRig"}]; !ok || why != "the archive here is a zip" {
			t.Errorf("%s: the skipped test is read as skipped=%v with the reason %q", name, ok, why)
		}
		if why, ok := r.skipped[test{pkgUpdates, "TestSilent"}]; !ok || why != "" {
			t.Errorf("%s: a test that skipped without a word is read as skipped=%v, reason %q", name, ok, why)
		}
		if r.passed[test{pkgUpdates, "TestRig"}] {
			t.Errorf("%s: a skipped test is read as passed", name)
		}
		if !r.packages[pkgUpdates] {
			t.Errorf("%s: the package is not read as having run", name)
		}
	}
	// In text a result is its package's: the second package's TestRig passed
	// and the first's skipped, and neither is the other's.
	r := readAll(t, asText)
	other := "github.com/aiii-dot-id/aii-os/internal/other"
	if !r.passed[test{other, "TestRig"}] || hasSkip(r, test{other, "TestRig"}) {
		t.Error("the second package's test of the same name took the first's result")
	}
	if why := r.skipped[test{pkgUpdates, "TestRig"}]; why != "the archive here is a zip" {
		t.Errorf("the first package's reason became %q", why)
	}
}

func TestATextLogCutBeforeItsPackageLineIsRefused(t *testing.T) {
	err := newResults().read(strings.NewReader("=== RUN   TestRan\n--- PASS: TestRan (0.01s)\n"))
	if err == nil {
		t.Fatal("results with no package line after them were read as a whole run")
	}
}

func TestAListedTestMustHaveRunAndPassedWhereItIsRequired(t *testing.T) {
	list := []critical{
		{"internal/updates", "TestRan", []string{"linux", "windows"}},
		{"internal/updates", "TestRig", []string{"windows"}},
		{"internal/updates", "TestGone", []string{"windows", "darwin"}},
		{"internal/absent", "TestNeverSeen", []string{"windows"}},
	}
	r := readAll(t, asJSON)

	got := judge(r, list, "windows", true)
	want := []string{
		"RELEASE_CRITICAL_SKIPPED internal/updates TestRig: the archive here is a zip",
		"RELEASE_CRITICAL_MISSING internal/updates TestGone: the package ran and this test did not",
		"RELEASE_CRITICAL_MISSING internal/absent TestNeverSeen: its package did not run",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("on windows, a whole run:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A run of some packages answers for those packages only.
	if got := judge(r, list, "windows", false); len(got) != 2 {
		t.Errorf("a part of a run was held to a package it did not cover, or let one it covered go: %q", got)
	}
	// A platform an entry does not name is not held to it.
	if got := judge(r, list, "linux", true); len(got) != 0 {
		t.Errorf("linux was held to tests listed for other platforms: %q", got)
	}
	if got := judge(r, list, "darwin", true); len(got) != 1 || !strings.Contains(got[0], "TestGone") {
		t.Errorf("darwin: %q", got)
	}
}

func TestTheListIsReadStrictly(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "list.txt")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	list, err := readCritical(write("# a comment\n\ninternal/updates TestRan linux windows  # why\n"))
	if err != nil || len(list) != 1 || list[0].name != "TestRan" || len(list[0].platforms) != 2 {
		t.Fatalf("a well-formed list: %+v, %v", list, err)
	}
	for name, body := range map[string]string{
		"no platform":         "internal/updates TestRan\n",
		"an unknown platform": "internal/updates TestRan linux macos\n",
		"listed twice":        "internal/updates TestRan linux\ninternal/updates TestRan windows\n",
	} {
		if _, err := readCritical(write(body)); err == nil {
			t.Errorf("a list with %s was accepted", name)
		}
	}
}

// The list in the tree names tests that are there. A line whose test was
// renamed, or whose package moved, would be caught by the gate only for the
// platforms the gate runs on; this holds every line, on every platform's run
// of this package.
func TestEveryListedTestIsInItsPackage(t *testing.T) {
	list, err := readCritical(filepath.Join("..", "release_critical.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("the list of release-critical tests is empty")
	}
	for _, c := range list {
		files, _ := filepath.Glob(filepath.Join("..", "..", filepath.FromSlash(c.pkg), "*_test.go"))
		found := false
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(src), "\nfunc "+c.name+"(t *testing.T) {") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s is listed and no test file of that package declares it", c.pkg, c.name)
		}
	}
}

// Both runners read their runs through this program, and a native run reads
// a run of every package.
func TestTheGateAndTheNativeRunReportTheirSkips(t *testing.T) {
	for file, wants := range map[string][]string{
		"run_race_scope.sh": {
			`report_skips "$short_scope" $(awk '{ print $2 }' "$jobs") || return 1`,
			`report_skips rest "$log" || return 1`,
			`start_job "$rs_log" "$go_bin" run ./test/skipreport -platform "$rs_goos" -critical "$script_dir/release_critical.txt" "$@"`,
		},
		"native-run.sh": {
			`run ./test/skipreport -platform "$goos" -critical test/release_critical.txt -whole "$log"`,
			`skips; record "$ver" "$rc" "$start"; exit $rc ;;`,
			`skips; record "$ver" "$rc" "$start"; exit "$rc"`,
			// and a Windows pass counts only from the desktop's session
			`if [ "$host" = windows ] && [ "$rc" = 0 ] && ! tr -d '\r' < "$log" | grep -qE '^native session=[1-9][0-9]* interactive=True'; then`,
		},
	} {
		src, err := os.ReadFile(filepath.Join("..", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(src), want) {
				t.Errorf("test/%s no longer holds %q", file, want)
			}
		}
	}
}
