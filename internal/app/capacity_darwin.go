//go:build darwin || ios

package app

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"golang.org/x/sys/unix"
)

type hostCapacity struct{}

func (hostCapacity) Measure() pluginfacility.Availability {
	return measureDarwinCapacity(capacitySysctl)
}

func measureDarwinCapacity(read func(string) (uint64, error)) pluginfacility.Availability {
	total, err := read("hw.memsize")
	if err != nil || total == 0 || total > math.MaxInt64 {
		return pluginfacility.Availability{}
	}
	page, err := read("hw.pagesize")
	if err != nil || page == 0 {
		return pluginfacility.Availability{HostTotal: int64(total)}
	}
	var pages uint64
	for _, name := range []string{"vm.page_free_count", "vm.page_pageable_external_count"} {
		n, err := read(name)
		if err != nil || n > math.MaxInt64-pages {
			return pluginfacility.Availability{HostTotal: int64(total)}
		}
		pages += uint64(n)
	}
	if page > math.MaxInt64 || pages > uint64(math.MaxInt64)/page || pages*page > total {
		return pluginfacility.Availability{HostTotal: int64(total)}
	}
	return pluginfacility.Availability{HostKnown: true, HostTotal: int64(total), HostAvailable: int64(pages * uint64(page))}
}

func capacitySysctl(name string) (uint64, error) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return 0, err
	}
	return capacityCounter(raw)
}

func capacityCounter(raw []byte) (uint64, error) {
	switch len(raw) {
	case 4:
		return uint64(binary.NativeEndian.Uint32(raw)), nil
	case 8:
		return binary.NativeEndian.Uint64(raw), nil
	default:
		return 0, fmt.Errorf("memory counter has unsupported width %d", len(raw))
	}
}
