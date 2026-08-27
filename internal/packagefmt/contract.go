package packagefmt

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// ORIGIN: contracts/trust-tiers.json is a verbatim byte-for-byte copy of
// the C-stack shared contract at
// opensuperclaw/src/aii-os-plugin-sdk/contracts/trust-tiers.json
// (copied 2026-08-19, sha256
// c3668b18cee7952ef93b00ad54b19a4870acc710f5964cdcb353b9e01197075f).
// Go consumes the contract, it does not re-open the tier design
// (PLUGIN_FRAMEWORK.md §3): tier rules here are policy as data, derived
// from the embedded invariants — never from manifest strings, and never
// restated as Go constants that could drift from the C stack.
//
//go:embed contracts/trust-tiers.json
var trustTiersJSON []byte

// tierContract is the consumed slice of trust-tiers.json: the invariants
// the verifier enforces. Unknown siblings are deliberately ignored —
// the SDK contract also carries developer-workflow fields that are not
// verifier inputs.
type tierContract struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Invariants    struct {
		WASMBaselineRequiredT0T1T2   bool   `json:"wasm_baseline_required_for_t0_t1_t2"`
		T3WASMBaselineRequired       bool   `json:"platform_reserved_t3_wasm_baseline_required"`
		PublisherCertRequiredT1T2    bool   `json:"publisher_certificate_required_for_t1_t2"`
		CertifiedNativeAtT2Plus      bool   `json:"certified_native_allowed_at_t2_plus"`
		T2RequiresValidT1            bool   `json:"t2_requires_valid_t1"`
		T3RequiresPlatformReleaseSig bool   `json:"t3_requires_platform_release_signature"`
		T3RequiresPublisherSig       bool   `json:"t3_requires_publisher_signature"`
		NativeBelowT2DeniedReason    string `json:"certified_native_below_t2_denied_reason"`
	} `json:"invariants"`
}

// contract is the parsed shared tier contract. Parsed once at package
// init; a broken embed is a build defect, not a runtime condition.
var contract = mustParseContract()

func mustParseContract() tierContract {
	var c tierContract
	if err := json.Unmarshal(trustTiersJSON, &c); err != nil {
		panic(fmt.Sprintf("packagefmt: embedded trust-tiers.json unparseable: %v", err))
	}
	if c.Kind != "aiii_plugin_sdk_trust_tiers" || c.SchemaVersion != 1 {
		panic(fmt.Sprintf("packagefmt: embedded trust-tiers.json is not the v1 trust-tier contract (kind=%q schema_version=%d)", c.Kind, c.SchemaVersion))
	}
	if c.Invariants.NativeBelowT2DeniedReason == "" {
		panic("packagefmt: trust-tiers.json contract missing certified_native_below_t2_denied_reason")
	}
	return c
}

// reasonNativeTierIneligible is the contract-owned reason string for a
// native admission profile whose signatures do not prove enough tier
// ("NATIVE_TRUST_TIER_INELIGIBLE" in the current contract) — adopted
// from the data, not hardcoded.
func reasonNativeTierIneligible() Reason {
	return Reason(contract.Invariants.NativeBelowT2DeniedReason)
}

// PublisherProven reports whether the shared tier contract backs tier t
// with an accountable release signature: a certified-publisher exact-
// release signature at T1/T2 (publisher_certificate_required_for_t1_t2)
// or the platform release signature at T3
// (t3_requires_platform_release_signature). This is the capability
// broker's outer-ring floor for external effects and persistent RING4
// (PLUGIN_FRAMEWORK §3: T0 = pure compute, invocation input, temp RING4;
// T1 = user-approved network, persistent RING4). Derived from the
// contract DATA, never restated as Go constants: if the contract ever
// stopped requiring publisher proof at a tier, that tier would lose the
// external surface with it — fail closed, byte-agreed with the C stack.
func (t Tier) PublisherProven() bool {
	switch t {
	case TierT1, TierT2:
		return contract.Invariants.PublisherCertRequiredT1T2
	case TierT3:
		return contract.Invariants.T3RequiresPlatformReleaseSig
	}
	return false
}

// ReviewProven reports whether the contract backs tier t with an
// independent AIII review proof beyond the publisher's own signature:
// the reviewer attestation at T2, the platform release at T3. The data
// hook is certified_native_allowed_at_t2_plus — the contract's own
// statement of where reviewed trust begins (trust sufficient to run
// native code starts at T2). The broker uses this as the credential
// floor: the C daemon requires T2+ for auth_profile use (operations.c:
// 851-855 "auth_profile requires T2 or T3 trust"), and secret.read's
// registry minimum is SEV_TRUST_T2 (sev_capability.h).
func (t Tier) ReviewProven() bool {
	switch t {
	case TierT2:
		return contract.Invariants.CertifiedNativeAtT2Plus && t.PublisherProven()
	case TierT3:
		return contract.Invariants.T3RequiresPlatformReleaseSig
	}
	return false
}
