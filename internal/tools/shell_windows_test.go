//go:build windows

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
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

func TestShellWritesUTF8(t *testing.T) {
	const eAcute, quote = "\u00e9", "\u2019"
	long := " #" + strings.Repeat("a", shellCmdRelayMax)
	for _, c := range []struct{ name, command, want string }{
		{"PowerShell's own output", `Write-Output ("caf" + [char]0x00E9 + " reader" + [char]0x2019 + "s")`, "caf" + eAcute + " reader" + quote + "s"},
		{"a console program it starts", `cmd /c ("echo caf" + [char]0x00E9)`, "caf" + eAcute},
		{"PowerShell's own output, through the first PowerShell", `Write-Output ("caf" + [char]0x00E9 + " reader" + [char]0x2019 + "s")` + long, "caf" + eAcute + " reader" + quote + "s"},
		{"a console program it starts, through the first PowerShell", `cmd /c ("echo caf" + [char]0x00E9)` + long, "caf" + eAcute},
	} {
		st := &ShellTool{timeout: 60 * time.Second, sandbox: t.TempDir()}
		res, err := st.Execute(context.Background(), map[string]any{"command": c.command})
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(res.Output) {
			t.Errorf("%s is not UTF-8: %q", c.name, res.Output)
		}
		if strings.HasPrefix(res.Output, "\ufeff") {
			t.Errorf("%s begins with a byte-order mark: %q", c.name, res.Output)
		}
		if !strings.Contains(res.Output, c.want) {
			t.Errorf("%s: want %q in %q (err %q)", c.name, c.want, res.Output, res.Error)
		}
	}
}

func TestTheIdentitysCommandIsTheOnlyStatementOfItsOwnShell(t *testing.T) {
	rows := []struct {
		name, command string
		want, not     []string
	}{
		{"using comes first", `using namespace System.IO; [Path]::GetFileName('a\b.txt')`, []string{"b.txt"}, []string{"must appear before"}},
		{"param comes first", `param($x = 5) Write-Output $x`, []string{"5"}, []string{"not recognized"}},
		{"an error's position is the command's", `Get-Item nope`, []string{"At line:1 char:1", "+ Get-Item nope", "[exit status 1"}, []string{"OutputEncoding", "ProcessStartInfo", "chcp"}},
		{"a program's failure is the shell's failure", `cmd /c exit 5`, []string{"[exit status 1"}, nil},
		{"a failure that is not last is no failure", `cmd /c exit 5; Write-Output after`, []string{"after"}, []string{"exit status"}},
		{"exit is the command's", `Write-Output partial; exit 3`, []string{"partial", "[exit status 3]"}, nil},
		{"a written error names the command", `Write-Error 'e1'`, []string{"Write-Error 'e1'", " : e1", "WriteErrorException", "[exit status 1"}, []string{"At line", "OutputEncoding"}},
		{"cmd's characters are the command's", `Write-Output 'a!b %PATH% 100% a^b & c | d > e < f "g"'`, []string{`a!b %PATH% 100% a^b & c | d > e < f "g"`}, nil},
		{"a line break is the command's", "Write-Output one\nWrite-Output two", []string{"one\ntwo"}, nil},
		{"an accented letter arrives whole", `Write-Output ("caf" + [char]0x00E9)`, []string{"caf\u00e9"}, nil},
	}
	long := " #" + strings.Repeat("a", shellCmdRelayMax)
	for _, c := range rows {
		for _, tail := range []string{"", long} {
			if tail != "" && strings.Contains(c.command, "\n") {
				continue // a comment would end at the line break
			}
			name := c.name
			if tail != "" {
				name += ", as a command too long for cmd.exe"
			}
			st := &ShellTool{timeout: 60 * time.Second, sandbox: t.TempDir()}
			res, err := st.Execute(context.Background(), map[string]any{"command": c.command + tail})
			if err != nil {
				t.Fatal(err)
			}
			out := res.Output
			got := res.Output
			if tail != "" {
				out = strings.ReplaceAll(out, strings.Repeat("a", 200), "")
				// The console wraps the long line where its width falls
				// (120 in a desktop session), so a wanted phrase may be
				// cut by a line break the command did not write.
				got = strings.ReplaceAll(got, "\n", "")
			}
			if strings.HasPrefix(res.Output, "\ufeff") {
				t.Errorf("%s: begins with a byte-order mark: %.300q", name, out)
			}
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("%s: want %q in %.300q (err %q)", name, want, out, res.Error)
				}
			}
			for _, not := range c.not {
				if strings.Contains(res.Output, not) {
					t.Errorf("%s: %q is in %.300q", name, not, out)
				}
			}
		}
	}
	if start, _ := shellCommand("Write-Output hello", t.TempDir(), true); !strings.EqualFold(filepath.Base(start.bin), "cmd.exe") {
		t.Errorf("a short command is started through %s, want cmd.exe", start.bin)
	}
	if start, _ := shellCommand("Write-Output hello"+long, t.TempDir(), true); strings.EqualFold(filepath.Base(start.bin), "cmd.exe") {
		t.Error("a command too long for cmd.exe is handed to cmd.exe, which would cut it")
	}
}

