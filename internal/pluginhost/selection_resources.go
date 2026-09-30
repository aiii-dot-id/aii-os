package pluginhost

import (
	"context"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

type selectionResources struct {
	profiles  map[string]AcceleratorProfile
	available pluginfacility.Availability
	policy    pluginfacility.AdmissionPolicy
}

func validateSelectionProfiles(m *packagefmt.Manifest, profiles map[string]AcceleratorProfile) error {
	for _, v := range m.Variants {
		if v.ExecutionRuntime != "native_t3_component" {
			continue
		}
		p, ok := profiles[v.VariantID]
		if !ok || p.OS != v.Platform || p.Arch != v.Arch || p.DeviceMemoryBytes == nil {
			return &AcceleratorError{PluginID: m.ID, Detail: fmt.Sprintf("variant %s requires a matching profile and explicit device_memory_bytes (0 for no device allocation)", v.VariantID)}
		}
		if *p.DeviceMemoryBytes > 0 && len(p.RequiredAccelerators) == 0 {
			return &AcceleratorError{PluginID: m.ID, Detail: fmt.Sprintf("variant %s: device reservation requires required_accelerators", v.VariantID)}
		}
	}
	return nil
}

func selectionAvailability(ctx context.Context, opts *Options) (pluginfacility.Availability, error) {
	if err := ctx.Err(); err != nil {
		return pluginfacility.Availability{}, err
	}
	if opts.ResourceFacts == nil {
		return pluginfacility.Availability{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	facts, err := opts.ResourceFacts(ctx)
	if ctx.Err() != nil {
		return pluginfacility.Availability{}, ctx.Err()
	}
	return facts, err
}

func resourceRefusals(p AcceleratorProfile, a pluginfacility.Availability, policy pluginfacility.AdmissionPolicy) []string {
	var missing []string
	if !a.HostKnown || a.HostTotal <= 0 || a.HostAvailable < 0 || a.HostAvailable > a.HostTotal {
		missing = append(missing, "available host memory is unknown")
	} else {
		room := a.HostAvailable
		reserve := max(policy.ReserveBytes, 0)
		if reserve > room {
			room = 0
		} else {
			room -= reserve
		}
		if policy.BudgetBytes > 0 && policy.BudgetBytes < room {
			room = policy.BudgetBytes
		}
		if p.MemoryBytes > room {
			missing = append(missing, fmt.Sprintf("memory_bytes needs %d, %d available after operator limits", p.MemoryBytes, room))
		}
	}
	for _, name := range p.RequiredAccelerators {
		d := a.Devices[name]
		if !d.Known || d.Total <= 0 || d.Available < 0 || d.Available > d.Total {
			missing = append(missing, "accelerator:"+name+" availability is unknown")
			continue
		}
		if p.DeviceMemoryBytes == nil {
			missing = append(missing, "device_memory_bytes is unknown")
		} else if d.Unified && *p.DeviceMemoryBytes != 0 {
			missing = append(missing, "accelerator:"+name+" uses unified memory; include its footprint in memory_bytes and declare device_memory_bytes=0")
		} else if !d.Unified && *p.DeviceMemoryBytes > d.Available {
			missing = append(missing, fmt.Sprintf("accelerator:%s needs %d device bytes, %d available", name, *p.DeviceMemoryBytes, d.Available))
		}
	}
	return missing
}
