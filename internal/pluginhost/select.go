package pluginhost

// Predicate-matched variant selection — the PLATFORM_SEAMS §3 gap,
// closed with the C daemon's own precedence adopted verbatim
// (manifest.c:1604-1655 select_current_variant):
//
//  1. a variant is a candidate only on an EXACT platform ∧ arch ∧
//     topology match (variant_matches_host_without_runtime,
//     manifest.c:1584-1590 — the C vocabulary has no wildcard and no
//     "portable" coordinate, so nearest-neighbor guessing does not
//     exist);
//  2. among candidates, a variant is SELECTABLE iff its runtime has a
//     lane on this host ∧ its admission profile is within the proven
//     tier ∧ every required predicate holds (the requirements
//     evaluation the C admission applies to the selected variant,
//     plugin_host_install.c:1029-1121, folded into selection here per
//     PLATFORM_SEAMS §3.3);
//  3. exactly one selectable → selected; several → the manifest's
//     default_variant picks (manifest.c:1643-1646); several with no
//     deciding default → a typed AMBIGUOUS refusal, never a guess
//     (manifest.c:1647-1650); zero → a typed refusal naming every
//     missing predicate per variant, because the operator must see
//     exactly why (the C result's missing_requirements list, made the
//     error itself).
//
// Predicate semantics per class, each traced to the C evaluation
// (plugin_host_install.c:1029-1082):
//
//   facility:  "facility:sev_transport.local" is satisfied
//              unconditionally (line 1055-1056 — a local transport
//              exists wherever a plugin can run at all); every other
//              name must be in the host's advertised set. An unknown
//              facility NAME simply never matches — names are open,
//              the CLASS set is closed (parse-time law).
//   runtime:   satisfied iff it names the variant's own
//              execution_runtime (lines 1033-1042) — a declared
//              self-consistency, not a host fact.
//   topology:  satisfied iff it names the variant's own topology
//              (lines 1043-1052) — same.
//   permission: NEVER satisfied on this host — no granted-permission
//              set exists yet, exactly the C state (lines 1067-1071:
//              PERMISSION_UNAVAILABLE unconditionally). The mobile
//              shells' OS-permission surface is the recorded wiring
//              point; until it exists, a variant requiring any
//              permission: is not selectable, typed and named.
//   distribution: never satisfied (lines 1072-1078, adopted) — no
//              distribution facts exist host-side.
//   backend:   NEVER host-matched (PLATFORM_SEAMS §3.3, §5: backend:
//              names what the variant BRINGS — its silicon). Carried
//              into ActivePlugin metadata for receipts/conformance.
//              Deliberate, recorded divergence from C, which refuses
//              required backend: predicates outright (lines
//              1072-1078); the register's ruling supersedes there.
//
// Only the REQUIRED lists gate selection; optional requirements are
// declarations (plugin_host_install.c:1084-1100 iterates n_required
// only — adopted).

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// hostContext is everything selection knows about this host.
type hostContext struct {
	platform string
	arch     string
	topology string
	// facilities is the app-assembled advertised set (nil advertises
	// nothing beyond the structurally-always transport predicate).
	facilities *facility.Set
	// supervised reports whether the exec lane exists here: the
	// operator's worker binary resolved on a desktop build. It is the
	// ONE switch for both supervised wasm and native T3 children —
	// deliberately one operator-owned toggle, not two.
	supervised bool
}

func (h hostContext) String() string {
	lane := "in-process"
	if h.supervised {
		lane = "supervised"
	}
	return fmt.Sprintf("%s/%s %s (%s lane)", h.platform, h.arch, h.topology, lane)
}

