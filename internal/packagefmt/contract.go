package packagefmt

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .

//go:embed contracts/trust-tiers.json
var trustTiersJSON []byte

// .
// .
// .
// .
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

// .
// .
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

// .
// .
// .
// .
func reasonNativeTierIneligible() Reason {
	return Reason(contract.Invariants.NativeBelowT2DeniedReason)
}

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
func (t Tier) PublisherProven() bool {
	switch t {
	case TierT1, TierT2:
		return contract.Invariants.PublisherCertRequiredT1T2
	case TierT3:
		return contract.Invariants.T3RequiresPlatformReleaseSig
	}
	return false
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (t Tier) ReviewProven() bool {
	switch t {
	case TierT2:
		return contract.Invariants.CertifiedNativeAtT2Plus && t.PublisherProven()
	case TierT3:
		return contract.Invariants.T3RequiresPlatformReleaseSig
	}
	return false
}
