package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

type selectionResources struct {
	profiles  map[string]AcceleratorProfile
	available pluginfacility.Availability
	policy    pluginfacility.AdmissionPolicy
	runtimes  map[string]RuntimeDecl
	limits    packagefmt.TreeLimits

	keep string
}

func (r *selectionResources) refusals(variant string) []string {
	fixed, momentary := capacityRefusals(r.profiles[variant], r.available, r.policy)
	if d, ok := r.runtimes[variant]; ok {
		fixed = append(fixed, ceilingWords(runtimeDeclarationRefusals(d, r.limits))...)
	}

	if variant == r.keep {
		return fixed
	}
	return append(fixed, momentary...)
}

func runtimeDeclarationRefusals(d RuntimeDecl, limits packagefmt.TreeLimits) []runtimeCeiling {
	defaults := packagefmt.DefaultTreeLimits
	if limits.MaxCompressedBytes <= 0 {
		limits.MaxCompressedBytes = defaults.MaxCompressedBytes
	}
	if limits.MaxInstalledBytes <= 0 {
		limits.MaxInstalledBytes = defaults.MaxInstalledBytes
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = defaults.MaxFileBytes
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	ceilings := []runtimeCeiling{
		{"compressed bytes", "plugins.runtime.max_compressed_bytes", d.Size, limits.MaxCompressedBytes},
		{"installed bytes", "plugins.runtime.max_installed_bytes", d.InstalledBytes, limits.MaxInstalledBytes},
		{"files", "plugins.runtime.max_files", int64(d.Files), int64(limits.MaxFiles)},
	}
	if d.LargestFileBytes != nil && d.Depth != nil {
		ceilings = append(ceilings,
			runtimeCeiling{"largest file bytes", "plugins.runtime.max_file_bytes", *d.LargestFileBytes, limits.MaxFileBytes},
			runtimeCeiling{"depth", "plugins.runtime.max_depth", int64(*d.Depth), int64(limits.MaxDepth)})
	}
	var over []runtimeCeiling
	for _, c := range ceilings {
		if c.declared > c.ceiling {
			over = append(over, c)
		}
	}
	return over
}

type runtimeCeiling struct {
	name, key         string
	declared, ceiling int64
}

func (c runtimeCeiling) String() string {
	return fmt.Sprintf("runtime %s declares %d, operator ceiling %d (%s)", c.name, c.declared, c.ceiling, c.key)
}

func ceilingWords(over []runtimeCeiling) []string {
	words := make([]string, len(over))
	for i, c := range over {
		words[i] = c.String()
	}
	return words
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

const selectionMeasurement = 5 * time.Second

func selectionAvailability(ctx context.Context, opts *Options) (pluginfacility.Availability, error) {
	if opts.ResourceFacts == nil {
		return pluginfacility.Availability{}, ctx.Err()
	}
	return measureWithin(ctx, opts.ResourceFacts, selectionMeasurement)
}

func measureWithin(ctx context.Context, measure func(context.Context) (pluginfacility.Availability, error), bound time.Duration) (pluginfacility.Availability, error) {
	if err := ctx.Err(); err != nil {
		return pluginfacility.Availability{}, err
	}
	mctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	facts, err := measure(mctx)
	if cerr := ctx.Err(); cerr != nil {
		return pluginfacility.Availability{}, cerr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = nil
	}
	return facts, err
}

func resourceRefusals(p AcceleratorProfile, a pluginfacility.Availability, policy pluginfacility.AdmissionPolicy) []string {
	fixed, momentary := capacityRefusals(p, a, policy)
	return append(fixed, momentary...)
}

func capacityRefusals(p AcceleratorProfile, a pluginfacility.Availability, policy pluginfacility.AdmissionPolicy) (fixed, momentary []string) {
	if !a.HostKnown || a.HostTotal <= 0 || a.HostAvailable < 0 || a.HostAvailable > a.HostTotal {
		fixed = append(fixed, "available host memory is unknown")
	} else {
		reserve := max(policy.ReserveBytes, 0)
		limited := func(n int64) int64 {
			n = max(n-reserve, 0)
			if policy.BudgetBytes > 0 && policy.BudgetBytes < n {
				n = policy.BudgetBytes
			}
			return n
		}
		if most := limited(a.HostTotal); p.MemoryBytes > most {
			fixed = append(fixed, fmt.Sprintf("memory_bytes needs %d, this host offers at most %d after operator limits", p.MemoryBytes, most))
		} else if room := limited(a.HostAvailable); p.MemoryBytes > room {
			momentary = append(momentary, fmt.Sprintf("memory_bytes needs %d, %d available after operator limits", p.MemoryBytes, room))
		}
	}
	for _, name := range p.RequiredAccelerators {
		d := a.Devices[name]
		if !d.Known || d.Total <= 0 || d.Available < 0 || d.Available > d.Total {
			fixed = append(fixed, "accelerator:"+name+" availability is unknown")
			continue
		}
		switch {
		case p.DeviceMemoryBytes == nil:
			fixed = append(fixed, "device_memory_bytes is unknown")
		case d.Unified && *p.DeviceMemoryBytes != 0:
			fixed = append(fixed, "accelerator:"+name+" uses unified memory; include its footprint in memory_bytes and declare device_memory_bytes=0")
		case !d.Unified && *p.DeviceMemoryBytes > d.Total:
			fixed = append(fixed, fmt.Sprintf("accelerator:%s needs %d device bytes, %d in total", name, *p.DeviceMemoryBytes, d.Total))
		case !d.Unified && *p.DeviceMemoryBytes > d.Available:
			momentary = append(momentary, fmt.Sprintf("accelerator:%s needs %d device bytes, %d available", name, *p.DeviceMemoryBytes, d.Available))
		}
	}
	return fixed, momentary
}

func credited(a pluginfacility.Availability, serving *AcceleratorProfile) pluginfacility.Availability {
	if serving == nil {
		return a
	}
	give := func(available, total, n int64) int64 {
		if n <= 0 {
			return available
		}
		return available + min(n, total-available)
	}
	if a.HostKnown && a.HostTotal > 0 && a.HostAvailable >= 0 && a.HostAvailable <= a.HostTotal {
		a.HostAvailable = give(a.HostAvailable, a.HostTotal, serving.MemoryBytes)
	}
	if serving.DeviceMemoryBytes != nil && *serving.DeviceMemoryBytes > 0 && len(a.Devices) > 0 {
		a.Devices = maps.Clone(a.Devices)
		for _, name := range serving.RequiredAccelerators {
			d, ok := a.Devices[name]
			if !ok || !d.Known || d.Unified || d.Total <= 0 || d.Available < 0 || d.Available > d.Total {
				continue
			}
			d.Available = give(d.Available, d.Total, *serving.DeviceMemoryBytes)
			a.Devices[name] = d
		}
	}
	return a
}
