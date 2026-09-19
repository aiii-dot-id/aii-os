//go:build windows

// .
// .
// .
// .
// .
package tools

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

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

func TestShellLaunchHidesConsoleWindow(t *testing.T) {
	cmd := exec.Command("powershell.exe")
	prepareTree(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("shell launch does not hide its console window")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatalf("shell launch does not create a hidden console for child inheritance: %#x", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW != 0 {
		t.Fatalf("shell launch must not combine CREATE_NEW_CONSOLE with CREATE_NO_WINDOW: %#x", cmd.SysProcAttr.CreationFlags)
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

func TestShellNestedConsoleCommandDoesNotOpenWindow(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	title := "aii-shell-nested-console-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	resultCh := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := st.Execute(context.Background(), map[string]interface{}{
			"command": `cmd.exe /c "title ` + title + ` & echo cmd child-process test: OK & ver & ping.exe -n 5 127.0.0.1 >nul"`,
		})
		resultCh <- struct {
			result Result
			err    error
		}{result: result, err: err}
	}()

	visible := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hasVisibleWindowTitle(title) {
			visible = true
		}
		select {
		case outcome := <-resultCh:
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			if outcome.result.Error != "" {
				t.Fatalf("nested command failed: %s", outcome.result.Error)
			}
			if !strings.Contains(outcome.result.Output, "cmd child-process test: OK") || !strings.Contains(outcome.result.Output, "Microsoft Windows") {
				t.Fatalf("nested command output was not captured: %q", outcome.result.Output)
			}
			if visible {
				t.Fatalf("nested cmd.exe opened a visible console window")
			}
			return
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.result.Error != "" {
		t.Fatalf("nested command failed: %s", outcome.result.Error)
	}
	if !strings.Contains(outcome.result.Output, "cmd child-process test: OK") || !strings.Contains(outcome.result.Output, "Microsoft Windows") {
		t.Fatalf("nested command output was not captured: %q", outcome.result.Output)
	}
	if visible || hasVisibleWindowTitle(title) {
		t.Fatalf("nested cmd.exe opened a visible console window")
	}
}

var getWindowTextW = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextW")

func hasVisibleWindowTitle(title string) bool {
	found := false
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !windows.IsWindowVisible(windows.HWND(hwnd)) {
			return 1
		}
		var text [256]uint16
		length, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		if syscall.UTF16ToString(text[:length]) == title {
			found = true
			return 0
		}
		return 1
	})
	_ = windows.EnumWindows(callback, nil)
	return found
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
