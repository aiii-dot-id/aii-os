//go:build windows

// shell_windows_test.go — the Windows proof suite for the shell organ
// (R79). The host gate cross-vets this file (it must compile), but it
// RUNS only on a real Windows host: cross-compile the test binary and
// execute it there (the darwin travel pattern). A green linux suite
// says nothing about this file's claims.
package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The dialect is part of the contract (R79): the identity learns what
// language it writes from the tool surface, never from a failure.
func TestShellDialectIsDeclaredWindows(t *testing.T) {
	st := &ShellTool{}
	if !strings.Contains(st.Description(), "PowerShell") {
		t.Fatalf("windows shell tool does not declare its dialect: %q", st.Description())
	}
}

// The organ runs real PowerShell and returns its output.
func TestShellRunsPowerShell(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `Write-Output ("hello from " + $PSVersionTable.PSVersion.Major)`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" {
		t.Fatalf("powershell did not run: %s", res.Error)
	}
	if !strings.Contains(res.Output, "hello from ") {
		t.Fatalf("no powershell output: %q", res.Output)
	}
}

// Exit status arrives BESIDE the output, not instead of it — the same
// answer shape as unix.
func TestShellExitStatusBesideOutput(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `Write-Output partial; exit 3`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "partial") || !strings.Contains(res.Output, "exit status 3") {
		t.Fatalf("want output beside exit status 3, got: %q (err %q)", res.Output, res.Error)
	}
}

// The scrubbed environment: nothing of the host's own env crosses
// over, and the child's profile IS the sandbox.
func TestShellEnvIsScrubbed(t *testing.T) {
	t.Setenv("AII_CANARY_SECRET", "leak-me")
	sandbox := t.TempDir()
	st := &ShellTool{timeout: 30 * time.Second, sandbox: sandbox}
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `Write-Output ("canary=[" + $env:AII_CANARY_SECRET + "] profile=[" + $env:USERPROFILE + "]")`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "canary=[]") {
		t.Fatalf("the host environment leaked into the shell: %q", res.Output)
	}
	if !strings.Contains(res.Output, "profile=["+sandbox+"]") {
		t.Fatalf("USERPROFILE is not the sandbox: %q", res.Output)
	}
}

// THE CEILING BOUNDS THE TREE on Windows too: the job object dies at
// the deadline and takes the grandchild with it — the P1 regression
// class (an orphan holding the pipe past the ceiling) cannot recur
// here wearing PowerShell clothes.
func TestShellTimeoutKillsTheJobTree(t *testing.T) {
	st := &ShellTool{timeout: 2 * time.Second, sandbox: t.TempDir()}

	start := time.Now()
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": `$p = Start-Process powershell -WindowStyle Hidden -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 300' -PassThru; Write-Output ("child:" + $p.Id); Start-Sleep -Seconds 300`,
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("Execute held for %s — the tree still owns the ceiling", elapsed)
	}
	if !strings.Contains(res.Output, "timed out after") {
		t.Fatalf("expected the timeout report, got: %q (err %q)", res.Output, res.Error)
	}

	for _, line := range strings.Split(res.Output, "\n") {
		if !strings.HasPrefix(line, "child:") {
			continue
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "child:")))
		if perr != nil {
			t.Fatalf("could not parse child pid from %q", line)
		}
		// The grandchild must be DEAD, not just disowned. One grace
		// beat for the kernel to reap the job.
		deadline := time.Now().Add(3 * time.Second)
		for {
			if !processAlive(t, uint32(pid)) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("grandchild %d survived the job termination", pid)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("child pid line missing from output: %q", res.Output)
}

// processAlive probes a pid: openable AND still running. A pid that
// cannot be opened, or whose exit code is no longer STILL_ACTIVE, is
// dead for this test's purpose.
func processAlive(t *testing.T, pid uint32) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == uint32(windows.STATUS_PENDING)
}

// The identity's command must arrive at PowerShell VERBATIM. The R79
// review probed this on the real VM expecting the documented
// "-Command strips quotes" minefield — and measured the OPPOSITE:
// Go's windows argv quoting round-trips a single trailing -Command
// argument exactly, while the "safer" -EncodedCommand alternative
// switches PS 5.1 into CLIXML stream serialization and sprays XML
// progress noise into results. These cases lock the measured-correct
// invocation so nobody "fixes" it into that regression on
// documentation alone. (Meta-lesson: the minefield is real for
// hand-built command lines; it is not real for THIS composition —
// measure your own composition.)
func TestShellCommandArrivesVerbatim(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	cases := []struct{ name, cmd, want string }{
		{"dquote-in-squote", "Write-Output 'a\"b'", "a\"b"},
		{"spaced-dquote", "Write-Output \"x y\"", "x y"},
		{"literal-backslash", "Write-Output 'C:\\dir\\'", "C:\\dir\\"},
		{"nested-var", "$v='ok'; Write-Output \"got $v\"", "got ok"},
	}
	for _, c := range cases {
		res, err := st.Execute(context.Background(), map[string]interface{}{"command": c.cmd})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Error != "" || !strings.Contains(res.Output, c.want) {
			t.Fatalf("%s: want %q in output, got %q (err %q)", c.name, c.want, res.Output, res.Error)
		}
		if strings.Contains(res.Output, "CLIXML") {
			t.Fatalf("%s: CLIXML serialization noise reached the result: %q", c.name, res.Output)
		}
	}
}

// The status bracket must sit flush after real output: PowerShell
// tails \r\n and the old trim stripped only \n, leaving a stray \r
// against our own seam.
func TestShellStatusSeamHasNoStrayCR(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]interface{}{
		"command": "Write-Output tail; exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "tail\n[exit status 7]") {
		t.Fatalf("status seam is not flush (stray CR?): %q", res.Output)
	}
}
