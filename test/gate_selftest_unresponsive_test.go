//go:build !windows

package test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Signal the GATE, not just its runner. A run owns its temporary files until
// every child is gone, and the gate must retain failure evidence after waiting
// for that cleanup. The ordinary success/failure cases pin the reference
// command itself: no discovery, sharding or reduced race instrumentation.
func TestReferenceGateOwnsItsRunThroughCompletion(t *testing.T) {
	for _, mode := range []string{"success", "failure", "INT", "TERM"} {
		t.Run(mode, func(t *testing.T) {
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
			// The fake driver refuses ANY command other than the full reference.
			// Its child is stubborn; on Linux it also leaves the parent's group,
			// like the browser fixtures whose interrupted profiles were leaked.
			const driver = `#!/bin/bash
if [ "$*" = version ]; then echo 'go version reference-fixture'; exit 0; fi
[ "$*" = 'test -v -race -count=1 -timeout 45m ./...' ] || exit 81
set -eu
printf '%s\n' "$PPID" > "$FIXTURE_STATE/runner"
printf '%s\n' "$TMPDIR" > "$FIXTURE_STATE/tmpdir"
touch "$TMPDIR/owned-profile"
printf 'reference-output\n'
if [ "$FIXTURE_MODE" = success ]; then exit 0; fi
if [ "$FIXTURE_MODE" = failure ]; then exit 7; fi
trap '' HUP INT TERM
if [ "$FIXTURE_DETACH" = yes ]; then
 setsid sh -c 'trap "" HUP INT TERM; echo "$$" > "$FIXTURE_STATE/child"; while :; do sleep 1; done' &
else
 sh -c 'trap "" HUP INT TERM; echo "$$" > "$FIXTURE_STATE/child"; while :; do sleep 1; done' &
fi
printf '%s\n' "$$" > "$FIXTURE_STATE/driver"
while [ ! -s "$FIXTURE_STATE/child" ]; do sleep 0.01; done
touch "$FIXTURE_STATE/ready"
while :; do sleep 1; done
`
			fakeGo := filepath.Join(state, "go")
			if err := os.WriteFile(fakeGo, []byte(driver), 0o700); err != nil {
				t.Fatal(err)
			}
			control := exec.Command("sleep", "600")
			if err := control.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = control.Process.Kill(); _ = control.Wait() })
			alive := func(pid string) bool {
				out, err := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
				stat := strings.TrimSpace(string(out))
				return err == nil && stat != "" && !strings.HasPrefix(stat, "Z")
			}
			var output bytes.Buffer
			cmd := exec.Command("sh", "test/gate.sh")
			cmd.Dir = root
			detach := "no"
			if runtime.GOOS == "linux" {
				detach = "yes"
			}
			cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "GATE_ONLY=suite", "EVIDENCE_DIR="+evidence, "TMPDIR="+state,
				"FIXTURE_STATE="+state, "FIXTURE_MODE="+mode, "FIXTURE_DETACH="+detach, "AII_RACE_SCOPE_GRACE=1")
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			reaped := false
			t.Cleanup(func() {
				// Negative controls must not leak their intentionally unowned jobs.
				for _, name := range []string{"driver", "child", "runner"} {
					raw, _ := os.ReadFile(filepath.Join(state, name))
					pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
					if err == nil && pid > 0 && alive(strconv.Itoa(pid)) {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
				if !reaped {
					_ = cmd.Process.Kill()
					<-exited
				}
			})
			if mode == "INT" || mode == "TERM" {
				deadline := time.Now().Add(scopeStopBudget)
				for {
					if _, err := os.Stat(filepath.Join(state, "ready")); err == nil {
						break
					}
					select {
					case err := <-exited:
						reaped = true
						t.Fatalf("gate exited before readiness: %v\n%s", err, output.String())
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("reference driver did not become ready")
					}
					time.Sleep(10 * time.Millisecond)
				}
				sig := syscall.SIGTERM
				if mode == "INT" {
					sig = syscall.SIGINT
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-exited:
				reaped = true
				if (err == nil) != (mode == "success") {
					t.Fatalf("wrong gate outcome: %v\n%s", err, output.String())
				}
			case <-time.After(scopeStopBudget):
				t.Fatal("reference gate did not finish its owned cleanup")
			}
			for _, name := range []string{"driver", "child", "runner"} {
				raw, _ := os.ReadFile(filepath.Join(state, name))
				if pid := strings.TrimSpace(string(raw)); pid != "" && alive(pid) {
					t.Errorf("%s %s outlived the reference gate", name, pid)
				}
			}
			if !alive(strconv.Itoa(control.Process.Pid)) {
				t.Error("unrelated process was killed")
			}
			raw, err := os.ReadFile(filepath.Join(state, "tmpdir"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(strings.TrimSpace(string(raw))); !os.IsNotExist(err) {
				t.Errorf("run-owned temporary directory survived cleanup: %v", err)
			}
			gateLog, _ := os.ReadFile(filepath.Join(evidence, "gate.log"))
			want := "verdict=FAIL"
			code := map[string]string{"success": "0", "failure": "7", "INT": "130", "TERM": "143"}[mode]
			if mode == "success" {
				want = "verdict=PASS"
			} else if strings.Contains(string(gateLog), "verdict=PASS") {
				t.Error("failed reference claimed PASS")
			}
			if !strings.Contains(string(gateLog), want) || !strings.Contains(string(gateLog), "exit="+code+" log=suite.log") {
				t.Errorf("missing verdict or stage status:\n%s", gateLog)
			}
			suiteLog, _ := os.ReadFile(filepath.Join(evidence, "suite.log"))
			if !strings.Contains(string(suiteLog), "reference-output") {
				t.Errorf("lost reference output:\n%s", suiteLog)
			}
			check := exec.Command("sh", "-c", "if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi")
			check.Dir = evidence
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("unsealed reference outcome: %v\n%s", err, out)
			}
		})
	}
}

// Exercise the actual trap functions with deterministic observation seams.
// Real process tests above establish the OS boundary; these distinguish a kill
// request from completed cleanup without relying on the child's scheduling.
func TestReferenceCleanupWaitsAndRetainsUnfinishedFiles(t *testing.T) {
	gate, err := os.ReadFile("gate.sh")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	function := func(raw []byte, name string) string {
		t.Helper()
		_, body, ok := strings.Cut(string(raw), name+"() {")
		if !ok {
			t.Fatalf("missing function %s", name)
		}
		body, _, ok = strings.Cut(body, "\n}")
		if !ok {
			t.Fatalf("unterminated function %s", name)
		}
		return name + "() {" + body + "\n}\n"
	}
	stop := function(gate, "stop_reference")
	cmd := exec.Command("sh", "-c", stop+`
reference_pid=424242
kill() { test "$*" = '-TERM 424242' || exit 81; sent=yes; }
wait() { test "${sent:-}" = yes && test "$*" = 424242 || exit 82; waited=yes; }
stop_reference 130
test "$reference_status" = 130 && test "${waited:-}" = yes
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gate did not forward and wait: %v\n%s", err, out)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, ".aii-race-scope")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-c", function(runner, "cleanup")+`
run_dir=$1
marker=$run_dir/.aii-race-scope
active_pids=
reference_log=
grace_seconds=0
sweep_strays() { return 1; }
cleanup
`, "incomplete-cleanup", dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cleanup incomplete; retained") {
		t.Fatalf("incomplete cleanup reported success or lost its recovery path: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("files removed before process exit was established: %v", err)
	}
}

func TestRaceScopeKillsWhatIgnoresItsInterrupt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep finds what a run started through /proc")
	}
	for _, command := range []string{"sh", "bash", "setsid", "ps"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	for _, tc := range []struct {
		scope    string
		sig      os.Signal
		children int
		retired  bool
	}{
		{"rest", syscall.SIGTERM, 2, false},
		{"rest", os.Interrupt, 2, false},
		{"all", syscall.SIGTERM, shardedScopeChildren(t), false}, // a pair of shards per partitioned package, plus rest; each with a detached descendant
		{"rest", os.Interrupt, 2, true},                          // already gone is also a valid observation
	} {
		t.Run(tc.scope+"/"+tc.sig.String()+"/retired="+strconv.FormatBool(tc.retired), func(t *testing.T) { testRaceScopeUnresponsive(t, tc.scope, tc.sig, tc.children, tc.retired) })
	}
}

func testRaceScopeUnresponsive(t *testing.T, scope string, sig os.Signal, wantChildren int, retired bool) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	children := filepath.Join(dir, "children")
	detached := filepath.Join(dir, "detached")
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
trap '' HUP INT TERM
setsid sh -c 'trap "" HUP INT TERM; echo "$$" > "$AII_RUNNER_DETACHED"; echo "$$" >> "$AII_RUNNER_CHILDREN"; if [ "$AII_CHILD_ALREADY_RETIRED" = true ]; then exit 0; fi; while :; do sleep 1; done' &
echo "$$" >> "$AII_RUNNER_CHILDREN"
while :; do sleep 1; done
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o700); err != nil {
		t.Fatal(err)
	}
	alive := func(pid int) bool {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		// pid (comm) STATE …: a zombie has exited; only its reap is left.
		rest := string(raw[bytes.LastIndexByte(raw, ')')+1:])
		return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
	}

	// A process the run never started: it carries no mark of THIS run and
	// must be left alone whatever the sweep does.
	control := exec.Command("sleep", "60")
	control.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Process.Kill(); _, _ = control.Process.Wait() }()

	var output bytes.Buffer
	cmd := exec.Command("sh", "test/run_race_scope.sh", scope)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "AII_TEST_SHARDS=2", "AII_RUNNER_CHILDREN="+children,
		"AII_RACE_SCOPE_GRACE=1", "AII_RUNNER_DETACHED="+detached, "AII_CHILD_ALREADY_RETIRED="+strconv.FormatBool(retired))
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pids []int
	exited := make(chan error, 1)
	reaped := false
	t.Cleanup(func() {
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !reaped {
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	go func() { exited <- cmd.Wait() }()

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
		t.Fatalf("the scope started %d of %d jobs and descendants", len(pids), wantChildren)
	}
	if retired {
		raw, err := os.ReadFile(detached)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if alive(pid) {
			t.Fatal("fixture child did not retire before interruption")
		}
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	// One second of grace was declared; the kill, the reap and the sweep
	// are what the rest of this bound is for.
	select {
	case err := <-exited:
		reaped = true
		if err == nil {
			t.Error("an interrupted run said it passed")
		}
	case <-time.After(scopeStopBudget):
		t.Fatal("the run was still there six seconds after its interrupt: a job that ignored it held the runner")
	}
	survivors := 0
	for _, pid := range pids {
		if alive(pid) {
			survivors++
		}
	}
	if survivors != 0 {
		t.Errorf("%d of %d marked processes outlived the run", survivors, len(pids))
	}
	if !alive(control.Process.Pid) {
		t.Error("the sweep killed a process the run never started")
	}
}

// The live process test above proves actual retirement and isolation. Delayed
// observation needs its own deterministic fixture: a real child can disappear
// from /proc before enumeration, so a ps wrapper on that child need never run.
// Exercise the runner's ACTUAL functions with a selected synthetic PID, no real
// signals, and exactly four observations before death. Keep the missing-wait
// mutation as a negative control; returning after SIGKILL must still fail.
func TestRaceScopeWaitsForObservedDeath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep uses /proc")
	}
	raw, err := os.ReadFile("run_race_scope.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start, end := strings.Index(source, "\nstill_running() {"), strings.Index(source, "\ncleanup() {")
	if start < 0 || end <= start {
		t.Fatal("runner observation functions were not found")
	}
	functions := source[start:end]
	for _, mutation := range []string{"none", "missing-wait", "missing-kill"} {
		t.Run(mutation, func(t *testing.T) {
			body := functions
			if mutation == "missing-wait" {
				if strings.Count(body, "\tss_ticks=0") != 1 {
					t.Fatal("post-kill wait mutation target changed")
				}
				body = strings.Replace(body, "\tss_ticks=0", "\treturn 0\n\tss_ticks=0", 1)
			} else if mutation == "missing-kill" {
				const kill = "\t\tkill -KILL \"$ss_pid\" 2>/dev/null || true"
				if strings.Count(body, kill) != 1 {
					t.Fatal("kill mutation target changed")
				}
				body = strings.Replace(body, kill, "\t\t:", 1)
			}
			const fixture = `
set -eu
run_dir=$1
grep() { printf '/proc/424242/environ\n'; }
kill() { test "$*" = '-KILL 424242' || return 1; printf 'sent\n' > "$run_dir/killed"; }
ps() {
 test "$*" = '-o stat= -p 424242' && test -f "$run_dir/killed" || return 1
 count=0
 if [ -f "$run_dir/probes" ]; then count=$(cat "$run_dir/probes"); fi
 count=$((count + 1))
 printf '%s\n' "$count" > "$run_dir/probes"
 if [ "$count" -lt 4 ]; then printf 'S\n'; else printf 'Z\n'; fi
}
sleep() { :; }
`
			cmd := exec.Command("bash", "-c", fixture+body+"\nsweep_strays\ntest \"$(cat \"$run_dir/probes\")\" = 4\n", "observation-proof", t.TempDir())
			out, err := cmd.CombinedOutput()
			if mutation != "none" && err == nil {
				t.Fatalf("%s mutation escaped the observation proof", mutation)
			}
			if mutation == "none" && err != nil {
				t.Fatalf("runner returned without observing death: %v\n%s", err, out)
			}
		})
	}
}

func TestRaceScopeSweepsWithAZeroPaddedGrace(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep finds what a run started through /proc")
	}
	for _, command := range []string{"sh", "bash", "setsid"} {
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
	// A job that PASSES and leaves a detached descendant behind.
	const fakeGoScript = `#!/bin/sh
if [ "$1" = list ]; then
 if [ "$2" = ./... ]; then
  printf 'fake/internal/%s\n' app dashboard identity store pluginhost updates other
 else
  echo "fake/${2#./}"
 fi
 exit 0
fi
setsid sh -c 'trap "" HUP INT TERM; echo "$$" >> "$AII_RUNNER_CHILDREN"; while :; do sleep 1; done' &
# This fixture asserts that a STARTED descendant is swept. Do not let the
# passing parent exit before the descendant publishes its PID: the sweep
# may correctly kill it before its first instruction otherwise.
attempt=0
while [ ! -s "$AII_RUNNER_CHILDREN" ]; do
 attempt=$((attempt + 1))
 if [ "$attempt" -ge 500 ]; then echo "descendant startup timed out" >&2; exit 1; fi
 sleep 0.01
done
echo "ok  fake/other 0s"
`
	if err := os.WriteFile(fakeGo, []byte(fakeGoScript), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, grace := range []string{"08", "09", "00", "2", "not-a-number"} {
		t.Run(grace, func(t *testing.T) {
			_ = os.Remove(children)
			var output bytes.Buffer
			cmd := exec.Command("sh", "test/run_race_scope.sh", "rest")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "AII_RUNNER_CHILDREN="+children, "AII_RACE_SCOPE_GRACE="+grace)
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Run(); err != nil {
				t.Fatalf("a passing run failed: %v\n%s", err, output.String())
			}
			if strings.Contains(output.String(), "value too great for base") {
				t.Errorf("the grace was read as octal:\n%s", output.String())
			}
			raw, err := os.ReadFile(children)
			if err != nil {
				t.Fatalf("the job's descendant never started: %v", err)
			}
			for _, field := range strings.Fields(string(raw)) {
				pid, _ := strconv.Atoi(field)
				deadline := time.Now().Add(scopeStopBudget)
				for time.Now().Before(deadline) {
					if st, err := os.ReadFile("/proc/" + field + "/stat"); err != nil || strings.HasPrefix(strings.TrimSpace(string(st[bytes.LastIndexByte(st, ')')+1:])), "Z") {
						pid = 0
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if pid != 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Errorf("grace %q: a marked descendant outlived a run that said it passed", grace)
				}
			}
		})
	}
}
