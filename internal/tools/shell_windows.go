//go:build windows

package tools

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const shellDialect = "PowerShell"

const shellRelayDescription = " Your shell's environment also holds " + shellRelayBinary + " and " + shellRelayArgs + ": the host's record of how it started your shell."

func shellBinary() string {

	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		return pwsh
	}
	return filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
}

func shellArgs(command string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", command}
}

func shellCommand(command, dir string, utf8 bool) (shellStart, error) {
	ps := shellBinary()
	line := shellLine(shellArgs(command))

	if n := utf16Units(`"` + ps + `" ` + line); n+1 > windowsCommandLineMax {
		return shellStart{}, errors.New(shellCommandTooLong(n))
	}
	if !utf8 {
		return shellStart{bin: ps, argv: shellArgs(command)}, nil
	}
	sr := os.Getenv("SystemRoot")
	env := []string{
		shellRelayBinary + `="` + ps + `"`, shellRelayArgs + "=" + line,

		"COMSPEC=" + filepath.Join(sr, `System32\cmd.exe`), "PROMPT=$P$G",
	}
	if !beginsWithTwoSeparators(dir) && len(ps)+len(line) <= shellCmdRelayMax {
		return shellStart{bin: filepath.Join(sr, `System32\cmd.exe`), argv: []string{"/d", "/v:on", "/s", "/c", shellCmdRelay}, env: env}, nil
	}
	mark := shellRelayMarkPrefix + rand.Text()
	return shellStart{
		bin: ps, argv: []string{"-NoProfile", "-NonInteractive", "-Command", shellPSRelay},
		env:  append(env, shellRelayMark+"="+mark),
		mark: mark, constrained: shellConstrainedLanguage(beginsWithTwoSeparators(dir)),
	}, nil
}

func shellLine(args []string) string {
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = syscall.EscapeArg(a)
	}
	return strings.Join(escaped, " ")
}

func beginsWithTwoSeparators(dir string) bool {
	return len(dir) >= 2 && os.IsPathSeparator(dir[0]) && os.IsPathSeparator(dir[1])
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

const windowsCommandLineMax = 32767

func shellCommandTooLong(units int) string {
	return "this command is " + groupedThousands(units) + " characters long as the shell is started with it, and Windows starts a program with at most 32,767: put the program in a file with the write tool and run the file"
}

func groupedThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func shellConstrainedLanguage(share bool) string {
	const held = "this machine's PowerShell runs in Constrained Language mode, where the host's starter for "
	if share {
		return held + "commands run in a directory that begins \\\\ (a network folder) cannot run: such commands cannot be run here; run it with a cwd on this machine's own drive"
	}
	return held + "long commands cannot run: commands over about " + groupedThousands(shellCmdRelayMax) + " characters cannot be run here; put the program in a file with the write tool and run the file"
}

const (
	shellRelayBinary     = "AII_SHELL_BINARY"
	shellRelayArgs       = "AII_SHELL_ARGUMENTS"
	shellRelayMark       = "AII_SHELL_RELAY_MARK"
	shellRelayMarkPrefix = "aii-relay-"
)

const shellCmdRelayMax = 7000

const shellCmdRelay = `%SystemRoot%\System32\chcp.com 65001 >nul & !` + shellRelayBinary + `! !` + shellRelayArgs + `!`

const shellPSRelay = `$n=$env:` + shellRelayMark + `;Remove-Item Env:` + shellRelayMark + ` -ErrorAction Ignore;` +
	`if($ExecutionContext.SessionState.LanguageMode -ne 'FullLanguage'){Write-Output ($n+' ` + shellRelayLanguage + `');exit 1};` +
	`try{& ($env:SystemRoot+'\System32\chcp.com') 65001 > $null;` +
	`$i=New-Object Diagnostics.ProcessStartInfo;$i.FileName=$env:` + shellRelayBinary + `.Trim([char]34);$i.Arguments=$env:` + shellRelayArgs + `;$i.UseShellExecute=$false;` +
	`$p=[Diagnostics.Process]::Start($i);if(-not $p){throw 'no process was started'}}` +
	`catch{$e=$_.Exception;if($e.InnerException){$e=$e.InnerException};[Console]::Error.WriteLine($n+' ` + shellRelayStart + ` '+($e.Message -replace '\s+',' '));exit 1};` +
	`$p.WaitForExit();exit $p.ExitCode`

func shellEnv(sandbox string) []string {
	sr := os.Getenv("SystemRoot")
	psHome := filepath.Join(sr, `System32\WindowsPowerShell\v1.0`)

	path := os.Getenv("PATH")
	if path == "" {
		path = sr + `\System32;` + sr + `;` + psHome
	}
	if !strings.Contains(strings.ToLower(path), strings.ToLower(psHome)) {
		path += ";" + psHome
	}

	psModule := os.Getenv("PSModulePath")
	if psModule == "" {
		psModule = filepath.Join(psHome, "Modules")
	}

	env := []string{
		"SystemRoot=" + sr,
		"PATH=" + path,
		"PATHEXT=" + envOr("PATHEXT", ".COM;.EXE;.BAT;.CMD;.PS1"),
		"PSModulePath=" + psModule,

		"USERPROFILE=" + sandbox,
		"APPDATA=" + sandbox,
		"LOCALAPPDATA=" + sandbox,
		"TEMP=" + sandbox,
		"TMP=" + sandbox,
	}

	for _, k := range []string{"SystemDrive", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "COMPUTERNAME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_SUSPENDED,
	}
}

type shellTree struct {
	mu  sync.Mutex
	job windows.Handle
}

func (t *shellTree) adopt(cmd *exec.Cmd, walled bool) error {
	if cmd.Process == nil {
		return nil
	}
	pid := uint32(cmd.Process.Pid)
	job, err := t.join(pid)
	if err != nil {
		if walled {
			return fmt.Errorf("%s: %w", wallNoJob, err)
		}
		resumeProcess(pid)
		return nil
	}
	t.mu.Lock()
	t.job = job
	t.mu.Unlock()
	resumeProcess(pid)
	return nil
}

func (t *shellTree) join(pid uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func resumeProcess(pid uint32) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		h, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if oerr != nil {
			continue
		}
		_, _ = windows.ResumeThread(h)
		_ = windows.CloseHandle(h)
	}
}

func (t *shellTree) kill(cmd *exec.Cmd) error {
	t.mu.Lock()
	job := t.job
	t.mu.Unlock()
	if job != 0 {
		return windows.TerminateJobObject(job, 1)
	}
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (t *shellTree) close() {
	t.mu.Lock()
	job := t.job
	t.job = 0
	t.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}

type leftJobs struct{}

func (*leftJobs) keep(*exec.Cmd) bool            { return false }
func (*leftJobs) end(time.Duration) (int, error) { return 0, nil }

func normalizeShellOutput(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func invocationExempt(token string) bool {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false
	}
	clean := filepath.Clean(token)
	if !filepath.IsAbs(clean) {
		return false
	}
	sys := filepath.Clean(filepath.Join(root, "System32"))
	if strings.EqualFold(clean, sys) {
		return true
	}
	return len(clean) > len(sys) && strings.EqualFold(clean[:len(sys)], sys) && os.IsPathSeparator(clean[len(sys)])
}
