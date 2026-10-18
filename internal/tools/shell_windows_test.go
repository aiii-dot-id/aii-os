//go:build windows

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

func TestShellDialectIsDeclaredWindows(t *testing.T) {
	st := &ShellTool{}
	if !strings.Contains(st.Description(), "PowerShell") {
		t.Fatalf("windows shell tool does not declare its dialect: %q", st.Description())
	}
}

// The organ runs real PowerShell and returns its output.
func TestShellRunsPowerShell(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]any{
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
	res, err := st.Execute(context.Background(), map[string]any{
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
	res, err := st.Execute(context.Background(), map[string]any{
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

func TestShellTimeoutKillsTheJobTree(t *testing.T) {

	const ceiling = 8 * time.Second
	st := &ShellTool{timeout: ceiling, sandbox: t.TempDir()}

	start := time.Now()
	res, err := st.Execute(context.Background(), map[string]any{
		"command": `$p = Start-Process powershell -WindowStyle Hidden -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 300' -PassThru; Write-Output ("child:" + $p.Id); Start-Sleep -Seconds 300`,
	})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > ceiling+8*time.Second {
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

func TestShellCommandArrivesVerbatim(t *testing.T) {
	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	cases := []struct{ name, cmd, want string }{
		{"dquote-in-squote", "Write-Output 'a\"b'", "a\"b"},
		{"spaced-dquote", "Write-Output \"x y\"", "x y"},
		{"literal-backslash", "Write-Output 'C:\\dir\\'", "C:\\dir\\"},
		{"nested-var", "$v='ok'; Write-Output \"got $v\"", "got ok"},
	}
	for _, c := range cases {
		res, err := st.Execute(context.Background(), map[string]any{"command": c.cmd})
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
	res, err := st.Execute(context.Background(), map[string]any{
		"command": "Write-Output tail; exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "tail\n[exit status 7]") {
		t.Fatalf("status seam is not flush (stray CR?): %q", res.Output)
	}
}

// The launch attributes themselves, with no desktop involved: this
// one runs anywhere Windows does, including session 0, and is what
// catches a regression that removes the flags.
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

// The reported fault itself: a shell command whose child is a native
// console program must not put a window on the desktop, and must
// still have its output captured.
func TestShellNestedConsoleCommandDoesNotOpenWindow(t *testing.T) {
	requireWindowProbeWorks(t)

	st := &ShellTool{timeout: 30 * time.Second, sandbox: t.TempDir()}
	title := "aii-shell-nested-console-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	resultCh := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := st.Execute(context.Background(), map[string]any{
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
			checkNestedOutcome(t, outcome.result, outcome.err)
			if visible {
				t.Fatalf("nested cmd.exe opened a visible console window")
			}
			return
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	outcome := <-resultCh
	checkNestedOutcome(t, outcome.result, outcome.err)
	if visible || hasVisibleWindowTitle(title) {
		t.Fatalf("nested cmd.exe opened a visible console window")
	}
}

// The window must stay shut AND the output must still arrive: a fix
// that hid the window by losing the child would pass half of this.
func checkNestedOutcome(t *testing.T, result Result, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("nested command failed: %s", result.Error)
	}
	if !strings.Contains(result.Output, "cmd child-process test: OK") || !strings.Contains(result.Output, "Microsoft Windows") {
		t.Fatalf("nested command output was not captured: %q", result.Output)
	}
}

var getWindowTextW = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextW")

// One callback for all queries: Windows callbacks occupy permanent Go
// runtime slots, so polling must not allocate a capturing callback.
type windowTitleQuery struct {
	title string
	found bool
}

var windowTitleCallback = windows.NewCallback(func(hwnd uintptr, query *windowTitleQuery) uintptr {
	if !windows.IsWindowVisible(windows.HWND(hwnd)) {
		return 1
	}
	var text [256]uint16
	length, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	// TrimSpace, and it is not cosmetic: `cmd.exe /c "title X & ..."`
	// hands the title command everything up to the &, so the console
	// window is really titled "X " and an exact compare never matches.
	// Measured on the VM in session 1 — the window was plainly visible
	// in EnumWindows while this returned false.
	if strings.TrimSpace(syscall.UTF16ToString(text[:length])) == query.title {
		query.found = true
		return 0
	}
	return 1
})

func hasVisibleWindowTitle(title string) bool {
	query := windowTitleQuery{title: title}
	// EnumWindows invokes the callback synchronously and returns before this
	// query leaves scope. Each caller carries its own result through lParam.
	_ = windows.EnumWindows(windowTitleCallback, unsafe.Pointer(&query))
	return query.found
}

// requireWindowProbeWorks proves hasVisibleWindowTitle can see a
// window that IS visible, and skips the caller when it cannot.
//
// Without this the test below is worthless. A process in Windows
// session 0 — anything launched over ssh or by a service — enumerates
// its own window station, never Winsta0/Default where the operator's
// desktop lives, so EnumWindows finds nothing visible no matter what
// the code under test does. The negative assertion would pass, every
// time, proving only that the test could not see. So: open a console
// that is deliberately visible, and require the probe to find it.
func requireWindowProbeWorks(t *testing.T) {
	t.Helper()
	title := "aii-shell-probe-control-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cmd := exec.Command("cmd.exe", "/c", "title "+title+" & ping.exe -n 6 127.0.0.1 >nul")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot open a control window: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if hasVisibleWindowTitle(title) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Skip("this session cannot see windows on the operator desktop (session 0?) — the check below would pass without testing anything")
}