// runtimeLane reports whether this host can run the given
// execution_runtime, and when it cannot, why — the refusal entry uses
// the C missing-requirement spelling "runtime:<name>"
// (plugin_host_install.c:911-926 plugin_manifest_runtime_requirement).
func (h hostContext) runtimeLane(rt string) (ok bool, why string) {
	switch rt {
	case "wasm_component":
		// The wazero wall runs everywhere; supervised when the lane
		// exists, in-process otherwise.
		return true, ""
	case "native_t3_component":
		if h.supervised {
			return true, ""
		}
		return false, "runtime:native_t3_component (no supervised exec lane on this host)"
	case "wasm_aot_component":
		// The pure-Go worker has no AOT lane (precompiled machine code
		// is exactly what wazero-in-worker refuses to run).
		return false, "runtime:wasm_aot_component (no AOT lane in the pure-Go worker)"
	case "inprocess_component":
		// Mobile bundled native components need an in-process native
		// loader the Go runtime does not carry yet (the shells run
		// bundled WASM in-process instead).
		return false, "runtime:inprocess_component (no native in-process loader yet)"
	case "service_process":
		// The C daemon's plugin-owned service binaries; the Go host's
		// only native lane is the T3 stdio child.
		return false, "runtime:service_process (no service-process lane in the Go host)"
	}
	return false, "runtime:" + rt
}

// currentHost assembles the host context from the build's coordinates
// and the caller's options.
func currentHost(opts *Options) hostContext {
	return hostContext{
		platform:   packagefmt.HostPlatform(),
		arch:       packagefmt.HostArch(),
		topology:   packagefmt.HostTopology(),
		facilities: opts.Facilities,
		supervised: opts.WorkerBinary != "",
	}
}

// selectVariant applies the grounded selection to a verified Result.
// On success it returns the selected variant; otherwise a
// *VariantSelectionError naming, per variant, every reason it was not
// selectable.
func selectVariant(res *packagefmt.Result, host hostContext) (*packagefmt.Variant, *VariantSelectionError) {
	m := res.Manifest
	refusal := &VariantSelectionError{PluginID: m.ID, Host: host.String()}

	var selectable []*packagefmt.Variant
	for i := range m.Variants {
		v := &m.Variants[i]
		var missing []string

		if v.Platform != host.platform || v.Arch != host.arch || v.Topology != host.topology {
			// Not for this host at all — the C scan skips these
			// silently (manifest.c:1621-1622); the refusal still
			// names the mismatch so the operator sees the whole
			// picture.
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

		// Tier eligibility re-check. Verify already refused any
		// manifest whose variants claim more than the signatures
		// prove (verify.go checkTierEligibility), so with an honest
		// Result this cannot fire — it stays as the selection-local
		// statement of the rule (native variants only with proven
		// evidence; no self-elevation) so selection is safe even
		// against a Result constructed by future code paths.
		switch v.AdmissionProfile {
		case "platform_reserved":
			if res.Tier != packagefmt.TierT3 {
				missing = append(missing, fmt.Sprintf("trust tier %s proven, platform_reserved requires T3", res.Tier))
			}
		case "certified_native":
			if res.Tier < packagefmt.TierT2 {
				missing = append(missing, fmt.Sprintf("trust tier %s proven, certified_native requires T2+", res.Tier))
			}
		}

		for _, predicate := range m.RequiredPredicates(v) {
			class, name, ok := packagefmt.SplitPredicate(predicate)
			if !ok {
				// Unreachable behind parseManifest; refuse honestly
				// if a foreign Result ever carries it.
				missing = append(missing, predicate+" (malformed predicate)")
				continue
			}
			switch class {
			case packagefmt.PredicateClassFacility:
				if name == facility.TransportLocal {
					continue // always satisfied (C line 1055-1056)
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
				// Never host-matched; carried as metadata.
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
	// Several selectable: the publisher's signed default_variant picks
	// (manifest.c:1643-1646); with no deciding default the C daemon
	// refuses AMBIGUOUS rather than guessing (manifest.c:1647-1650) —
	// adopted, so precedence stays the publisher's declaration, never
	// a host-invented ranking.
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
