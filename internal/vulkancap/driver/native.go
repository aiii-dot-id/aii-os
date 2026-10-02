//go:build ((linux && !android) || windows) && (amd64 || arm64)

package driver

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
)

type applicationInfo struct {
	Type               uint32
	Next               unsafe.Pointer
	Name               *byte
	Version            uint32
	Engine             *byte
	EngineVersion, API uint32
}
type instanceInfo struct {
	Type           uint32
	Next           unsafe.Pointer
	Flags          uint32
	App            *applicationInfo
	LayerCount     uint32
	Layers         unsafe.Pointer
	ExtensionCount uint32
	Extensions     unsafe.Pointer
}
type extension struct {
	Name    [256]byte
	Version uint32
}
type queue struct {
	Flags, Count, Timestamp uint32
	Extent                  [3]uint32
}
type memoryType struct{ Flags, Heap uint32 }
type heap struct {
	Size    uint64
	Flags   uint32
	Padding uint32
}
type memoryProperties struct {
	Type      uint32
	Next      *memoryBudget
	TypeCount uint32
	Types     [32]memoryType
	HeapCount uint32
	Heaps     [16]heap
}
type memoryBudget struct {
	Type          uint32
	Next          unsafe.Pointer
	Budget, Usage [16]uint64
}

func measure() (Capacity, error) {
	lookup, closeLoader, err := openLoader()
	if err != nil {
		return Capacity{}, err
	}
	defer closeLoader()

	var create func(*instanceInfo, uintptr, *uintptr) int32
	var destroy func(uintptr, uintptr)
	var devices func(uintptr, *uint32, *uintptr) int32
	var properties func(uintptr, *[128]uint64)
	var extensions func(uintptr, uintptr, *uint32, *extension) int32
	var queues func(uintptr, *uint32, *queue)
	var memory func(uintptr, *memoryProperties)
	for _, entry := range []struct {
		name string
		fn   any
	}{
		{"vkCreateInstance", &create}, {"vkDestroyInstance", &destroy},
		{"vkEnumeratePhysicalDevices", &devices}, {"vkGetPhysicalDeviceProperties", &properties},
		{"vkEnumerateDeviceExtensionProperties", &extensions}, {"vkGetPhysicalDeviceQueueFamilyProperties", &queues},
		{"vkGetPhysicalDeviceMemoryProperties2", &memory},
	} {
		addr, err := lookup(entry.name)
		if err != nil {
			return Capacity{}, fmt.Errorf("Vulkan entry point %s: %w", entry.name, err)
		}
		purego.RegisterFunc(entry.fn, addr)
	}
	app := applicationInfo{API: 1<<22 | 1<<12}
	info := instanceInfo{Type: 1, App: &app}
	var instance uintptr
	if r := create(&info, 0, &instance); r != 0 {
		return Capacity{}, fmt.Errorf("Vulkan instance refused: %d", r)
	}
	defer destroy(instance, 0)
	runtime.KeepAlive(info)
	var count uint32
	if r := devices(instance, &count, nil); r != 0 || count == 0 || count > 32 {
		return Capacity{}, errors.New("Vulkan device census unavailable")
	}
	list := make([]uintptr, count)
	if r := devices(instance, &count, &list[0]); r != 0 || count > uint32(len(list)) {
		return Capacity{}, errors.New("Vulkan device census changed")
	}
	if count == 0 {
		return Capacity{}, errors.New("Vulkan device census became empty")
	}
	kinds := make([]uint32, count)
	for i, device := range list[:count] {

		var props [128]uint64
		properties(device, &props)
		kinds[i] = uint32(props[2])
	}
	index, err := discreteDevice(kinds)
	if err != nil {
		return Capacity{}, err
	}
	chosen := list[index]
	var n uint32
	if r := extensions(chosen, 0, &n, nil); r != 0 || n == 0 || n > 4096 {
		return Capacity{}, errors.New("Vulkan extensions unavailable")
	}
	exts := make([]extension, n)
	if r := extensions(chosen, 0, &n, &exts[0]); r != 0 || n > uint32(len(exts)) {
		return Capacity{}, errors.New("Vulkan extension census changed")
	}
	budgetSupported := false
	for _, e := range exts[:n] {
		if string(bytes.TrimRight(e.Name[:], "\x00")) == "VK_EXT_memory_budget" {
			budgetSupported = true
		}
	}
	if !budgetSupported {
		return Capacity{}, errors.New("Vulkan driver does not expose a memory budget")
	}
	queues(chosen, &n, nil)
	if n == 0 || n > 256 {
		return Capacity{}, errors.New("Vulkan queues unavailable")
	}
	qs := make([]queue, n)
	queues(chosen, &n, &qs[0])
	if n > uint32(len(qs)) {
		return Capacity{}, errors.New("Vulkan queue census changed")
	}
	compute := false
	for _, q := range qs[:n] {
		if q.Count > 0 && q.Flags&2 != 0 {
			compute = true
		}
	}
	if !compute {
		return Capacity{}, errors.New("Vulkan compute unavailable")
	}
	b := memoryBudget{Type: 1000237000}
	m := memoryProperties{Type: 1000059006, Next: &b}
	memory(chosen, &m)
	c, err := capacity(m, b)
	runtime.KeepAlive(b)
	return c, err
}

func discreteDevice(kinds []uint32) (int, error) {
	chosen := -1
	for i, kind := range kinds {

		if kind != 2 {
			continue
		}
		if chosen >= 0 {
			return -1, errors.New("Vulkan multi-discrete-GPU placement is unknown")
		}
		chosen = i
	}
	if chosen < 0 {
		return -1, errors.New("Vulkan discrete GPU unavailable")
	}
	return chosen, nil
}

func capacity(m memoryProperties, b memoryBudget) (Capacity, error) {
	if m.HeapCount == 0 || m.HeapCount > 16 || m.TypeCount == 0 || m.TypeCount > 32 {
		return Capacity{}, errors.New("invalid Vulkan memory census")
	}
	selected := -1
	for i, h := range m.Heaps[:m.HeapCount] {
		if h.Flags&1 == 0 {
			continue
		}
		usable := false
		for _, t := range m.Types[:m.TypeCount] {
			if t.Heap == uint32(i) && t.Flags&1 != 0 {
				usable = true
			}
		}
		if usable && (selected < 0 || h.Size > m.Heaps[selected].Size) {
			selected = i
		}
	}
	if selected < 0 {
		return Capacity{}, errors.New("Vulkan device-local heap unavailable")
	}
	total, budget, usage := m.Heaps[selected].Size, b.Budget[selected], b.Usage[selected]
	if total == 0 || total > math.MaxInt64 || budget > total || usage > budget {
		return Capacity{}, errors.New("invalid Vulkan memory budget")
	}
	return Capacity{Total: int64(total), Available: int64(budget - usage)}, nil
}
