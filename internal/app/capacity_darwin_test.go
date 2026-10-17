//go:build darwin

package app

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestDarwinFileCacheIsAvailableWithoutCountingAnonymousOrWiredMemory(t *testing.T) {
	facts := map[string]uint64{
		"hw.memsize": 32 << 30, "hw.pagesize": 16384,
		"vm.page_free_count": 1 << 15, "vm.page_pageable_external_count": 1 << 20,
	}
	read := func(name string) (uint64, error) {
		v, ok := facts[name]
		if !ok {
			return 0, errors.New("unavailable")
		}
		return v, nil
	}
	got := measureDarwinCapacity(read)
	if !got.HostKnown || got.HostAvailable != ((1<<15)+(1<<20))*16384 {
		t.Fatalf("file cache omitted or unrelated counters added: %+v", got)
	}
	for _, key := range []string{"hw.memsize", "hw.pagesize", "vm.page_free_count", "vm.page_pageable_external_count"} {
		v := facts[key]
		delete(facts, key)
		if measureDarwinCapacity(read).HostKnown {
			t.Fatalf("missing %s admitted", key)
		}
		facts[key] = v
	}
	facts["vm.page_pageable_external_count"] = math.MaxUint64
	if measureDarwinCapacity(read).HostKnown {
		t.Fatal("counter overflow admitted")
	}
	facts["vm.page_pageable_external_count"] = 1 << 22
	if measureDarwinCapacity(read).HostKnown {
		t.Fatal("inconsistent snapshot exceeds physical memory")
	}
	facts["vm.page_free_count"], facts["vm.page_pageable_external_count"] = 0, 0
	got = measureDarwinCapacity(read)
	if !got.HostKnown || got.HostAvailable != 0 {
		t.Fatal("measured exhaustion became unknown", got)
	}
}

func TestDarwinCapacityAcceptsActualCounterWidths(t *testing.T) {
	for _, width := range []int{4, 8} {
		raw := make([]byte, width)
		if width == 4 {
			binary.NativeEndian.PutUint32(raw, 16384)
		} else {
			binary.NativeEndian.PutUint64(raw, 16384)
		}
		n, err := capacityCounter(raw)
		if err != nil || n != 16384 {
			t.Fatalf("width=%d n=%d err=%v", width, n, err)
		}
	}
	if _, err := capacityCounter(make([]byte, 3)); err == nil {
		t.Fatal("unknown width became a fact")
	}
	got := (hostCapacity{}).Measure()
	if !got.HostKnown || got.HostAvailable > got.HostTotal {
		t.Fatalf("live macOS counters lost: %+v", got)
	}
}
