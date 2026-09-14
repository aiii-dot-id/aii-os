package packagefmt

import "fmt"

// .
// .
// .
// .
// .
// .
type Reason string

const (
	// .
	// .
	ReasonEnvelopeMalformed Reason = "ENVELOPE_MALFORMED"
	// .
	// .
	ReasonMemberOrder Reason = "MEMBER_ORDER_VIOLATION"
	// .
	// .
	// .
	ReasonCeilingExceeded Reason = "CEILING_EXCEEDED"
	// .
	// .
	ReasonManifestInvalid Reason = "MANIFEST_SCHEMA_INVALID"
	// .
	// .
	ReasonPackageHashMismatch Reason = "PACKAGE_HASH_MISMATCH"
	// .
	// .
	// .
	ReasonTrustObjectShape Reason = "TRUST_OBJECT_SHAPE_INVALID"
	// .
	// .
	ReasonTrustRootUnavailable Reason = "TRUST_ROOT_UNAVAILABLE"
	// .
	// .
	// .
	ReasonPublisherCertInvalid Reason = "PUBLISHER_CERT_INVALID"
	// .
	// .
	ReasonPublisherSigInvalid Reason = "PUBLISHER_SIG_INVALID"
	// .
	// .
	// .
	ReasonAttestationInvalid Reason = "ATTESTATION_INVALID"
	// .
	// .
	ReasonPlatformSigInvalid Reason = "PLATFORM_SIG_INVALID"
	// .
	// .
	// .
	ReasonQuartetMismatch Reason = "QUARTET_MISMATCH"
	// .
	// .
	// .
	// .
	ReasonTrustPayloadRevoked Reason = "TRUST_PAYLOAD_REVOKED"
	// .
	// .
	// .
	// .
	// .
	ReasonRevocationStatusUnavailable Reason = "REVOCATION_STATUS_UNAVAILABLE"
	// .
	// .
	ReasonVariantIntegrity Reason = "VARIANT_INTEGRITY_INVALID"
	// .
	// .
	ReasonWASMBaselineMissing Reason = "WASM_BASELINE_MISSING"
)

// .
// .
// .
type Error struct {
	Reason Reason
	Step   string
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
