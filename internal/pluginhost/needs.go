package pluginhost

import (
	"fmt"
	"strings"
)

func needsWords(order []string, profiles map[string]AcceleratorProfile) string {
	var least *AcceleratorProfile
	var free []string
	for _, id := range order {
		p, ok := profiles[id]
		if !ok {
			continue
		}
		if len(p.RequiredAccelerators) == 0 {
			free = append(free, id)
			continue
		}
		if least == nil || deviceBytes(&p) < deviceBytes(least) {
			least = &p
		}
	}
	if least == nil {
		return ""
	}
	words := "needs a GPU (" + strings.Join(least.RequiredAccelerators, ", ") + ")"
	if n := deviceBytes(least); n > 0 && n < 1<<62 {
		words += fmt.Sprintf(" with %s of its memory", humanBytes(n))
	}
	if len(free) > 0 {
		words += ", or runs without one (" + strings.Join(free, ", ") + ")"
	}
	return words
}

func deviceBytes(p *AcceleratorProfile) int64 {
	if p.DeviceMemoryBytes == nil {
		return 1 << 62
	}
	return *p.DeviceMemoryBytes
}
