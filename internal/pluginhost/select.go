package pluginhost

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
type hostContext struct {
	platform string
	arch     string
	topology string
	// .
	// .
	facilities *facility.Set
	// .
	// .
	// .
	// .
	supervised bool
}

func (h hostContext) String() string {
	lane := "in-process"
	if h.supervised {
		lane = "supervised"
	}
	return fmt.Sprintf("%s/%s %s (%s lane)", h.platform, h.arch, h.topology, lane)
}

// .
// .
// .
// .
func (h hostContext) runtimeLane(rt string) (ok bool, why string) {
	switch rt {
	case "wasm_component":
		// .
		// .
		return true, ""
	case "native_t3_component":
		if h.platform == "windows" && !WindowsContainedNativeQualified {
			// .
			return false, WindowsNativeRefusal
		}
		if h.supervised {
			return true, ""
		}
		return false, "runtime:native_t3_component (no supervised exec lane on this host)"
	case "wasm_aot_component":
		// .
		// .
		return false, "runtime:wasm_aot_component (no AOT lane in the pure-Go worker)"
	case "inprocess_component":
		// .
		// .
		// .
		return false, "runtime:inprocess_component (no native in-process loader yet)"
	case "service_process":
		// .
		// .
		return false, "runtime:service_process (no service-process lane in the Go host)"
	}
	return false, "runtime:" + rt
}

// .
// .
func currentHost(opts *Options) hostContext {
	return hostContext{
		platform:   packagefmt.HostPlatform(),
		arch:       packagefmt.HostArch(),
		topology:   packagefmt.HostTopology(),
		facilities: opts.Facilities,
		supervised: opts.WorkerBinary != "",
	}
}

// .
// .
// .
// .
func selectVariant(res *packagefmt.Result, host hostContext) (*packagefmt.Variant, *VariantSelectionError) {
	m := res.Manifest
	refusal := &VariantSelectionError{PluginID: m.ID, Host: host.String()}

	var selectable []*packagefmt.Variant
	for i := range m.Variants {
		v := &m.Variants[i]
		var missing []string

		if v.Platform != host.platform || v.Arch != host.arch || v.Topology != host.topology {
			// .
			// .
			// .
			// .
			refusal.Refusals = append(refusal.Refusals, VariantRefusal{
				VariantID: v.VariantID,
				Missing: []string{fmt.Sprintf("built for %s/%s %s, host is %s/%s %s",
					v.Platform, v.Arch, v.Topology, host.platform, host.arch, host.topology)},
			})
			continue
		}

		if ok, why := host.runtimeLane(v.ExecutionRuntime); !ok {
			missing = append(missing, why)
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if v.AdmissionProfile == "platform_reserved" || v.AdmissionProfile == "certified_native" ||
			v.ExecutionRuntime == "service_process" || v.ExecutionRuntime == "native_t3_component" {
			if res.Tier != packagefmt.TierT3 {
				missing = append(missing, fmt.Sprintf("trust tier %s proven, native (%s, %s) requires T3 — native is T3 only", res.Tier, v.AdmissionProfile, v.ExecutionRuntime))
			}
		}

		for _, predicate := range m.RequiredPredicates(v) {
			class, name, ok := packagefmt.SplitPredicate(predicate)
			if !ok {
				// .
				// .
				missing = append(missing, predicate+" (malformed predicate)")
				continue
			}
			switch class {
			case packagefmt.PredicateClassFacility:
				if name == facility.TransportLocal {
					continue
				}
				if !host.facilities.Has(name) {
					missing = append(missing, predicate)
				}
			case packagefmt.PredicateClassRuntime:
				if name != v.ExecutionRuntime {
					missing = append(missing, predicate)
				}
			case packagefmt.PredicateClassTopology:
				if name != v.Topology {
					missing = append(missing, predicate)
				}
			case packagefmt.PredicateClassPermission:
				missing = append(missing, predicate+" (no granted-permission set exists on this host; mobile shells are the recorded wiring point)")
			case packagefmt.PredicateClassDistribution:
				missing = append(missing, predicate+" (no distribution facts exist host-side)")
			case packagefmt.PredicateClassBackend:
				// .
			}
		}

		if len(missing) == 0 {
			selectable = append(selectable, v)
		} else {
			refusal.Refusals = append(refusal.Refusals, VariantRefusal{VariantID: v.VariantID, Missing: missing})
		}
	}

	switch len(selectable) {
	case 0:
		return nil, refusal
	case 1:
		return selectable[0], nil
	}
	// .
	// .
	// .
	// .
	// .
	if m.DefaultVariant != "" {
		for _, v := range selectable {
			if v.VariantID == m.DefaultVariant {
				return v, nil
			}
		}
	}
	for _, v := range selectable {
		refusal.Ambiguous = append(refusal.Ambiguous, v.VariantID)
	}
	return nil, refusal
}
