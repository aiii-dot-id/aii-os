//go:build windows

package app

import (
	"time"

	"golang.org/x/sys/windows"
)

func processEnded(pid int, bound time.Duration) (ended, later bool) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return true, false
	}
	defer windows.CloseHandle(h)
	if created, ok := processCreated(h); ok {
		if ours, ok := thisProcessCreated(); ok && created.After(ours) {
			return true, true
		}
	}

	ms := min(bound.Milliseconds(), int64(windows.INFINITE-1))
	ev, err := windows.WaitForSingleObject(h, uint32(ms))
	return err == nil && ev == windows.WAIT_OBJECT_0, false
}

func processCreated(h windows.Handle) (time.Time, bool) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, created.Nanoseconds()), true
}

func defaultThisProcessCreated() (time.Time, bool) {
	return processCreated(windows.CurrentProcess())
}
