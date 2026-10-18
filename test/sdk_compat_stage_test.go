package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sdk_compat_stage_test.go — the sdkcompat stage's own rules, held where
// they can be held without TinyGo or the SDK: the stage never skips, it
// refuses by name what it lacks, a skipped test or a run with no test
// fails it, its exit status is the tests' own, and the gate runs it as a
// stage whose evidence lands beside the others'.
//
// The script runs here against a fixture: a one-commit SDK repository,
// and a go, a TinyGo and an aiisdk that only pretend, each doing just
// enough for the script to reach its tests. What the tests print is the
// case's to choose.

const stageGo = `#!/bin/sh
case "$1" in
version) echo "go version go0.0.0-fixture"; exit 0 ;;
build)
  out=
  while [ $# -gt 0 ]; do [ "$1" = -o ] && out=$2; shift; done
  cp "$FAKE_TOOL" "$out" && chmod +x "$out"; exit $? ;;
test) [ -z "$FAKE_GO_LOG" ] || echo "dir=$PWD os=$AII_OS_DIR args=$*" >> "$FAKE_GO_LOG"; printf '%b' "$FAKE_TEST_OUTPUT"; exit "${FAKE_TEST_EXIT:-0}" ;;
esac
echo "fixture go: $*" >&2
exit 2
`

const stageTool = `#!/bin/sh
field() { sed -n "s/.*\"$1\": \"\([^\"]*\)\".*/\1/p" plugin.json | head -n 1; }
case "$1" in
build) mkdir -p dist && : > "dist/$(field id).wasm" ;;
package) mkdir -p dist && field version > "dist/$(field id)-$(field version).aiiospkg" ;;
devcert) mkdir -p .keys && : > .keys/certifier-root.pub.json && : > .keys/aiii_plugin_publisher_certifier_status.json ;;
sign) ;;
*) echo "fixture tool: $*" >&2; exit 2 ;;
esac
`

type stageFixture struct {
	root, sdk, rev, bin string
}