// shareOf is dir as the machine's own administrative share names it
// (\\localhost\c$\...): the same folder, reached the way a granted root on
// a share is.
func shareOf(t *testing.T, dir string) string {
	t.Helper()
	// The long name: in a desktop session the temporary folder is named by
	// its 8.3 form, which the shell, reached through the share, does not
	// report back.
	if long, err := filepath.EvalSymlinks(dir); err == nil {
		dir = long
	}
	vol := filepath.VolumeName(dir)
	if len(vol) != 2 || vol[1] != ':' {
		t.Fatalf("%s is not on a drive letter", dir)
	}
	share := `\\localhost\` + strings.ToLower(vol[:1]) + `$` + dir[len(vol):]
	if _, err := os.Stat(share); err != nil {
		t.Fatalf("the test's folder is not reachable as a share (%v): run as an account that may open \\\\localhost\\c$", err)
	}
	return share
}

func TestAShellCallInAShareRunsWhereTheWallJudged(t *testing.T) {
	share := shareOf(t, t.TempDir())
	if start, _ := shellCommand("Write-Output hello", share, true); strings.EqualFold(filepath.Base(start.bin), "cmd.exe") {
		t.Errorf("a call in %s is started through cmd.exe, which would run it in the Windows directory", share)
	}
	same := func(a, b string) bool {
		return strings.EqualFold(strings.TrimRight(a, `\`), strings.TrimRight(b, `\`))
	}
	for _, tail := range []string{"", " #" + strings.Repeat("a", shellCmdRelayMax)} {
		st := &ShellTool{timeout: 60 * time.Second, sandbox: share}
		st.wall = func() (shellWall, error) { return shellWall{}, nil }
		res, err := st.Execute(context.Background(), map[string]any{
			"command": `Write-Output ('process=' + [Environment]::CurrentDirectory); Write-Output ('location=' + (Get-Location).ProviderPath)` + tail,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Failed() {
			t.Fatalf("long=%v: the call failed: %s", tail != "", res.Error)
		}
		got := map[string]string{}
		for _, line := range strings.Split(res.Output, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				got[k] = v
			}
		}
		for _, k := range []string{"process", "location"} {
			if !same(got[k], share) {
				t.Errorf("long=%v: the shell's %s directory is %q, not the folder the wall judged, %q (output %.400q)", tail != "", k, got[k], share, res.Output)
			}
		}
	}
}

func TestACommandWindowsCannotStartIsRefusedWithItsLength(t *testing.T) {
	st := &ShellTool{timeout: 60 * time.Second, sandbox: t.TempDir()}
	res, err := st.Execute(context.Background(), map[string]any{"command": "Write-Output x #" + strings.Repeat("a", 33000)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() || !res.Refused || res.ReasonCode != ReasonShellCommandTooLong || res.ExitStatus != nil {
		t.Fatalf("a 33,000-letter command: failed %v, refused %v, reason %q, status %v; output %.200q, error %q",
			res.Failed(), res.Refused, res.ReasonCode, res.ExitStatus, res.Output, res.Error)
	}
	for _, want := range []string{"characters long", "32,767", "write tool"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("the refusal does not say %q: %q", want, res.Error)
		}
	}
}

// The refusal stands exactly at Windows' own limit: the longest line the
// shell can be started with is not refused, one unit more is. A letter
// outside the Basic Multilingual Plane counts two units, as Windows
// counts it.
func TestTheLengthRefusalIsAtWindowsOwnLimit(t *testing.T) {
	dir := t.TempDir()
	lineUnits := func(command string) int {
		return utf16Units(`"` + shellBinary() + `" ` + shellLine(shellArgs(command)))
	}
	base := lineUnits("#")
	fits := "#" + strings.Repeat("a", windowsCommandLineMax-1-base)
	if n := lineUnits(fits); n != windowsCommandLineMax-1 {
		t.Fatalf("the fitting command's line is %d units", n)
	}
	for _, relayed := range []bool{true, false} {
		if _, err := shellCommand(fits, dir, relayed); err != nil {
			t.Errorf("relay %v: a line of %d units (and its null) is refused: %v", relayed, windowsCommandLineMax-1, err)
		}
		if _, err := shellCommand(fits+"a", dir, relayed); err == nil {
			t.Errorf("relay %v: a line of %d units is not refused", relayed, windowsCommandLineMax)
		}
		if _, err := shellCommand(fits[:len(fits)-1]+"\U0001F600", dir, relayed); err == nil {
			t.Errorf("relay %v: a letter of two units at the limit is counted as one", relayed)
		}
	}
}

func TestARelayThatCannotStartTheShellFailsWithItsReason(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell.exe")
	st := &ShellTool{timeout: 60 * time.Second, sandbox: t.TempDir(), extraEnv: []string{shellRelayBinary + `="` + missing + `"`}}
	res, err := st.Execute(context.Background(), map[string]any{"command": "Write-Output x #" + strings.Repeat("a", shellCmdRelayMax)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() || res.Refused || res.ReasonCode != ReasonShellRelayFailed || res.ExitStatus != nil {
		t.Fatalf("failed %v, refused %v, reason %q, status %v; output %.200q, error %q",
			res.Failed(), res.Refused, res.ReasonCode, res.ExitStatus, res.Output, res.Error)
	}
	if !strings.HasPrefix(res.Error, "the host's starter could not start your shell: ") || !strings.HasSuffix(res.Error, ". Nothing of your command ran.") {
		t.Errorf("the failure's words: %q", res.Error)
	}
	if strings.Contains(res.Error+res.Output, shellRelayMarkPrefix) {
		t.Errorf("the relay's mark reached the identity: %q / %q", res.Error, res.Output)
	}
}

func TestConstrainedLanguageIsSaidAsSuch(t *testing.T) {
	probe := exec.Command(shellBinary(), "-NoProfile", "-NonInteractive", "-Command", "$ExecutionContext.SessionState.LanguageMode")
	probe.Env = append(shellEnv(t.TempDir()), "__PSLockdownPolicy=4")
	out, err := probe.CombinedOutput()
	if mode := strings.TrimSpace(string(out)); err != nil || mode != "ConstrainedLanguage" {
		t.Skipf("MEASURED: %s with __PSLockdownPolicy=4 in its environment runs in %q (err %v): it does not take the policy from a process's environment, so Constrained Language is not shown here", shellBinary(), mode, err)
	}
	for _, c := range []struct {
		name, sandbox, command, want string
	}{
		{"a long command", t.TempDir(), "Write-Output x #" + strings.Repeat("a", shellCmdRelayMax), "long commands cannot run"},
		{"a command in a folder on a share", shareOf(t, t.TempDir()), "Write-Output x", "directory that begins"},
	} {
		st := &ShellTool{timeout: 60 * time.Second, sandbox: c.sandbox, extraEnv: []string{"__PSLockdownPolicy=4"}}
		res, err := st.Execute(context.Background(), map[string]any{"command": c.command})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Failed() || res.ReasonCode != ReasonShellRelayFailed || res.ExitStatus != nil {
			t.Fatalf("%s: failed %v, reason %q, status %v; output %.200q, error %q", c.name, res.Failed(), res.ReasonCode, res.ExitStatus, res.Output, res.Error)
		}
		if !strings.HasPrefix(res.Error, "this machine's PowerShell runs in Constrained Language mode") || !strings.Contains(res.Error, c.want) {
			t.Errorf("%s: the words: %q", c.name, res.Error)
		}
	}
}

func TestTheRelaysGiveTheShellTheSameEnvironment(t *testing.T) {
	sandbox := t.TempDir()
	list := func(tail string) map[string]string {
		st := &ShellTool{timeout: 60 * time.Second, sandbox: sandbox}
		res, err := st.Execute(context.Background(), map[string]any{
			"command": `[Environment]::GetEnvironmentVariables().GetEnumerator() | ForEach-Object { 'ENV ' + $_.Key + '=' + $_.Value }` + tail,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Failed() {
			t.Fatalf("long=%v: %s", tail != "", res.Error)
		}
		env := map[string]string{}
		for _, line := range strings.Split(res.Output, "\n") {
			if k, v, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), "ENV "), "="); ok && strings.HasPrefix(strings.TrimSpace(line), "ENV ") {
				env[strings.ToUpper(k)] = v
			}
		}
		return env
	}
	short, long := list(""), list(" #"+strings.Repeat("a", shellCmdRelayMax))
	t.Logf("the shell's environment through cmd.exe: %d names", len(short))
	for k, v := range short {
		t.Logf("  %s=%.120s", k, v)
	}
	for _, env := range []map[string]string{short, long} {
		if _, ok := env[shellRelayMark]; ok {
			t.Errorf("the relay's mark is in the shell's environment")
		}
	}
	for k, v := range short {
		if k == shellRelayArgs {
			continue
		}
		if w, ok := long[k]; !ok || w != v {
			t.Errorf("%s through cmd.exe is %q, through the first PowerShell %q (present %v)", k, v, w, ok)
		}
	}
	for k := range long {
		if _, ok := short[k]; !ok {
			t.Errorf("%s is in the shell's environment through the first PowerShell only", k)
		}
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
//
// Every call starts a relay before the shell (shellCommand): cmd.exe and
// chcp.com, or a first PowerShell and chcp.com. All of them must take the
// one hidden console, so the command runs through each relay: short,
// through cmd.exe, and made long with a comment, through the first
// PowerShell. Over ssh (session 0) this skips; it is run in a desktop
// session.
func TestShellNestedConsoleCommandDoesNotOpenWindow(t *testing.T) {
	requireWindowProbeWorks(t)
	for _, tail := range []string{"", " #" + strings.Repeat("a", shellCmdRelayMax)} {
		nestedConsoleOpensNoWindow(t, tail)
	}
}

func nestedConsoleOpensNoWindow(t *testing.T, tail string) {
	t.Helper()
	st := &ShellTool{timeout: 60 * time.Second, sandbox: t.TempDir()}
	title := "aii-shell-nested-console-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	resultCh := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := st.Execute(context.Background(), map[string]any{
			"command": `cmd.exe /c "title ` + title + ` & echo cmd child-process test: OK & ver & ping.exe -n 5 127.0.0.1 >nul"` + tail,
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

// THE OPERATOR'S SWITCH: tools.shell_windows_utf8 off starts the shell as
// before the relay. The identity's PowerShell is what the tool starts,
// with the same arguments, no relay variables and no mark; it writes in
// the console's own page, so an accented letter does not arrive whole
// (the record keeps it with its bytes marked); and the tool's description
// does not name the variables. On, the description names them. Red with
// the setting not reaching shellCommand: the letter arrives whole and the
// variables are set.
func TestTheSettingOffStartsTheShellAsBefore(t *testing.T) {
	dir := t.TempDir()
	start, err := shellCommand("Write-Output hello", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if start.bin != shellBinary() || strings.Join(start.argv, "\x00") != strings.Join(shellArgs("Write-Output hello"), "\x00") || start.env != nil || start.mark != "" {
		t.Fatalf("off, the start is %q %q env %q mark %q; want %q %q and nothing more", start.bin, start.argv, start.env, start.mark, shellBinary(), shellArgs("Write-Output hello"))
	}
	if long, _ := shellCommand("Write-Output hello #"+strings.Repeat("a", shellCmdRelayMax), shareOf(t, dir), false); long.bin != shellBinary() || long.mark != "" {
		t.Errorf("off, a long command in a folder on a share is started through %q (mark %q)", long.bin, long.mark)
	}

	off := &ShellTool{timeout: 60 * time.Second, sandbox: dir, windowsUTF8: func() bool { return false }}
	res, err := off.Execute(context.Background(), map[string]any{
		"command": `Write-Output ("caf" + [char]0x00E9); Write-Output ('binary=[' + $env:AII_SHELL_BINARY + ']')`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("off, the call failed: %s", res.Error)
	}
	if strings.Contains(res.Output, "caf\u00e9") {
		t.Errorf("off, the letter arrived whole, as only the relay makes it: %q", res.Output)
	}
	if !strings.Contains(res.Output, "binary=[]") {
		t.Errorf("off, the relay's variables are in the shell's environment: %q", res.Output)
	}
	if strings.Contains(off.Description(), shellRelayBinary) {
		t.Errorf("off, the description names the relay's variables: %q", off.Description())
	}
	on := &ShellTool{windowsUTF8: func() bool { return true }}
	if !strings.Contains(on.Description(), shellRelayDescription) || shellRelayDescription == "" {
		t.Errorf("on, the description does not name the relay's variables: %q", on.Description())
	}
}
