package pluginhost

// The selection matrix (PLATFORM_SEAMS §3 + the C precedence,
// manifest.c:1604-1655). Pure and in-memory: Results are constructed
// directly so every cell of the matrix is one crafted variant away —
// the E2E path is proven separately over real packages.

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// selHost is the canonical desktop host for the matrix: linux/x86_64
// full_identity_host with the supervised lane and a small facility set.
func selHost(t *testing.T, supervised bool, facilities ...facility.Facility) hostContext {
	t.Helper()
	var set *facility.Set
	if len(facilities) > 0 {
		var err error
		set, err = facility.NewSet(facilities...)
		if err != nil {
			t.Fatal(err)
		}
	}
	return hostContext{
		platform: "linux", arch: "x86_64", topology: "full_identity_host",
		facilities: set, supervised: supervised,
	}
}

func selVariant(id, platform, arch, runtime, profile string, required ...string) packagefmt.Variant {
	v := packagefmt.Variant{
		VariantID: id, Platform: platform, Arch: arch,
		Topology: "full_identity_host", ExecutionRuntime: runtime, AdmissionProfile: profile,
	}
	if len(required) > 0 {
		v.Requirements = &packagefmt.Requirements{Required: required}
	}
	return v
}

func selResult(tier packagefmt.Tier, defaultVariant string, variants ...packagefmt.Variant) *packagefmt.Result {
	return &packagefmt.Result{
		Tier: tier,
		Manifest: &packagefmt.Manifest{
			ID: "org.example.matrix", DefaultVariant: defaultVariant, Variants: variants,
		},
	}
}

func refusalFor(t *testing.T, e *VariantSelectionError, variantID string) VariantRefusal {
	t.Helper()
	for _, r := range e.Refusals {
		if r.VariantID == variantID {
			return r
		}
	}
	t.Fatalf("refusal for %s not present in %v", variantID, e.Refusals)
	return VariantRefusal{}
}

func TestSelectMissingFacilityNamedVerbatim(t *testing.T) {
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-audio", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"facility:sev_audio.raw_pcm", "facility:sev_operator_presence.fresh"))
	host := selHost(t, true, facility.Facility{Name: facility.OperatorPresenceFresh, Provider: "test"})

	_, serr := selectVariant(res, host)
	if serr == nil {
		t.Fatal("a variant requiring an unadvertised facility must not select")
	}
	missing := refusalFor(t, serr, "v-audio").Missing
	if len(missing) != 1 || missing[0] != "facility:sev_audio.raw_pcm" {
		t.Fatalf("the refusal must name exactly the missing predicate verbatim, got %v", missing)
	}
	// The advertised presence facility satisfied its predicate — it
	// must NOT appear as missing.
	if strings.Contains(serr.Error(), "presence") {
		t.Fatalf("satisfied predicates must not be reported missing: %v", serr)
	}
}

func TestSelectUnknownFacilityNameSimplyDoesNotMatch(t *testing.T) {
	// Grammar-legal, name unknown to any host set: classes are closed
	// (parse law) but names are open — the variant is unselectable and
	// the predicate is named, no parse failure anywhere.
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-mystery", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"facility:sev_totally.unknown"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil {
		t.Fatal("unknown facility names must not match")
	}
	if m := refusalFor(t, serr, "v-mystery").Missing; len(m) != 1 || m[0] != "facility:sev_totally.unknown" {
		t.Fatalf("the unknown name must be named verbatim, got %v", m)
	}
}

func TestSelectTransportLocalAlwaysSatisfied(t *testing.T) {
	// C hardcodes facility:sev_transport.local as satisfied
	// (plugin_host_install.c:1055-1056) — even a nil facility set
	// cannot unsatisfy it.
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-transport", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"facility:sev_transport.local"))
	v, serr := selectVariant(res, selHost(t, false))
	if serr != nil || v.VariantID != "v-transport" {
		t.Fatalf("transport.local must be structurally satisfied: %v", serr)
	}
}

func TestSelectPermissionPredicateNotSelectableOnDesktop(t *testing.T) {
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-mic", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"permission:microphone"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil {
		t.Fatal("no granted-permission set exists; a permission-requiring variant must not select")
	}
	m := refusalFor(t, serr, "v-mic").Missing
	if len(m) != 1 || !strings.HasPrefix(m[0], "permission:microphone") {
		t.Fatalf("the permission predicate must be named, got %v", m)
	}
	if !strings.Contains(m[0], "no granted-permission set") {
		t.Fatalf("the refusal must state the rule, got %v", m)
	}
}

