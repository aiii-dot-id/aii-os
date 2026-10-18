//go:build windows

package supervisor

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestTheJobOfANativeChildEndsItOnAnUnhandledException(t *testing.T) {
	for _, ceiling := range []uint64{0, 256 << 20} {
		job, _, err := newJobObject(ceiling)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		qerr := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if cerr := windows.CloseHandle(job); cerr != nil {
			t.Fatal(cerr)
		}
		if qerr != nil {
			t.Fatal(qerr)
		}
		flags := info.BasicLimitInformation.LimitFlags
		for name, flag := range map[string]uint32{
			"JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION": windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION,
			"JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE":          windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		} {
			if flags&flag == 0 {
				t.Errorf("ceiling %d: the job's limits are %#x, without %s", ceiling, flags, name)
			}
		}
		if memory := flags&windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY != 0; memory != (ceiling > 0) || (memory && uint64(info.ProcessMemoryLimit) != ceiling) {
			t.Errorf("ceiling %d: the job's limits are %#x with a process memory limit of %d", ceiling, flags, info.ProcessMemoryLimit)
		}
	}
}
