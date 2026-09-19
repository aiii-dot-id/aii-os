//go:build darwin

package app

import (
	"golang.org/x/sys/unix"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
// .
type hostCapacity struct{}

func (hostCapacity) Measure() pluginfacility.Availability {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		return pluginfacility.Availability{}
	}
	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil || page == 0 {
		return pluginfacility.Availability{HostTotal: int64(total)}
	}
	var pages uint64
	for _, name := range []string{"vm.page_free_count", "vm.page_speculative_count", "vm.page_purgeable_count"} {
		n, err := unix.SysctlUint32(name)
		if err != nil {
			return pluginfacility.Availability{HostTotal: int64(total)}
		}
		pages += uint64(n)
	}
	return pluginfacility.Availability{HostKnown: true, HostTotal: int64(total), HostAvailable: int64(pages * uint64(page))}
}