func TestSelectPlatformArchMismatchRecorded(t *testing.T) {
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-mac", "macos", "arm64", "wasm_component", "wasm_sandbox"),
		selVariant("v-wrong-arch", "linux", "arm64", "wasm_component", "wasm_sandbox"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil {
		t.Fatal("no variant matches this host")
	}
	if len(serr.Refusals) != 2 {
		t.Fatalf("every variant must be accounted for, got %v", serr.Refusals)
	}
	if m := refusalFor(t, serr, "v-mac").Missing[0]; !strings.Contains(m, "macos/arm64") || !strings.Contains(m, "linux/x86_64") {
		t.Fatalf("the mismatch entry must show both coordinates, got %q", m)
	}
}

func TestSelectExactMatchWinsAmongDeclaredVariants(t *testing.T) {
	// The five-variant voice-release shape: one variant per platform;
	// exactly the host's own must select.
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-mac", "macos", "arm64", "wasm_component", "wasm_sandbox"),
		selVariant("v-linux", "linux", "x86_64", "wasm_component", "wasm_sandbox"),
		selVariant("v-win", "windows", "x86_64", "wasm_component", "wasm_sandbox"))
	v, serr := selectVariant(res, selHost(t, true))
	if serr != nil || v.VariantID != "v-linux" {
		t.Fatalf("the host's exact variant must select, got %v %v", v, serr)
	}
}

func TestSelectDefaultVariantDecidesAmongSelectable(t *testing.T) {
	// The C precedence: several selectable → the signed default picks
	// (manifest.c:1643-1646).
	res := selResult(packagefmt.TierT0, "v-b",
		selVariant("v-a", "linux", "x86_64", "wasm_component", "wasm_sandbox"),
		selVariant("v-b", "linux", "x86_64", "wasm_component", "wasm_sandbox"))
	v, serr := selectVariant(res, selHost(t, true))
	if serr != nil || v.VariantID != "v-b" {
		t.Fatalf("default_variant must decide, got %v %v", v, serr)
	}
}

