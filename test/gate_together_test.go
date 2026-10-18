package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// THE GATE RUNS THE STAGES THAT ONLY READ THE TREE TOGETHER, AND READS EVERY
// ONE. The real gate with two stand-in stages, each of which ends well only
// once it has seen the other begin: run one after the other, the first never
// sees the second and fails here. Each is recorded as a stage always was,
// with its exit and its log, in the order the gate lists them, and the record
// seals. One that fails fails the gate, and the other is still waited for and
// recorded before the verdict: a stage left running would write into a record
// already sealed, and a second failure would be hidden behind the first.
func TestGateRunsItsReadOnlyStagesTogetherAndRecordsEachOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate is a POSIX shell script; Windows runs it through Git's sh in the native recipe, not here")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		t.Skip("the gate binds its evidence to a git tree, and this is an extracted archive (TestGateRetainsEvidenceAndRecordsAFailure holds the refusal)")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		if p, err := exec.LookPath("go"); err == nil {
			goBin = p
		} else {
			goBin = "go"
		}
	}
	gate := func(evidence string, env ...string) (string, int) {
		cmd := exec.Command("sh", "test/gate.sh")
		cmd.Dir = root
		cmd.Env = append(append(os.Environ(), "EVIDENCE_DIR="+evidence, "GATE_ONLY=selftest-together", "AII_GO="+goBin,
			"AII_GATE_SELFTEST_DIR="+t.TempDir(), "AII_GATE_SELFTEST_FAILS="), env...)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run: %v\n%s", err, out)
		}
		return string(out), code
	}
	// The lines of a record, in order, cut to "stage=NAME exit=N" and the verdict.
	record := func(dir string) string {
		t.Helper()
		gateLog, err := os.ReadFile(filepath.Join(dir, "gate.log"))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, line := range strings.Split(string(gateLog), "\n") {
			f := strings.Fields(line)
			switch {
			case strings.HasPrefix(line, "stage=") && len(f) == 5:
				got = append(got, f[0]+" "+f[3])
			case strings.HasPrefix(line, "verdict="):
				got = append(got, f[0])
			}
		}
		check := exec.Command("sh", "-c", "if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi")
		check.Dir = dir
		if out, err := check.CombinedOutput(); err != nil {
			t.Fatalf("SHA256SUMS does not verify: %v\n%s", err, out)
		}
		return strings.Join(got, "; ")
	}

	dir := t.TempDir()
	out, code := gate(dir)
	if want := "stage=selftest-left exit=0; stage=selftest-right exit=0; verdict=PASS"; code != 0 || record(dir) != want {
		t.Fatalf("two stages that each wait to see the other begin: exit %d, record %q, want %q\n%s", code, record(dir), want, out)
	}
	for name, want := range map[string]string{"selftest-left.log": "left ran beside right", "selftest-right.log": "right ran beside left"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%s was not kept with what its stage said: %v %q", name, err, b)
		}
	}

	// The first listed fails: the second is still waited for and recorded.
	dir = t.TempDir()
	out, code = gate(dir, "AII_GATE_SELFTEST_FAILS=left")
	if want := "stage=selftest-left exit=3; stage=selftest-right exit=0; verdict=FAIL"; code == 0 || record(dir) != want {
		t.Fatalf("the first of the two failed: exit %d, record %q, want %q\n%s", code, record(dir), want, out)
	}
	if !strings.Contains(out, "=== selftest-left failed; context ===") || !strings.Contains(out, "left ran beside right") {
		t.Errorf("the failed stage's own words were not shown:\n%s", out)
	}
	// The second listed fails: its failure is not lost behind the first's pass.
	dir = t.TempDir()
	out, code = gate(dir, "AII_GATE_SELFTEST_FAILS=right")
	if want := "stage=selftest-left exit=0; stage=selftest-right exit=3; verdict=FAIL"; code == 0 || record(dir) != want {
		t.Fatalf("the second of the two failed: exit %d, record %q, want %q\n%s", code, record(dir), want, out)
	}

	// With no record asked for, what each said is printed when all have
	// ended, as a stage's output always was.
	out, code = gate("")
	for _, want := range []string{"left ran beside right", "right ran beside left", "GATE_RESULT_PASS"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("a gate that keeps no record did not print %q (exit %d):\n%s", want, code, out)
		}
	}
}
