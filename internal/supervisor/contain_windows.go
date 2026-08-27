//go:build windows

package supervisor

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Containment for a native T3 child, Windows mechanism: ONE job object,
// assigned immediately after spawn, carrying lifetime, UI restrictions
// AND the address-space envelope together.
//
// WINDOWS CONTAINS THE PROCESS, NOT THE COMMAND LINE. Linux and macOS
// both wrap argv — bwrap and sandbox-exec are programs you run your
// program under — so their mechanism lives in pluginhost beside the
// spawn. Windows has nothing to wrap: containment is applied to a
// process that already exists, which is why it lives here.
//
// ONE JOB, BY RULE (D19, Sev 2026-08-26): the first cut assigned a
// second, memory-only job beside this one, and Windows refuses that
// nested assignment when the first job carries UI restrictions — the
// documented nested-job constraint. The memory ceiling therefore rides
// THIS job's extended limits; rlimit_windows.go records the fact.
//
// WHAT A JOB OBJECT ACTUALLY BUYS, stated rather than averaged into the
// word "contained":
//
//   - KILL ON CLOSE. Every process in the job dies when the last handle
//     closes — including when the supervisor crashes. The handle is now
//     TRACKED PER CHILD and closed by the reap path once the child is
//     dead (D20): closing then both ends the handle-per-activation leak
//     and reaps any descendants the direct kill missed.
//   - NO BREAKAWAY. A child cannot escape the job by creating processes
//     outside it. Breakaway is forbidden by NOT ASKING FOR IT — there is
//     no "deny" flag — and the first version of this file set
//     JOB_OBJECT_LIMIT_BREAKAWAY_OK, which GRANTS it, directly beneath a
//     comment promising there was none.
//   - UI RESTRICTIONS. No desktop switching, no clipboard, no window
//     messages to whatever else the operator is running.
//   - PROCESS MEMORY CEILING, when the spec configures one.
//
// TWO THINGS THIS DOES NOT DO, recorded rather than claimed away. The
// child is ALREADY RUNNING when this is called (Go's os/exec does not
// expose the main thread handle for CREATE_SUSPENDED + resume), so a
// window of a few hundred microseconds runs uncontained — the telemetry
// says so. And no filesystem or network restriction: AII OS brokers
// those on every platform; on Windows the enforcement layer (restricted
// token, WFP) is future work.
func containProcess(pid int, rlimitASBytes uint64) (func(), string, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, "", fmt.Errorf("supervisor: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	envelope := ""
	if rlimitASBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(rlimitASBytes)
		envelope = fmt.Sprintf(", memory ceiling %d bytes", rlimitASBytes)
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, "", fmt.Errorf("supervisor: set job limits: %w", err)
	}

	ui := windows.JOBOBJECT_BASIC_UI_RESTRICTIONS{
		UIRestrictionsClass: windows.JOB_OBJECT_UILIMIT_DESKTOP |
			windows.JOB_OBJECT_UILIMIT_DISPLAYSETTINGS |
			windows.JOB_OBJECT_UILIMIT_EXITWINDOWS |
			windows.JOB_OBJECT_UILIMIT_GLOBALATOMS |
			windows.JOB_OBJECT_UILIMIT_HANDLES |
			windows.JOB_OBJECT_UILIMIT_READCLIPBOARD |
			windows.JOB_OBJECT_UILIMIT_SYSTEMPARAMETERS |
			windows.JOB_OBJECT_UILIMIT_WRITECLIPBOARD,
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectBasicUIRestrictions,
		uintptr(unsafe.Pointer(&ui)),
		uint32(unsafe.Sizeof(ui))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, "", fmt.Errorf("supervisor: set job UI restrictions: %w", err)
	}

	h, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, "", fmt.Errorf("supervisor: open child for containment: %w", err)
	}
	defer windows.CloseHandle(h)

	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return nil, "", fmt.Errorf("supervisor: assign child to job: %w", err)
	}
	var once sync.Once
	cleanup := func() { once.Do(func() { _ = windows.CloseHandle(job) }) }
	return cleanup, "contained (one job object: dies with the supervisor, no breakaway, UI-restricted" + envelope + "; " +
		"assigned just AFTER spawn, so a brief pre-containment window remains; " +
		"filesystem and network are broker-mediated, not enforced)", nil
}