func TestSelectAmbiguousWithoutDefaultRefuses(t *testing.T) {
	// The C precedence's other half: several selectable and no
	// deciding default is AMBIGUOUS — refuse, never guess
	// (manifest.c:1647-1650).
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-a", "linux", "x86_64", "wasm_component", "wasm_sandbox"),
		selVariant("v-b", "linux", "x86_64", "wasm_component", "wasm_sandbox"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil || len(serr.Ambiguous) != 2 {
		t.Fatalf("two selectable without default must refuse ambiguous, got %v", serr)
	}
	if !strings.Contains(serr.Error(), "refusing to guess") {
		t.Fatalf("the refusal must state the rule, got %v", serr)
	}

	// An inert default (naming no selectable variant) refuses the same
	// way — C compares against variant ids and a miss decides nothing.
	res.Manifest.DefaultVariant = "v-elsewhere"
	if _, serr := selectVariant(res, selHost(t, true)); serr == nil || len(serr.Ambiguous) != 2 {
		t.Fatalf("an inert default must not decide, got %v", serr)
	}
}

func TestSelectRuntimeLanes(t *testing.T) {
	for _, tc := range []struct {
		runtime, profile string
		tier             packagefmt.Tier
		supervised       bool
		selectable       bool
		wantMissing      string
	}{
		{"wasm_component", "wasm_sandbox", packagefmt.TierT0, false, true, ""},
		{"wasm_component", "wasm_sandbox", packagefmt.TierT0, true, true, ""},
		{"wasm_aot_component", "wasm_sandbox", packagefmt.TierT0, true, false, "runtime:wasm_aot_component"},
		{"service_process", "standard", packagefmt.TierT0, true, false, "runtime:service_process"},
		{"native_t3_component", "platform_reserved", packagefmt.TierT3, true, true, ""},
		{"native_t3_component", "platform_reserved", packagefmt.TierT3, false, false, "runtime:native_t3_component"},
	} {
		res := selResult(tc.tier, "", selVariant("v", "linux", "x86_64", tc.runtime, tc.profile))
		v, serr := selectVariant(res, selHost(t, tc.supervised))
		if tc.selectable {
			if serr != nil || v == nil {
				t.Fatalf("%s (supervised=%t) must select: %v", tc.runtime, tc.supervised, serr)
			}
			continue
		}
		if serr == nil {
			t.Fatalf("%s (supervised=%t) must refuse", tc.runtime, tc.supervised)
		}
		if m := refusalFor(t, serr, "v").Missing; !strings.Contains(strings.Join(m, ";"), tc.wantMissing) {
			t.Fatalf("%s refusal must carry %q, got %v", tc.runtime, tc.wantMissing, m)
		}
	}
}

func TestSelectTierGateOnNativeVariants(t *testing.T) {
	// Verify already refuses manifests whose variants outrun their
	// evidence, so selection's re-check is defense in depth against a
	// Result no verifier produced — it must still hold the line.
	res := selResult(packagefmt.TierT1, "",
		selVariant("v-native", "linux", "x86_64", "native_t3_component", "platform_reserved"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil {
		t.Fatal("platform_reserved without T3 evidence must not select")
	}
	if m := refusalFor(t, serr, "v-native").Missing; !strings.Contains(strings.Join(m, ";"), "requires T3") {
		t.Fatalf("the tier gate must be named, got %v", m)
	}
}

func TestSelectRuntimeAndTopologySelfPredicates(t *testing.T) {
	// runtime:/topology: predicates assert the variant's OWN declared
	// coordinates (plugin_host_install.c:1033-1052): consistent ones
	// hold, inconsistent ones are named missing.
	ok := selVariant("v-ok", "linux", "x86_64", "wasm_component", "wasm_sandbox",
		"runtime:wasm_component", "topology:full_identity_host")
	res := selResult(packagefmt.TierT0, "", ok)
	if _, serr := selectVariant(res, selHost(t, true)); serr != nil {
		t.Fatalf("self-consistent predicates must hold: %v", serr)
	}

	bad := selVariant("v-bad", "linux", "x86_64", "wasm_component", "wasm_sandbox",
		"runtime:native_t3_component", "topology:mobile_app_host")
	res = selResult(packagefmt.TierT0, "", bad)
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil {
		t.Fatal("inconsistent self-predicates must refuse")
	}
	m := strings.Join(refusalFor(t, serr, "v-bad").Missing, ";")
	for _, want := range []string{"runtime:native_t3_component", "topology:mobile_app_host"} {
		if !strings.Contains(m, want) {
			t.Fatalf("missing list must carry %q, got %v", want, m)
		}
	}
}

func TestSelectBackendPredicatesNeverHostMatched(t *testing.T) {
	// backend: names what the variant BRINGS — carried as metadata,
	// never a selection gate (PLATFORM_SEAMS §3.3, §5: the deliberate,
	// recorded divergence from the C admission's outright refusal).
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-accel", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"backend:cuda", "backend:cpu"))
	res.Manifest.Requirements = &packagefmt.Requirements{Optional: []string{"backend:rocm"}}
	v, serr := selectVariant(res, selHost(t, true))
	if serr != nil {
		t.Fatalf("backend predicates must never block selection: %v", serr)
	}
	backends := res.Manifest.BackendDeclarations(v)
	want := map[string]bool{"backend:cuda": true, "backend:cpu": true, "backend:rocm": true}
	if len(backends) != len(want) {
		t.Fatalf("backend declarations = %v, want the full declared set", backends)
	}
	for _, b := range backends {
		if !want[b] {
			t.Fatalf("unexpected backend declaration %q", b)
		}
	}
}

func TestSelectDistributionPredicateUnsatisfiable(t *testing.T) {
	// distribution: adopts the C evaluation: no distribution facts
	// exist host-side, so a required one never holds
	// (plugin_host_install.c:1072-1078).
	res := selResult(packagefmt.TierT0, "",
		selVariant("v-store", "linux", "x86_64", "wasm_component", "wasm_sandbox",
			"distribution:store"))
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil || !strings.Contains(serr.Error(), "distribution:store") {
		t.Fatalf("a required distribution predicate must refuse named, got %v", serr)
	}
}

func TestSelectManifestLevelRequirementsGateEveryVariant(t *testing.T) {
	// Release-level requirements apply to the selected variant exactly
	// like its own (plugin_host_install.c:1102-1121).
	res := selResult(packagefmt.TierT0, "",
		selVariant("v", "linux", "x86_64", "wasm_component", "wasm_sandbox"))
	res.Manifest.Requirements = &packagefmt.Requirements{Required: []string{"facility:sev_keystore.secret"}}
	_, serr := selectVariant(res, selHost(t, true))
	if serr == nil || !strings.Contains(serr.Error(), "facility:sev_keystore.secret") {
		t.Fatalf("release-level requirements must gate selection, got %v", serr)
	}
}

func TestSelectOptionalRequirementsNeverGate(t *testing.T) {
	// Only required gates (plugin_host_install.c:1084-1100 iterates
	// n_required only) — an optional facility the host lacks changes
	// nothing.
	v := selVariant("v", "linux", "x86_64", "wasm_component", "wasm_sandbox")
	v.Requirements = &packagefmt.Requirements{Optional: []string{"facility:sev_audio.raw_pcm"}}
	res := selResult(packagefmt.TierT0, "", v)
	if _, serr := selectVariant(res, selHost(t, true)); serr != nil {
		t.Fatalf("optional requirements must never gate: %v", serr)
	}
}