func newStageFixture(t *testing.T) stageFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stage is a POSIX shell script, run by the gate")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	f := stageFixture{root: t.TempDir(), sdk: t.TempDir(), bin: t.TempDir()}
	for _, name := range []string{"run_sdk_compat.sh", "toolchain.sh"} {
		script, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		write(t, f.root, filepath.Join("test", name), string(script))
	}
	git(t, f.sdk, "init", "-q", "-b", "main")
	write(t, f.sdk, "examples/compat/plugin.json", "{\n  \"id\": \"com.aiii.examples.compat\",\n  \"version\": \"0.1.0\"\n}\n")
	write(t, f.sdk, "examples/interaction-reader/plugin.json", "{\n  \"id\": \"org.example.interaction-reader\",\n  \"version\": \"0.1.0\"\n}\n")
	git(t, f.sdk, "add", "-A")
	git(t, f.sdk, "commit", "-qm", "the kit")
	f.rev = strings.TrimSpace(git(t, f.sdk, "rev-parse", "HEAD"))
	write(t, f.root, "test/sdk-compat.rev", f.rev+"\n")
	for name, body := range map[string]string{"go": stageGo, "tool": stageTool, "tinygo": "#!/bin/sh\necho tinygo version 0.0.0-fixture\n"} {
		write(t, f.bin, name, body)
		if err := os.Chmod(filepath.Join(f.bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// run runs the stage with the tests printing output and exiting code.
func (f stageFixture) run(t *testing.T, output string, code string, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", "test/run_sdk_compat.sh")
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(),
		"AII_GO="+filepath.Join(f.bin, "go"), "TINYGO="+filepath.Join(f.bin, "tinygo"), "AII_SDK_DIR="+f.sdk,
		"FAKE_TOOL="+filepath.Join(f.bin, "tool"), "FAKE_TEST_OUTPUT="+output, "FAKE_TEST_EXIT="+code)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	status := 0
	if ee, ok := err.(*exec.ExitError); ok {
		status = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the stage: %v\n%s", err, out)
	}
	return status, string(out)
}

func TestTheSDKCompatStageComparesTheKitsVectorsWithThisTrees(t *testing.T) {
	f := newStageFixture(t)
	log := filepath.Join(t.TempDir(), "go-test.log")
	if status, out := f.run(t, stagePassed, "0", "FAKE_GO_LOG="+log); status != 0 {
		t.Fatalf("a passing run failed the stage (exit %d):\n%s", status, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "-tags e2e") && strings.Contains(line, "TestVectorsMatchSibling") && strings.Contains(line, "TestEveryVectorIsDeclared") && strings.Contains(line, "./e2e/") {
			found = true
			if !strings.Contains(line, "os="+f.root) && !strings.Contains(line, "/"+filepath.Base(f.root)) {
				t.Errorf("the host is not this tree (%s): %s", f.root, line)
			}
			if strings.Contains(line, "dir="+f.root+" ") {
				t.Errorf("the kit's test ran in this tree, not the extracted kit: %s", line)
			}
		}
	}
	if !found {
		t.Errorf("the stage never ran the kit's vector comparison:\n%s", raw)
	}
}

func TestTheSDKCompatStageHoldsTheVoiceEventNamesToTheKit(t *testing.T) {
	f := newStageFixture(t)
	log := filepath.Join(t.TempDir(), "go-test.log")
	if status, out := f.run(t, stagePassed, "0", "FAKE_GO_LOG="+log); status != 0 {
		t.Fatalf("a passing run failed the stage (exit %d):\n%s", status, out)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "TestTheVoiceEventNamesAreTheKits") && strings.Contains(line, "./internal/pluginhost/") {
			found = true
			if !strings.Contains(line, "dir="+f.root+" ") && !strings.Contains(line, "/"+filepath.Base(f.root)+" ") {
				t.Errorf("the voice event names were checked outside this tree (%s): %s", f.root, line)
			}
		}
	}
	if !found {
		t.Errorf("the stage never held the voice event names to the kit:\n%s", raw)
	}
}

const stagePassed = `=== RUN   TestSDKCompat\n--- PASS: TestSDKCompat (0.01s)\nPASS\nok  \tfixture\t0.1s\n`

// A RUN THAT PASSED PASSES, and says what it built from: the pinned
// commit and the hash of every package the suite installed.
func TestTheSDKCompatStagePassesAPassingRunAndNamesWhatItBuilt(t *testing.T) {
	f := newStageFixture(t)
	status, out := f.run(t, stagePassed, "0")
	if status != 0 {
		t.Fatalf("a passing run failed the stage (exit %d):\n%s", status, out)
	}
	for _, want := range []string{"sdk revision=" + f.rev, `sdk tinygo="tinygo version 0.0.0-fixture"`,
		"sdk package compat-t0.aiiospkg sha256=", "sdk package compat-0.1.0.aiiospkg sha256=", "sdk package compat-0.2.0.aiiospkg sha256=", "sdkcompat exit=0"} {
		if !strings.Contains(out, want) {
			t.Errorf("the stage's record lacks %q:\n%s", want, out)
		}
	}
}

// A SKIP IS NOT A PASS, and neither is a run in which no test ran: the
// suite skips outside the gate, and a -run that matched nothing exits 0.
func TestTheSDKCompatStageFailsASkipAndARunWithNoTest(t *testing.T) {
	f := newStageFixture(t)
	for output, want := range map[string]string{
		`=== RUN   TestSDKCompat\n--- SKIP: TestSDKCompat (0.00s)\nPASS\nok  \tfixture\t0.1s\n`: "a skip is not a pass",
		`testing: warning: no tests to run\nPASS\nok  \tfixture\t0.1s\n`:                        "no test ran",
	} {
		status, out := f.run(t, output, "0")
		if status == 0 || !strings.Contains(out, want) {
			t.Errorf("the stage must fail saying %q, got exit %d:\n%s", want, status, out)
		}
	}
}

// THE EXIT STATUS IS THE TESTS' OWN: captured, not a pipeline's.
func TestTheSDKCompatStageExitsWithTheTestsOwnStatus(t *testing.T) {
	f := newStageFixture(t)
	status, out := f.run(t, `--- FAIL: TestSDKCompat (0.01s)\nFAIL\n`, "3")
	if status != 3 {
		t.Fatalf("a run that exited 3 must end the stage 3, got %d:\n%s", status, out)
	}
}

// NOTHING IS MISSING QUIETLY: a pin that is not a commit, a checkout that
// is not there or lacks the pin, and a TinyGo that is not there each end
// the stage by name, before anything is built or tested.
func TestTheSDKCompatStageRefusesWhatItLacksByName(t *testing.T) {
	for _, c := range []struct {
		name, want string
		setup      func(f stageFixture) []string
	}{
		{"pin", "must hold one full commit id", func(f stageFixture) []string {
			write(t, f.root, "test/sdk-compat.rev", "main\n")
			return nil
		}},
		{"checkout", "no SDK checkout", func(f stageFixture) []string { return []string{"AII_SDK_DIR=" + t.TempDir()} }},
		{"revision", "does not hold the pinned commit", func(f stageFixture) []string {
			write(t, f.root, "test/sdk-compat.rev", strings.Repeat("a", 40)+"\n")
			return nil
		}},
		{"tinygo", "no TinyGo", func(f stageFixture) []string {
			return []string{"TINYGO=" + filepath.Join(f.bin, "missing-tinygo")}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newStageFixture(t)
			status, out := f.run(t, stagePassed, "0", c.setup(f)...)
			if status == 0 || !strings.Contains(out, c.want) {
				t.Fatalf("the stage must refuse saying %q, got exit %d:\n%s", c.want, status, out)
			}
			if strings.Contains(out, "--- PASS") || strings.Contains(out, "sdk package") {
				t.Fatalf("the stage built or tested before refusing:\n%s", out)
			}
		})
	}
}

// THE GATE RUNS IT AS A STAGE: by name, with its log retained and its
// exit recorded in gate.log like every other stage's. A checkout that is
// not there is the cheapest way to watch it run and fail.
func TestTheGateRunsTheSDKCompatStage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gate is a POSIX shell script")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Run(); err != nil {
		t.Skip("an extracted source archive: the gate refuses without a tree, which the gate's own test proves")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no Go toolchain")
		}
	}
	evidence := t.TempDir()
	cmd := exec.Command("sh", "test/gate.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "EVIDENCE_DIR="+evidence, "GATE_ONLY=sdkcompat", "AII_GO="+goBin, "AII_SDK_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "GATE_RESULT_FAIL") {
		t.Fatalf("the stage must fail the gate without its checkout: %v\n%s", err, out)
	}
	gateLog, _ := os.ReadFile(filepath.Join(evidence, "gate.log"))
	if !strings.Contains(string(gateLog), "stage=sdkcompat ") || !strings.Contains(string(gateLog), "exit=1 log=sdkcompat.log") || !strings.Contains(string(gateLog), "verdict=FAIL") {
		t.Fatalf("gate.log must record the sdkcompat stage and its exit:\n%s", gateLog)
	}
	stageLog, _ := os.ReadFile(filepath.Join(evidence, "sdkcompat.log"))
	if !strings.Contains(string(stageLog), "no SDK checkout") {
		t.Fatalf("the stage's log must be retained, saying why: %q", stageLog)
	}
}
