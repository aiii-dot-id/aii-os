//go:build windows

// shell_windows.go — the Windows host facts for the shell organ
// (R79): dialect, invocation, scrubbed environment, and tree
// containment. tool_shell.go is the tool; this file is what THIS host
// speaks. Windows PowerShell 5.1 ships with every supported Windows
// 11 — the shell the platform actually has, offered under its own
// name instead of the bash the platform never had (the pre-2026-08-18
// build advertised bash here and failed on every call; then the organ
// was honestly absent until a real need picked its shape — Beta 1
// journey 5 and Aster's 2026-08-27 signed gap report are that need).
package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shellDialect names the language the identity writes in the shell
// tool — declared in the tool's own description, never discovered by
// failure.
const shellDialect = "PowerShell"

// shellInvocation returns the host shell binary and the argv prefix
// the command is appended to. The binary is addressed absolutely
// under SystemRoot rather than resolved through PATH — the child env
// is scrubbed, and the shell's own location must not depend on what
// the scrub kept.
func shellInvocation() (string, []string) {
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	return ps, []string{"-NoProfile", "-NonInteractive", "-Command"}
}

// shellEnv is the scrubbed child environment: USERPROFILE/TEMP are
// the sandbox, PATH is the system triad PowerShell needs, and nothing
// of the host's own environment — keys, service vars, operator
// secrets — crosses over. SystemRoot must pass through: PowerShell
// and the .NET runtime under it do not start without it.
//
// Known bound, recorded not solved (Occam): PowerShell 5.1 encodes
// redirected native-command output in the console OEM codepage, so
// non-ASCII bytes can arrive OEM-encoded. ASCII — the overwhelming
// case — is unaffected; revisit with a real mojibake report, not in
// advance of one.
func shellEnv(sandbox string) []string {
	sr := os.Getenv("SystemRoot")
	return []string{
		"SystemRoot=" + sr,
		"PATH=" + sr + `\System32;` + sr + `;` + sr + `\System32\WindowsPowerShell\v1.0`,
		"PATHEXT=.COM;.EXE;.BAT;.CMD;.PS1",
		"USERPROFILE=" + sandbox,
		"TEMP=" + sandbox,
		"TMP=" + sandbox,
	}
}

// prepareTree is a no-op on Windows: process groups do not bound
// trees here. The tree is a job object, adopted after Start.
func prepareTree(*exec.Cmd) {}

// shellTree is the Windows tree handle: one job object with
// KILL_ON_JOB_CLOSE (the ruling recorded when this was a stub: "its
// tree-kill is a job object, not a process group"). Terminate kills
// the whole tree at the ceiling; closing the handle reaps anything
// that slipped through — on this platform nothing a command spawns
// outlives the call that ran it.
type shellTree struct {
	mu  sync.Mutex
	job windows.Handle
}

// adopt creates the job and places the shell in it. Failures leave
// job==0 and kill falls back to killing the shell process alone —
// weaker, and honest about it (the D24-shaped bound: post-start
// adoption cannot cover a child spawned in the shell's first
// instants either).
func (t *shellTree) adopt(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
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
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	t.mu.Lock()
	t.job = job
	t.mu.Unlock()
}

// kill terminates the job — the whole tree — at the ceiling. Without
// a job (adoption failed), the shell process alone is killed.
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

// close releases the job handle; KILL_ON_JOB_CLOSE reaps any process
// still inside it. On unix a backgrounded child may outlive a
// completed call (the group is killed only at the ceiling); here the
// tree dies with the call — the stricter reading of "the ceiling
// bounds the tree", recorded as a deliberate platform divergence.
func (t *shellTree) close() {
	t.mu.Lock()
	job := t.job
	t.job = 0
	t.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}
