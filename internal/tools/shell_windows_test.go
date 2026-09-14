//go:build windows

// .
// .
// .
// .
// .
package tools

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// .
// .
func TestShellDialectIsDeclaredWindows(t *testing.T) {
	st := &ShellTool{}
	if !strings.Contains(st.Description(), "PowerShell") {
		t.Fatalf("windows shell tool does not declare its dialect: %q", st.Description())
	}
}

// .
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

// .
// .
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

// .
// .
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

// .
// .
// .
// .
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
		// .
		// .
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

// .
// .
// .
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
// .
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

// .
// .
// .
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
