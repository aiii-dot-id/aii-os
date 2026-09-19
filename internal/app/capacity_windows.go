//go:build windows

package app

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// .
// .
type hostCapacity struct{}

func (hostCapacity) Measure() pluginfacility.Availability {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st)))
	if r == 0 || st.TotalPhys == 0 {
		return pluginfacility.Availability{}
	}
	return pluginfacility.Availability{HostKnown: true, HostTotal: int64(st.TotalPhys), HostAvailable: int64(st.AvailPhys)}
}
