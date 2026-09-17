//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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
// .
// .
// .
// .
// .
// .
// .
// .
func containProcess(pid int, rlimitASBytes uint64) (func() error, string, error) {
	job, envelope, err := newJobObject(rlimitASBytes)
	if err != nil {
		return nil, "", err
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
	cleanup := onceCleanup(func() error { return errors.Join(terminateAndWaitJob(job), windows.CloseHandle(job)) })
	return cleanup, "contained (one job object: dies with the supervisor, no breakaway, UI-restricted" + envelope + "; " +
		"assigned just AFTER spawn, so a brief pre-containment window remains; " +
		"filesystem and network are broker-mediated, not enforced)", nil
}

// .
// .
// .
// .
func newJobObject(rlimitASBytes uint64) (windows.Handle, string, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, "", fmt.Errorf("supervisor: create job object: %w", err)
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
		return 0, "", fmt.Errorf("supervisor: set job limits: %w", err)
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
		return 0, "", fmt.Errorf("supervisor: set job UI restrictions: %w", err)
	}
	return job, envelope, nil
}

// .
// .
func terminateAndWaitJob(job windows.Handle) error {
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return fmt.Errorf("terminate contained job: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var info struct {
			UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
			PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
		}
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
			return fmt.Errorf("query contained job retirement: %w", err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("contained job still has %d active processes", info.ActiveProcesses)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
