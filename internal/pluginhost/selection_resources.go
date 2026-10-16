package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

type selectionResources struct {
	profiles  map[string]AcceleratorProfile
	available pluginfacility.Availability
	policy    pluginfacility.AdmissionPolicy

	keep string
}

func runtimeDeclarationRefusals(d RuntimeDecl, operator packagefmt.TreeLimits) []runtimeCeiling {
	limits := runtimeLimits(d, operator)
	ceilings := []runtimeCeiling{
		{"compressed bytes", "plugins.runtime.max_compressed_bytes", d.Size, limits.MaxCompressedBytes},
		{"installed bytes", "plugins.runtime.max_installed_bytes", d.InstalledBytes, limits.MaxInstalledBytes},
		{"files", runtimeFilesKey, int64(d.Files), int64(limits.MaxFiles)},
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

const runtimeFilesKey = "plugins.runtime.max_files"

type runtimeCeiling struct {
	name, key         string
	declared, ceiling int64
}

func (c runtimeCeiling) String() string {
	return fmt.Sprintf("runtime %s declares %d, operator ceiling %d (%s)", c.name, c.declared, c.ceiling, c.key)
}

func (c runtimeCeiling) reason() reason {
	r := reason{log: c.String(), remedy: fmt.Sprintf("set %s to %d or more", c.key, c.declared)}
	switch c.key {
	case "plugins.runtime.max_compressed_bytes":
		r.words = fmt.Sprintf("its runtime archive is %s; the operator's ceiling %s is %s", humanBytes(c.declared), c.key, humanBytes(c.ceiling))
	case "plugins.runtime.max_installed_bytes":
		r.words = fmt.Sprintf("its installed runtime is %s; the operator's ceiling %s is %s", humanBytes(c.declared), c.key, humanBytes(c.ceiling))
	case "plugins.runtime.max_file_bytes":
		r.words = fmt.Sprintf("its largest runtime file is %s; the operator's ceiling %s is %s", humanBytes(c.declared), c.key, humanBytes(c.ceiling))
	case runtimeFilesKey:
		r.words = fmt.Sprintf("its runtime has %d files; %s admits %d", c.declared, c.key, c.ceiling)
	default:
		r.words = fmt.Sprintf("its runtime is %d folders deep; %s admits %d", c.declared, c.key, c.ceiling)
	}
	if strings.HasSuffix(c.key, "_bytes") {
		r.remedy += ", or clear it so that free disk decides"
	}
	return r
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
	if opts.ResourceFacts == nil || ctx.Err() != nil {
		return pluginfacility.Availability{}, ctx.Err()
	}
	facts, err := opts.ResourceFacts(ctx)
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
	return logs(append(fixed, momentary...))
}

func capacityRefusals(p AcceleratorProfile, a pluginfacility.Availability, policy pluginfacility.AdmissionPolicy) (fixed, momentary []reason) {
	if !a.HostKnown || a.HostTotal <= 0 || a.HostAvailable < 0 || a.HostAvailable > a.HostTotal {
		fixed = append(fixed, reason{log: "available host memory is unknown",
			words: "this machine's memory could not be measured, and an unmeasured machine is never taken to have room"})
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
			r := reason{log: fmt.Sprintf("memory_bytes needs %d, this host offers at most %d after operator limits", p.MemoryBytes, most)}
			if p.MemoryBytes > a.HostTotal {
				r.words = fmt.Sprintf("needs %s of memory; this machine has %s in all", humanBytes(p.MemoryBytes), humanBytes(a.HostTotal))
			} else {
				r.words = fmt.Sprintf("needs %s of memory; this machine has %s, and the operator's limits leave %s of it for plugins", humanBytes(p.MemoryBytes), humanBytes(a.HostTotal), humanBytes(most))
				var fix []string
				if policy.BudgetBytes > 0 && policy.BudgetBytes < p.MemoryBytes {
					fix = append(fix, fmt.Sprintf("set plugins.runtime.admission_memory_budget_bytes to %d or more", p.MemoryBytes))
				}
				if a.HostTotal-reserve < p.MemoryBytes {
					fix = append(fix, fmt.Sprintf("set plugins.runtime.admission_memory_reserve_bytes to %d or less", a.HostTotal-p.MemoryBytes))
				}
				r.remedy = strings.Join(fix, ", and ")
			}
			fixed = append(fixed, r)
		} else if room := limited(a.HostAvailable); p.MemoryBytes > room {
			held := "the rest held by other processes"
			if reserve > 0 {
				held += " or kept back by the operator's reserve"
			}
			momentary = append(momentary, reason{momentary: true,
				log:    fmt.Sprintf("memory_bytes needs %d, %d available after operator limits", p.MemoryBytes, room),
				words:  fmt.Sprintf("needs %s of this machine's %s of memory; %s is free for it now, %s", humanBytes(p.MemoryBytes), humanBytes(a.HostTotal), humanBytes(room), held),
				remedy: "it is chosen at a later activation once that memory is free"})
		}
	}
	for _, name := range p.RequiredAccelerators {
		d := a.Devices[name]
		if !d.Known || d.Total <= 0 || d.Available < 0 || d.Available > d.Total {
			r := reason{log: "accelerator:" + name + " availability is unknown",
				words: "the " + name + " device could not be measured on this machine, and an unmeasured device is never taken to have room"}

			if !d.Known && d.Why != "" {
				r.log += ": " + d.Why
				r.words += ": " + d.Why
			}
			fixed = append(fixed, r)
			continue
		}
		switch {
		case p.DeviceMemoryBytes == nil:
			fixed = append(fixed, reason{log: "device_memory_bytes is unknown",
				words: "the package does not say how much " + name + " device memory this set needs", remedy: "install a release that declares it"})
		case d.Unified && *p.DeviceMemoryBytes != 0:
			fixed = append(fixed, reason{log: "accelerator:" + name + " uses unified memory; include its footprint in memory_bytes and declare device_memory_bytes=0",
				words:  "the package declares separate " + name + " device memory, and on this machine that device shares the host's memory",
				remedy: "install a release that declares its footprint in memory_bytes"})
		case !d.Unified && *p.DeviceMemoryBytes > d.Total:
			fixed = append(fixed, reason{log: fmt.Sprintf("accelerator:%s needs %d device bytes, %d in total", name, *p.DeviceMemoryBytes, d.Total),
				words: fmt.Sprintf("needs %s of the %s device's memory; it has %s in all", humanBytes(*p.DeviceMemoryBytes), name, humanBytes(d.Total))})
		case !d.Unified && *p.DeviceMemoryBytes > d.Available:
			momentary = append(momentary, reason{momentary: true,
				log:    fmt.Sprintf("accelerator:%s needs %d device bytes, %d available", name, *p.DeviceMemoryBytes, d.Available),
				words:  fmt.Sprintf("needs %s of the %s device's %s; %s is free now, the rest held by other processes", humanBytes(*p.DeviceMemoryBytes), name, humanBytes(d.Total), humanBytes(d.Available)),
				remedy: "it is chosen at a later activation once that memory is free"})
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
