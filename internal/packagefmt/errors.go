package packagefmt

import "fmt"

// Reason is a stable, machine-readable rejection code. Every failure
// names its requirement (R39 pattern): the operator — or Track C's
// acceptance test — sees exactly which validation step refused, in the
// C stack's SCREAMING_SNAKE reason-code style. Where the shared
// contract defines the string (NATIVE_TRUST_TIER_INELIGIBLE), the value
// is read from the embedded contract, not restated here.
type Reason string

const (
	// ReasonEnvelopeMalformed — the .aiiospkg gzip/tar grammar or the
	// fixed bundle layout is violated (PLUGIN_BUNDLE_FORMAT.md §3).
	ReasonEnvelopeMalformed Reason = "ENVELOPE_MALFORMED"
	// ReasonMemberOrder — members are not in canonical bytewise
	// lexicographic order under a sole root (§3.2).
	ReasonMemberOrder Reason = "MEMBER_ORDER_VIOLATION"
	// ReasonCeilingExceeded — an archive-size, member, or decompression
	// ceiling was exceeded; the reader rejects, never truncates (§1
	// streaming-bounded).
	ReasonCeilingExceeded Reason = "CEILING_EXCEEDED"
	// ReasonManifestInvalid — manifest.json missing, unparseable, or
	// outside the manifest.schema.json required surface (§7 step 2).
	ReasonManifestInvalid Reason = "MANIFEST_SCHEMA_INVALID"
	// ReasonPackageHashMismatch — recomputed install-root digest does
	// not match the manifest's package_hash (§7 step 4).
	ReasonPackageHashMismatch Reason = "PACKAGE_HASH_MISMATCH"
	// ReasonTrustObjectShape — the signature set itself is malformed:
	// a partial publisher pair, an attestation without the pair, or a
	// T3 platform proof mixed with community objects (§7 step 5).
	ReasonTrustObjectShape Reason = "TRUST_OBJECT_SHAPE_INVALID"
	// ReasonTrustRootUnavailable — evidence is present but the caller
	// pinned no root for its domain. Unverifiable is not unsigned.
	ReasonTrustRootUnavailable Reason = "TRUST_ROOT_UNAVAILABLE"
	// ReasonPublisherCertInvalid — publisher.cert fails against the
	// pinned plugin_publisher_certifier key or its embedded key
	// envelope is invalid (§7 step 6, TRUST_AND_SIGNING §3.1).
	ReasonPublisherCertInvalid Reason = "PUBLISHER_CERT_INVALID"
	// ReasonPublisherSigInvalid — publisher.sig fails against the
	// certificate's publisher key (§7 step 6, TRUST_AND_SIGNING §3.2).
	ReasonPublisherSigInvalid Reason = "PUBLISHER_SIG_INVALID"
	// ReasonAttestationInvalid — certifier.attestation fails against
	// the pinned reviewer root or its exact T1 release binding (§7
	// step 7, TRUST_AND_SIGNING §3.3).
	ReasonAttestationInvalid Reason = "ATTESTATION_INVALID"
	// ReasonPlatformSigInvalid — platform.sig fails against the pinned
	// platform_release root (§7 step 7, TRUST_AND_SIGNING §3.4).
	ReasonPlatformSigInvalid Reason = "PLATFORM_SIG_INVALID"
	// ReasonQuartetMismatch — id/version/package_hash/manifest_hash do
	// not line up across the manifest and every participating trust
	// object (TRUST_AND_SIGNING §5).
	ReasonQuartetMismatch Reason = "QUARTET_MISMATCH"
	// ReasonTrustPayloadRevoked — a verified trust object's canonical
	// payload digest appears in its owning root's signed revocation
	// snapshot (TRUST_AND_SIGNING §3.5 / AIII_SERVER_KEYS §7.1). Revoked
	// signed evidence rejects — it never downgrades (§5.2).
	ReasonTrustPayloadRevoked Reason = "TRUST_PAYLOAD_REVOKED"
	// ReasonRevocationStatusUnavailable — signed evidence is present but
	// the owning root's revocation snapshot is missing, malformed,
	// mis-signed, cross-domain, or rolled back, so the dependent tier is
	// unavailable. Evidence that cannot be revocation-checked cannot
	// elevate; T0 stays independent (design §1 fail-closed-per-tier).
	ReasonRevocationStatusUnavailable Reason = "REVOCATION_STATUS_UNAVAILABLE"
	// ReasonVariantIntegrity — a declared variant's artifact_hash does
	// not match the packaged entrypoint bytes (§7 step 8).
	ReasonVariantIntegrity Reason = "VARIANT_INTEGRITY_INVALID"
	// ReasonWASMBaselineMissing — the contract requires a WASM baseline
	// variant for the resolved tier and the manifest declares none.
	ReasonWASMBaselineMissing Reason = "WASM_BASELINE_MISSING"
)

// Error is a typed, reason-coded verification failure. The zero-trust
// default is reject: every path out of Verify that is not a fully
// proven Result carries one of these.
type Error struct {
	Reason Reason
	Step   string // the PLUGIN_BUNDLE_FORMAT.md §7 step (or reader stage) that refused
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s [%s]: %v", e.Reason, e.Step, e.Err)
	}
	return fmt.Sprintf("%s [%s]", e.Reason, e.Step)
}

func (e *Error) Unwrap() error { return e.Err }

func fail(reason Reason, step string, format string, args ...interface{}) *Error {
	return &Error{Reason: reason, Step: step, Err: fmt.Errorf(format, args...)}
}
