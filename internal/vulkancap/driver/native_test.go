//go:build ((linux && !android) || windows) && (amd64 || arm64)

package driver

import (
	"errors"
	"testing"
	"unsafe"
)

func TestDiscreteDeviceDoesNotConfuseIntegratedOrSoftwareMemory(t *testing.T) {
	for _, tc := range []struct {
		kinds []uint32
		want  int
	}{
		{[]uint32{2, 1, 4}, 0}, {[]uint32{1, 2, 4}, 1}, {[]uint32{4, 2}, 1},
		{[]uint32{1, 4}, -1}, {[]uint32{2, 2}, -1}, {nil, -1},
	} {
		got, err := discreteDevice(tc.kinds)
		if got != tc.want || (err != nil) != (tc.want < 0) {
			t.Fatalf("%v: %d %v", tc.kinds, got, err)
		}
		if tc.want < 0 {
			want := errNoDiscreteDevice
			if len(tc.kinds) == 2 && tc.kinds[0] == 2 && tc.kinds[1] == 2 {
				want = errAmbiguousDevices
			}
			if !errors.Is(err, want) {
				t.Fatalf("%v: error %v does not preserve %v", tc.kinds, err, want)
			}
		}
	}
}

func TestVulkanABI(t *testing.T) {
	// These match the 64-bit Khronos C declarations, not Go serialization.
	for name, pair := range map[string][2]uintptr{
		"application":  {unsafe.Sizeof(applicationInfo{}), 48},
		"instance":     {unsafe.Sizeof(instanceInfo{}), 64},
		"extension":    {unsafe.Sizeof(extension{}), 260},
		"queue":        {unsafe.Sizeof(queue{}), 24},
		"heap":         {unsafe.Sizeof(heap{}), 16},
		"memory":       {unsafe.Sizeof(memoryProperties{}), 536},
		"budget":       {unsafe.Sizeof(memoryBudget{}), 272},
		"heaps offset": {unsafe.Offsetof(memoryProperties{}.Heaps), 280},
	} {
		if pair[0] != pair[1] {
			t.Fatalf("%s: %d != %d", name, pair[0], pair[1])
		}
	}
}

func TestBudgetIsAvailableNotTotalOrSum(t *testing.T) {
	m := memoryProperties{TypeCount: 2, HeapCount: 3}
	m.Types[0] = memoryType{Flags: 1, Heap: 0}
	m.Types[1] = memoryType{Flags: 1, Heap: 1}
	m.Heaps[0] = heap{Size: 8 << 30, Flags: 1}
	m.Heaps[1] = heap{Size: 256 << 20, Flags: 1}
	m.Heaps[2] = heap{Size: 64 << 30} // host memory is not VRAM
	b := memoryBudget{}
	b.Budget[0], b.Usage[0] = 6<<30, 1<<30
	b.Budget[1] = 256 << 20
	c, err := capacity(m, b)
	if err != nil || c.Total != 8<<30 || c.Available != 5<<30 {
		t.Fatalf("%+v %v", c, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*memoryProperties, *memoryBudget)
	}{
		{"heap count", func(m *memoryProperties, _ *memoryBudget) { m.HeapCount = 17 }},
		{"type count", func(m *memoryProperties, _ *memoryBudget) { m.TypeCount = 33 }},
		{"no local", func(m *memoryProperties, _ *memoryBudget) { m.Types[0].Flags = 0; m.Types[1].Flags = 0 }},
		{"zero size", func(m *memoryProperties, _ *memoryBudget) { m.Heaps[0].Size = 0; m.Heaps[1].Size = 0 }},
		{"overflow", func(m *memoryProperties, _ *memoryBudget) { m.Heaps[0].Size = 1 << 63 }},
		{"budget exceeds size", func(_ *memoryProperties, b *memoryBudget) { b.Budget[0] = 9 << 30 }},
		{"usage exceeds budget", func(_ *memoryProperties, b *memoryBudget) { b.Usage[0] = 7 << 30 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mm, bb := m, b
			tc.change(&mm, &bb)
			if _, err := capacity(mm, bb); err == nil {
				t.Fatal("invalid driver facts admitted")
			}
		})
	}
	b.Budget[0], b.Usage[0] = 0, 0
	if c, err := capacity(m, b); err != nil || c.Available != 0 {
		t.Fatalf("exhausted budget: %+v %v", c, err)
	}
}
