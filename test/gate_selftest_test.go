package test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGateRetainsEvidenceAndRecordsAFailure(t *testing.T) {
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

	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		if p, err := exec.LookPath("go"); err == nil {
			goBin = p
		} else {
			goBin = "go"
		}
	}
	run := func(only string) (string, int, string) {
		dir := t.TempDir()
		cmd := exec.Command("sh", "test/gate.sh")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "EVIDENCE_DIR="+dir, "GATE_ONLY="+only, "AII_GO="+goBin)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("run %s: %v\n%s", only, err, out)
		}
		return dir, code, string(out)
	}

	func() {
		bare := t.TempDir()
		if err := os.MkdirAll(filepath.Join(bare, "test"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"gate.sh", "gate-lock.sh", "toolchain.sh"} {
			script, err := os.ReadFile(filepath.Join(root, "test", name))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bare, "test", name), script, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		evidence := t.TempDir()
		cmd := exec.Command("sh", "test/gate.sh")
		cmd.Dir = bare
		cmd.Env = append(os.Environ(), "EVIDENCE_DIR="+evidence, "GATE_ONLY=selftest", "AII_GO="+goBin, "GIT_CEILING_DIRECTORIES="+filepath.Dir(bare))
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("THE GATE RAN WITH NO TREE TO BIND ITS EVIDENCE TO:\n%s", out)
		}
		if !strings.Contains(string(out), "cannot identify gate source tree") {
			t.Fatalf("the gate's refusal does not say a tree could not be identified:\n%s", out)
		}
		if left, _ := os.ReadDir(evidence); len(left) != 0 {
			t.Errorf("a refused gate left %d files calling themselves evidence", len(left))
		}
	}()

	if err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run(); err != nil {

		return
	}

	dir, code, out := run("selftest")
	if code != 0 || !strings.Contains(out, "GATE_RESULT_PASS") {
		t.Fatalf("selftest stage: exit %d\n%s", code, out)
	}
	gateLog, err := os.ReadFile(filepath.Join(dir, "gate.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gate commit=", "tree=", "dirty=", "go=\"", "toolchain=", "asked=", "host=", "scope=selftest", "stage=selftest start=", "exit=0 log=selftest.log", "verdict=PASS"} {
		if !strings.Contains(string(gateLog), want) {
			t.Errorf("gate.log missing %q:\n%s", want, gateLog)
		}
	}
	stageLog, err := os.ReadFile(filepath.Join(dir, "selftest.log"))
	if err != nil || !strings.Contains(string(stageLog), "gate-selftest") {
		t.Fatalf("stage log not retained: %v %q", err, stageLog)
	}
	check := exec.Command("sh", "-c", "if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi")
	check.Dir = dir
	if cout, err := check.CombinedOutput(); err != nil {
		t.Fatalf("SHA256SUMS does not verify: %v\n%s", err, cout)
	}

	if err := os.WriteFile(filepath.Join(dir, "selftest.log"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check = exec.Command("sh", "-c", "if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi")
	check.Dir = dir
	if cout, err := check.CombinedOutput(); err == nil || !strings.Contains(string(cout), "selftest.log") {
		t.Fatalf("an altered log must fail the checksum by name: err=%v\n%s", err, cout)
	}

	dir, code, out = run("selftest-fail")
	if code == 0 || !strings.Contains(out, "GATE_RESULT_FAIL") {
		t.Fatalf("a failing stage must fail the gate: exit %d\n%s", code, out)
	}
	gateLog, _ = os.ReadFile(filepath.Join(dir, "gate.log"))
	if !strings.Contains(string(gateLog), "stage=selftest-fail") || !strings.Contains(string(gateLog), "exit=3") || !strings.Contains(string(gateLog), "verdict=FAIL") {
		t.Fatalf("the failure was not recorded with its exit:\n%s", gateLog)
	}

	dir, code, out = run("does-not-exist")
	if code == 0 || strings.Contains(out, "GATE_RESULT_PASS") || !strings.Contains(out, "unknown GATE_ONLY stage") {
		t.Fatalf("unknown stage must fail before execution: exit %d\n%s", code, out)
	}
	gateLog, _ = os.ReadFile(filepath.Join(dir, "gate.log"))
	if strings.Contains(string(gateLog), "stage=") || !strings.Contains(string(gateLog), "verdict=FAIL") {
		t.Fatalf("unknown stage must record failure without executing a stage:\n%s", gateLog)
	}
}

func TestGateStaticcheckKeepsTheSelectedToolchain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX gate fixture")
	}
	root, bin := t.TempDir(), filepath.Join(t.TempDir(), "a toolchain")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gate.sh", "gate-lock.sh", "toolchain.sh", "run_staticcheck.sh", "evidence-seal.sh"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, "test/"+name, string(raw))
		if err := os.Chmod(filepath.Join(root, "test", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selected, replacement, link := filepath.Join(bin, "go"), filepath.Join(bin, "other-go"), filepath.Join(bin, "current")
	const selectedGo = `#!/bin/sh
case "$1" in
 version) rm "$FIXTURE_GO_LINK"; ln -s "$FIXTURE_GO_OTHER" "$FIXTURE_GO_LINK"; echo 'go version go1.27.1 fixture' ;;
 tool) exit 0 ;;
 build) printf '#!/bin/sh\nexec go tool fixture-analyzer\n' > "$3"; chmod 700 "$3" ;;
 *) exit 82 ;;
esac
`
	for name, body := range map[string]string{selected: selectedGo, replacement: "#!/bin/sh\necho wrong-toolchain >&2\nexit 81\n"} {
		if err := os.WriteFile(name, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(selected, link); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Command("readlink", "-f", link).Output(); err != nil {
		t.Skip("the existing toolchain selector needs readlink -f to pin a symlink")
	}
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	git(t, root, "add", "test")
	git(t, root, "commit", "-qm", "fixture")
	evidence := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "test/gate.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_GO="+link, "GATE_ONLY=staticcheck", "EVIDENCE_DIR="+evidence,
		"FIXTURE_GO_LINK="+link, "FIXTURE_GO_OTHER="+replacement)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("staticcheck changed compilers after the header: %v\n%s", err, out)
	}
	if got, err := os.Readlink(link); err != nil || got != replacement {
		t.Fatalf("the fixture never changed the selected link: %q, %v", got, err)
	}
	log, err := os.ReadFile(filepath.Join(evidence, "gate.log"))
	if err != nil || !strings.Contains(string(log), "toolchain="+resolved) || !strings.Contains(string(log), "exit=0 log=staticcheck.log") {
		t.Fatalf("receipt did not bind the successful stage to the selected toolchain: %v\n%s", err, log)
	}
}

const scopeStopBudget = 60 * time.Second

func TestRaceScopeStopsTrackedChildrenOnInterrupt(t *testing.T) {
	for _, scope := range []string{"app", "all"} {
		t.Run(scope, func(t *testing.T) { testRaceScopeInterrupt(t, scope) })
	}
}

func testRaceScopeInterrupt(t *testing.T, scope string) {
	if runtime.GOOS == "windows" {
		t.Skip("the scope runner is a POSIX host/CI script")
	}
	for _, command := range []string{"sh", "bash"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	children := filepath.Join(dir, "children")
	fakeGo := filepath.Join(dir, "go")
	const fakeGoScript = `#!/bin/sh
if [ "$1" = list ]; then
 if [ "$2" = ./... ]; then
  printf 'fake/internal/%s\n' app dashboard identity store pluginhost updates other
 else
  echo "fake/${2#./}"
 fi
 exit 0
fi
case " $* " in
  *" -list "*) printf 'TestAlpha\nTestBravo\nok  fake/package 0s\n'; exit 0 ;;
esac
echo "$$" >> "$AII_RUNNER_CHILDREN"
sh -c 'echo "$$" >> "$AII_RUNNER_CHILDREN"; trap "exit 0" HUP INT TERM; while :; do sleep 1; done' &
child=$!
trap 'kill -TERM "$child" 2>/dev/null; wait "$child"; exit 0' HUP INT TERM
wait "$child"
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o700); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := exec.Command("sh", "test/run_race_scope.sh", scope)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "AII_TEST_SHARDS=2", "AII_RUNNER_CHILDREN="+children)
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	var pids []int
	t.Cleanup(func() {
		if !finished && cmd.Process != nil {
			for _, pid := range pids {
				_ = exec.Command("sh", "-c", `kill -TERM "$1" 2>/dev/null`, "sh", strconv.Itoa(pid)).Run()
			}
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	wantChildren := 4
	if scope == "all" {
		wantChildren = shardedScopeChildren(t)
	}
	deadline := time.Now().Add(scopeStopBudget)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(children)
		pids = pids[:0]
		for _, field := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				t.Fatalf("child PID %q: %v", field, err)
			}
			pids = append(pids, pid)
		}
		if len(pids) == wantChildren {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pids) != wantChildren {
		t.Fatalf("scope did not start all test children and grandchildren:\n%s", output.String())
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		finished = true
		if err == nil {
			t.Fatal("interrupted scope exited successfully")
		}
	case <-time.After(scopeStopBudget):
		t.Fatalf("scope did not exit after interrupt:\n%s", output.String())
	}

	for _, pid := range pids {
		deadline = time.Now().Add(scopeStopBudget)
		for time.Now().Before(deadline) {
			probe := exec.Command("sh", "-c", `kill -0 "$1" 2>/dev/null`, "sh", strconv.Itoa(pid))
			if probe.Run() != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		probe := exec.Command("sh", "-c", `kill -0 "$1" 2>/dev/null`, "sh", strconv.Itoa(pid))
		if probe.Run() == nil {
			t.Errorf("tracked child %d survived runner interruption", pid)
		}
	}
}

func TestRaceScopeIncludesExamplesFuzzSeedsAndDiscoveryErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell runner")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		goBin, err = exec.LookPath("go")
		if err != nil {
			t.Skip("no Go toolchain")
		}
	}
	script, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, dir := range []string{"test", "internal/app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		"go.mod":                 []byte("module fixture\n\ngo 1.24\n"),
		"test/run_race_scope.sh": script,
		"internal/app/fixture_test.go": []byte(`package app
import ("fmt"; "os"; "testing")
func TestRegular(t *testing.T) { fmt.Println("fixture-success-body"); t.Run("child",func(t *testing.T){}) }
func FuzzSeed(f *testing.F) {
 f.Add("seed")
 f.Fuzz(func(t *testing.T,s string) { if os.Getenv("FIXTURE_FAILURE") == "fuzz" { t.Fatal("fuzz seed failure retained") } })
}
func Example() {
 if os.Getenv("FIXTURE_FAILURE") == "example" { fmt.Println("example failure retained") } else { fmt.Println("expected") }
 // Output: expected
}
`),
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), content, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, failure := range []string{"", "example", "fuzz", "discovery"} {
		t.Run("failure="+failure, func(t *testing.T) {
			if failure == "discovery" {
				if err := os.WriteFile(filepath.Join(root, "internal/app/broken.go"), []byte("package app\nvar _ = originalCompilerDiagnostic\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, runner := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, goBin, "test", "-race", "-count=1", "./internal/app")
				if runner {
					cmd = exec.CommandContext(ctx, "sh", "test/run_race_scope.sh", "app")
				}
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "AII_GO="+goBin, "AII_TEST_SHARDS=3", "FIXTURE_FAILURE="+failure)
				out, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("runner=%v timed out: %s", runner, out)
				}
				if failure == "" {
					if err != nil {
						t.Fatalf("runner=%v rejected valid test, example or seed: %v\n%s", runner, err, out)
					}
					if runner {
						for _, name := range []string{"TestRegular", "Example", "FuzzSeed"} {
							if strings.Count(string(out), "--- PASS: "+name+" (") != 1 {
								t.Errorf("missing or repeated timing for %s:\n%s", name, out)
							}
						}
						if strings.Contains(string(out), "fixture-success-body") {
							t.Errorf("timing retention copied the successful test body:\n%s", out)
						}
					}
				} else {
					if err == nil {
						t.Fatalf("runner=%v skipped %s failure:\n%s", runner, failure, out)
					}
					marker := map[string]string{"example": "example failure retained", "fuzz": "fuzz seed failure retained", "discovery": "undefined: originalCompilerDiagnostic"}[failure]
					if !strings.Contains(string(out), marker) {
						t.Fatalf("runner=%v lost original diagnostic %q:\n%s", runner, marker, out)
					}
				}
			}
		})
	}
}

func TestRaceScopeGivesEachShardABudgetThatOnlyCatchesAHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell runner")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		goBin, err = exec.LookPath("go")
		if err != nil {
			t.Skip("no Go toolchain")
		}
	}
	script, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, dir := range []string{"test", "internal/app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	for path, content := range map[string][]byte{
		"go.mod":                 []byte("module fixture\n\ngo 1.24\n"),
		"test/run_race_scope.sh": script,
		"internal/app/fixture_test.go": []byte(`package app
import ("testing"; "time")
func TestSlow(t *testing.T) { time.Sleep(3 * time.Second) }
`),
	} {
		if err := os.WriteFile(filepath.Join(root, path), content, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, budget string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "test/run_race_scope.sh", "app")
		cmd.Dir = root
		env := append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "AII_GO="+goBin, "AII_TEST_SHARDS=1")
		if budget != "" {
			env = append(env, "AII_TEST_SHARD_TIMEOUT="+budget)
		}
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("the runner itself never returned: %s", out)
		}
		return string(out), err
	}

	if out, err := run(t, ""); err != nil {
		t.Fatalf("A SLOW SHARD WAS KILLED BY ITS BUDGET rather than finishing: %v\n%s", err, out)
	}

	out, err := run(t, "1s")
	if err == nil {
		t.Fatalf("a shard given one second to do three ran to completion — the runner is not passing its budget:\n%s", out)
	}
	if !strings.Contains(out, "test timed out") {
		t.Errorf("a shard over its budget does not say what happened:\n%s", out)
	}
}

func shardedScopeChildren(t *testing.T) int {
	t.Helper()
	return (len(shardedPackages(t))*2 + 1) * 2
}

func shardedPackages(t *testing.T) []string {
	t.Helper()
	script, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(script), "sharded_packages() {")
	if !ok {
		t.Fatal("run_race_scope.sh has no sharded_packages")
	}
	body, _, _ := strings.Cut(after, "\n}")
	var packages []string
	for _, field := range strings.Fields(body) {
		if strings.HasPrefix(field, "./internal/") {
			packages = append(packages, field)
		}
	}
	if len(packages) == 0 {
		t.Fatal("run_race_scope.sh partitions no package")
	}
	return packages
}

func TestCIRunsEveryScopeTheRunnerPartitions(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/ci.yml")
	if os.IsNotExist(err) {
		t.Skipf("this tree carries no CI workflow: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	_, row, found := strings.Cut(string(raw), "\n        scope: [")
	row, _, closed := strings.Cut(row, "]")
	if !found || !closed {
		t.Fatal("ci.yml has no race scope row")
	}
	var scopes []string
	for _, pkg := range shardedPackages(t) {
		scopes = append(scopes, filepath.Base(pkg))
	}
	want := strings.Join(append(scopes, "rest"), ", ")
	if row != want {
		t.Errorf("CI's race scopes are [%s]; the runner's are [%s]", row, want)
	}
}

func TestRaceScopeRestKeepsEachTestsSecondsAndItsVerdict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell runner")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		goBin, err = exec.LookPath("go")
		if err != nil {
			t.Skip("no Go toolchain")
		}
	}
	script, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write(t, root, "go.mod", "module fixture\n\ngo 1.24\n")
	write(t, root, "test/run_race_scope.sh", string(script))

	for _, pkg := range shardedPackages(t) {
		write(t, root, pkg+"/doc.go", "package "+filepath.Base(pkg)+"\n")
	}
	write(t, root, "internal/other/other_test.go", `package other
import ("fmt"; "os"; "testing")
func TestKept(t *testing.T) {
 fmt.Println("rest-success-body")
 if os.Getenv("FIXTURE_FAILURE") == "rest" { t.Fatal("rest failure retained") }
}
`)
	for _, failure := range []string{"", "rest"} {
		t.Run("failure="+failure, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "test/run_race_scope.sh", "rest")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "AII_GO="+goBin, "FIXTURE_FAILURE="+failure)
			raw, err := cmd.CombinedOutput()
			out := string(raw)
			if ctx.Err() != nil {
				t.Fatalf("the rest scope never returned: %s", out)
			}
			if failure == "" {
				if err != nil {
					t.Fatalf("a passing package failed the rest scope: %v\n%s", err, out)
				}
				if strings.Count(out, "--- PASS: TestKept (") != 1 {
					t.Errorf("the rest scope did not keep the test's seconds exactly once:\n%s", out)
				}
				if !strings.Contains(out, "ok  \tfixture/internal/other\t") || !strings.Contains(out, "race scope rest: PASS") {
					t.Errorf("a passing rest scope lost a package result or its own verdict:\n%s", out)
				}
				if strings.Contains(out, "rest-success-body") {
					t.Errorf("keeping the timings copied a passing test's own output:\n%s", out)
				}
				return
			}
			if err == nil {
				t.Fatalf("A FAILING PACKAGE PASSED THE REST SCOPE:\n%s", out)
			}
			for _, want := range []string{"rest failure retained", "--- FAIL: TestKept (", "FAIL\tfixture/internal/other\t"} {
				if !strings.Contains(out, want) {
					t.Errorf("a failing rest scope lost %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "race scope rest: PASS") {
				t.Errorf("a failing rest scope said it passed:\n%s", out)
			}
		})
	}
}

func TestGateKeepsTheShardWeightsItsScopeMeasured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate is a POSIX shell script")
	}
	for _, command := range []string{"sh", "bash", "git"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	root, state, evidence := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"gate.sh", "gate-lock.sh", "toolchain.sh", "run_race_scope.sh", "evidence-seal.sh"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		write(t, root, "test/"+name, string(raw))
		if err := os.Chmod(filepath.Join(root, "test", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, "init", "-q")
	git(t, root, "add", "test")
	git(t, root, "commit", "-qm", "fixture")
	const driver = `#!/bin/sh
if [ "$*" = version ]; then echo 'go version weights-fixture'; exit 0; fi
if [ "$1" = list ]; then
 if [ "$2" = ./... ]; then echo fake/internal/other; else echo "fake/${2#./}"; fi
 exit 0
fi
case " $* " in
  *" -list "*) printf 'TestAlpha\nTestBravo\nok  fake 0s\n'; exit 0 ;;
esac
names=
for arg in "$@"; do
 case "$arg" in '^('*')$') names=${arg#??}; names=${names%??} ;; esac
done
for name in $(printf '%s' "$names" | tr '|' ' '); do
 seconds=12.00
 if [ "$name" = TestBravo ]; then seconds=3.00; fi
 printf '=== RUN   %s\n--- PASS: %s (%ss)\n' "$name" "$name" "$seconds"
done
printf 'PASS\nok  \tfake\t1.000s\n'
`
	fakeGo := filepath.Join(state, "go")
	if err := os.WriteFile(fakeGo, []byte(driver), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "test/gate.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "GATE_ONLY=scope", "EVIDENCE_DIR="+evidence, "TMPDIR="+state, "AII_TEST_SHARDS=2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the gate's scope stage failed over the fixture: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(evidence, "shard_weights.txt"))
	if err != nil {
		t.Fatalf("THE GATE KEPT NO WEIGHTS BESIDE ITS SCOPE LOG: %v", err)
	}
	weights := string(raw)
	if !strings.HasPrefix(weights, "# Seconds per top-level test") {
		t.Errorf("the kept weights are not the file the runner deals from:\n%s", weights)
	}
	for _, pkg := range shardedPackages(t) {
		if !strings.Contains(weights, "\n"+filepath.Base(pkg)+" TestAlpha 12.0\n") {
			t.Errorf("%s: the twelve-second test is not weighed:\n%s", pkg, weights)
		}
	}
	if strings.Contains(weights, "TestBravo") {
		t.Errorf("a test of three seconds was given a weight:\n%s", weights)
	}
	if tracked := git(t, root, "status", "--porcelain"); tracked != "" {
		t.Errorf("the gate wrote into its tree:\n%s", tracked)
	}
	sums, _ := os.ReadFile(filepath.Join(evidence, "SHA256SUMS"))
	gateLog, _ := os.ReadFile(filepath.Join(evidence, "gate.log"))
	if !strings.Contains(string(sums), "  shard_weights.txt\n") || !strings.Contains(string(gateLog), "verdict=PASS") {
		t.Errorf("the weights are outside the seal, or the gate did not pass:\n%s\n%s", sums, gateLog)
	}
	check := exec.Command("sh", "-c", "if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi")
	check.Dir = evidence
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("the evidence does not verify: %v\n%s", err, out)
	}
}

func TestTheRunnerBuildsEveryRaceScopeTheSameWay(t *testing.T) {
	script, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	definitions, exclusions, uses := 0, 0, 0
	references := 0
	for _, line := range strings.Split(string(script), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"):
		case line == `start_job "$reference_log" "$go_bin" test -v -race -count=1 -timeout 45m ./...`:
			references++
		case strings.HasPrefix(line, "race_args=("):
			definitions++
			if line != "race_args=(-race)" {
				t.Errorf("full instrumentation is not -race alone: %s", line)
			}
		case strings.HasPrefix(line, "race_args+=("):
			exclusions++
			var flags []string
			for _, library := range []string{"modernc.org/sqlite/lib", "modernc.org/libc", "github.com/trailofbits/go-slh-dsa/..."} {
				flags = append(flags, "-gcflags="+library+"=-race=false")
			}
			want := "race_args+=(" + strings.Join(flags, " ") + ")"
			if line != want {
				t.Errorf("normal mode does not add the three documented exclusions alone:\n got %s\nwant %s", line, want)
			}
		default:
			if strings.Contains(line, `"${race_args[@]}"`) {
				uses++
				line = strings.ReplaceAll(line, `"${race_args[@]}"`, "")
			}
			if strings.Contains(line, " -race ") || strings.HasSuffix(line, " -race") {
				t.Errorf("a run spells -race itself instead of taking race_args: %s", line)
			}
		}
	}
	if definitions != 1 || exclusions != 1 || uses != 3 {
		t.Errorf("race_args has %d definitions, %d normal exclusion lists and %d uses (want one, one and three: discovery, shard, rest)", definitions, exclusions, uses)
	}
	if references != 1 {
		t.Errorf("want one fully instrumented unsharded reference, got %d", references)
	}
}
