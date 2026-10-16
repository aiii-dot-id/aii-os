package pluginhost

import (
	"fmt"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

const (
	FacilityTransportLocal = "sev_transport.local"

	FacilityOperatorPresenceFresh = "sev_operator_presence.fresh"

	FacilityForegroundLifecycle = "sev_foreground.lifecycle"
)

type hostContext struct {
	platform string
	arch     string
	topology string

	facilities []string

	supervised bool

	resources *selectionResources

	sets map[string]*setMaterial

	failed map[string]string
}

func (h hostContext) refusals(variant string) []reason {
	var out []reason
	if s := h.sets[variant]; s != nil {
		out = append(out, s.refusals()...)
	}
	keep := ""
	if r := h.resources; r != nil {
		fixed, momentary := capacityRefusals(r.profiles[variant], r.available, r.policy)
		out = append(append(out, fixed...), momentary...)
		keep = r.keep
	}
	if why, failed := h.failed[variant]; failed {
		out = append(out, checksFailedReason(why))
	}
	if variant != "" && variant == keep {
		out = slices.DeleteFunc(out, func(r reason) bool { return r.momentary })
	}
	return out
}

func (h hostContext) String() string {
	lane := "in-process"
	if h.supervised {
		lane = "supervised"
	}
	return fmt.Sprintf("%s/%s %s (%s lane)", h.platform, h.arch, h.topology, lane)
}

func (h hostContext) runtimeLane(rt string) (ok bool, why string) {
	switch rt {
	case "wasm_component":

		return true, ""
	case "native_t3_component":
		if h.platform == "windows" && !WindowsContainedNativeQualified {

			return false, WindowsNativeRefusal
		}
		if h.supervised {
			return true, ""
		}
		return false, "runtime:native_t3_component (no supervised exec lane on this host)"
	case "wasm_aot_component":

		return false, "runtime:wasm_aot_component (no AOT lane in the pure-Go worker)"
	case "inprocess_component":

		return false, "runtime:inprocess_component (no native in-process loader yet)"
	case "service_process":

		return false, "runtime:service_process (no service-process lane in the Go host)"
	}
	return false, "runtime:" + rt
}

func currentHost(opts *Options) hostContext {
	return hostContext{
		platform:   packagefmt.HostPlatform(),
		arch:       packagefmt.HostArch(),
		topology:   packagefmt.HostTopology(),
		facilities: opts.Facilities,
		supervised: opts.WorkerBinary != "",
		failed:     opts.ChecksFailed,
	}
}

func checksFailedReason(why string) reason {
	return reason{log: "its publisher's checks failed on this machine: " + why,
		words:  "its publisher's checks failed on this machine: " + why,
		remedy: "Try again, or install a release whose checks pass here"}
}

func selectVariant(res *packagefmt.Result, host hostContext) (*packagefmt.Variant, *VariantSelectionError) {
	m := res.Manifest
	refusal := &VariantSelectionError{PluginID: m.ID, Host: host.String()}

	var selectable []*packagefmt.Variant
	for i := range m.Variants {
		v := &m.Variants[i]

		if v.Platform != host.platform || v.Arch != host.arch || v.Topology != host.topology {

			refusal.Refusals = append(refusal.Refusals, VariantRefusal{
				VariantID: v.VariantID,
				Missing: []string{fmt.Sprintf("built for %s/%s %s, host is %s/%s %s",
					v.Platform, v.Arch, v.Topology, host.platform, host.arch, host.topology)},
			})
			continue
		}

		missing := laneMissing(res, host, v)
		switch why, failed := host.failed[v.VariantID]; {
		case len(missing) > 0:
		case v.ExecutionRuntime == "native_t3_component":
			missing = logs(host.refusals(v.VariantID))
		case failed:
			missing = append(missing, checksFailedReason(why).log)
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

	if m.VariantPreference != nil {
		for _, id := range m.VariantPreference {
			for _, v := range selectable {
				if v.VariantID == id {
					return v, nil
				}
			}
		}
		return nil, refusal
	}

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

func laneMissing(res *packagefmt.Result, host hostContext, v *packagefmt.Variant) []string {
	m := res.Manifest
	var missing []string
	if ok, why := host.runtimeLane(v.ExecutionRuntime); !ok {
		missing = append(missing, why)
	}

	if v.AdmissionProfile == "platform_reserved" || v.AdmissionProfile == "certified_native" ||
		v.ExecutionRuntime == "service_process" || v.ExecutionRuntime == "native_t3_component" {
		if res.Tier != packagefmt.TierT3 {
			missing = append(missing, fmt.Sprintf("trust tier %s proven, native (%s, %s) requires T3 — native is T3 only", res.Tier, v.AdmissionProfile, v.ExecutionRuntime))
		}
	}

	for _, predicate := range m.RequiredPredicates(v) {
		class, name, ok := packagefmt.SplitPredicate(predicate)
		if !ok {

			missing = append(missing, predicate+" (malformed predicate)")
			continue
		}
		switch class {
		case packagefmt.PredicateClassFacility:
			if name == FacilityTransportLocal {
				continue
			}
			if !slices.Contains(host.facilities, name) {
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

		}
	}
	return missing
}
