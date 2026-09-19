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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestRaceScopeKillsWhatIgnoresItsInterrupt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep finds what a run started through /proc")
	}
	for _, command := range []string{"sh", "bash", "setsid"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("no %s", command)
		}
	}
	for _, tc := range []struct {
		scope    string
		sig      os.Signal
		children int
	}{
		{"rest", syscall.SIGTERM, 2},
		{"rest", os.Interrupt, 2},
		{"all", syscall.SIGTERM, 22},
	} {
		t.Run(tc.scope+"/"+tc.sig.String(), func(t *testing.T) { testRaceScopeUnresponsive(t, tc.scope, tc.sig, tc.children) })
	}
}

func testRaceScopeUnresponsive(t *testing.T, scope string, sig os.Signal, wantChildren int) {
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
  printf 'fake/internal/%s\n' app dashboard identity store pluginhost other
 else
  echo "fake/${2#./}"
 fi
 exit 0
fi
case " $* " in
  *" -list "*) printf 'TestAlpha\nTestBravo\nok  fake/package 0s\n'; exit 0 ;;
esac
trap '' HUP INT TERM
setsid sh -c 'trap "" HUP INT TERM; echo "$$" >> "$AII_RUNNER_CHILDREN"; while :; do sleep 1; done' &
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
		// .
		rest := string(raw[bytes.LastIndexByte(raw, ')')+1:])
		return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
	}

	// .
	// .
	control := exec.Command("sleep", "60")
	control.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = control.Process.Kill(); _, _ = control.Process.Wait() }()

	var output bytes.Buffer
	cmd := exec.Command("sh", "test/run_race_scope.sh", scope)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "AII_GO="+fakeGo, "AII_TEST_SHARDS=2", "AII_RUNNER_CHILDREN="+children, "AII_RACE_SCOPE_GRACE=1")
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

	deadline := time.Now().Add(10 * time.Second)
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
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	select {
	case err := <-exited:
		reaped = true
		if err == nil {
			t.Error("an interrupted run said it passed")
		}
	case <-time.After(6 * time.Second):
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

// .
// .
// .
// .
// .
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
	// .
	const fakeGoScript = `#!/bin/sh
if [ "$1" = list ]; then
 if [ "$2" = ./... ]; then
  printf 'fake/internal/%s\n' app dashboard identity store pluginhost other
 else
  echo "fake/${2#./}"
 fi
 exit 0
fi
setsid sh -c 'trap "" HUP INT TERM; echo "$$" >> "$AII_RUNNER_CHILDREN"; while :; do sleep 1; done' &
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
				deadline := time.Now().Add(2 * time.Second)
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
