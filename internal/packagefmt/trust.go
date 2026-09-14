package packagefmt

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
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
const (
	artifactKindPublisherCert = "plugin.publisher_certificate"
	artifactKindManifestSig   = "plugin.manifest"
	artifactKindAttestation   = "plugin.attestation"
	artifactKindPlatformSig   = "plugin.platform_release"

	keyTypePublisher          = "plugin_publisher"
	keyTypePublisherCertifier = "plugin_publisher_certifier"
	keyTypeReviewer           = "plugin_reviewer"
	keyTypePlatformRelease    = "platform_release"
)

// .
const (
	sigFilePublisherSig = "publisher.sig"
	sigFilePublisherCrt = "publisher.cert"
	sigFileAttestation  = "certifier.attestation"
	sigFilePlatformSig  = "platform.sig"
)

// .
// .
type Tier int

const (
	TierT0 Tier = iota
	TierT1
	TierT2
	TierT3
)

func (t Tier) String() string {
	switch t {
	case TierT0:
		return "T0"
	case TierT1:
		return "T1"
	case TierT2:
		return "T2"
	case TierT3:
		return "T3"
	}
	return fmt.Sprintf("Tier(%d)", int(t))
}

// .
// .
// .
// .
// .
type TrustRoots struct {
	PublisherCertifier *sigenvelope.PublicKeyEnvelope
	Reviewer           *sigenvelope.PublicKeyEnvelope
	PlatformRelease    *sigenvelope.PublicKeyEnvelope

	// .
	// .
	// .
	// .
	// .
	// .
	Revocation *RevocationStatusSet
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
func LoadPinnedRoot(path string) (*sigenvelope.PublicKeyEnvelope, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("not a public key envelope: %v", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, crypto.ProfileRoot); err != nil {
		return nil, fmt.Errorf("invalid key envelope: %v", err)
	}
	return &env, nil
}

// .
// .
// .
type hashPairPayload struct {
	PackageHash  string `json:"package_hash"`
	ManifestHash string `json:"manifest_hash"`
}

// .
// .
type certPayload struct {
	PublisherID  string          `json:"publisher_id"`
	PublisherKey json.RawMessage `json:"publisher_key"`
}

// .
type attestationPayload struct {
	PluginID             string   `json:"plugin_id"`
	Version              string   `json:"version"`
	PackageHash          string   `json:"package_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	ReviewedCapabilities []string `json:"reviewed_capabilities"`
}

// .
// .
func strictDecode(raw []byte, out interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after payload object")
	}
	return nil
}

// .
// .
// .
func validatePinnedRoot(root *sigenvelope.PublicKeyEnvelope, wantKeyType string, reason Reason, step string) *Error {
	if err := sigenvelope.ValidatePublicKeyEnvelope(root, crypto.ProfileRoot); err != nil {
		return fail(reason, step, "pinned %s root invalid: %v", wantKeyType, err)
	}
	if root.KeyType != wantKeyType {
		return fail(reason, step, "pinned root key_type %q is not %q — trust domains are separate keys", root.KeyType, wantKeyType)
	}
	return nil
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
// .
// .
// .
// .
// .
// .
func ValidatePlatformReleaseRoot(root *sigenvelope.PublicKeyEnvelope) error {
	if root == nil {
		return fmt.Errorf("no platform_release root pinned")
	}
	if verr := validatePinnedRoot(root, keyTypePlatformRelease, ReasonPlatformSigInvalid, "platform-release-root"); verr != nil {
		return verr
	}
	return nil
}

// .
// .
// .
// .
func verifyPublisherChain(certBytes, sigBytes []byte, roots TrustRoots, packageHash, manifestHash string) (string, *Error) {
	if roots.PublisherCertifier == nil {
		return "", fail(ReasonTrustRootUnavailable, "publisher-chain", "publisher evidence present but no plugin_publisher_certifier root is pinned")
	}
	if verr := validatePinnedRoot(roots.PublisherCertifier, keyTypePublisherCertifier, ReasonPublisherCertInvalid, "publisher-chain"); verr != nil {
		return "", verr
	}

	certRaw, err := sigenvelope.VerifyPayload(certBytes, roots.PublisherCertifier, artifactKindPublisherCert, crypto.ProfileRoot)
	if err != nil {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "publisher.cert does not verify against the pinned certifier: %v", err)
	}
	// .
	// .
	if verr := checkRevocation(roots, keyTypePublisherCertifier, artifactKindPublisherCert, certRaw, "publisher-chain"); verr != nil {
		return "", verr
	}
	var cert certPayload
	if err := strictDecode(certRaw, &cert); err != nil {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "publisher.cert payload is not the closed {publisher_id, publisher_key} object: %v", err)
	}
	if cert.PublisherID == "" || len(cert.PublisherKey) == 0 {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "publisher.cert payload is missing publisher_id or publisher_key")
	}

	var publisherKey sigenvelope.PublicKeyEnvelope
	if err := json.Unmarshal(cert.PublisherKey, &publisherKey); err != nil {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "certified publisher key envelope unreadable: %v", err)
	}
	// .
	// .
	if err := sigenvelope.ValidatePublicKeyEnvelope(&publisherKey, crypto.ProfileRoot); err != nil {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "certified publisher key envelope invalid: %v", err)
	}
	if publisherKey.KeyType != keyTypePublisher {
		return "", fail(ReasonPublisherCertInvalid, "publisher-chain", "certified key_type %q is not %q", publisherKey.KeyType, keyTypePublisher)
	}

	sigRaw, err := sigenvelope.VerifyPayload(sigBytes, &publisherKey, artifactKindManifestSig, crypto.ProfileRoot)
	if err != nil {
		return "", fail(ReasonPublisherSigInvalid, "publisher-chain", "publisher.sig does not verify against the certified publisher key: %v", err)
	}
	// .
	// .
	// .
	// .
	if verr := checkRevocation(roots, keyTypePublisherCertifier, artifactKindManifestSig, sigRaw, "publisher-chain"); verr != nil {
		return "", verr
	}
	var pair hashPairPayload
	if err := strictDecode(sigRaw, &pair); err != nil {
		return "", fail(ReasonPublisherSigInvalid, "publisher-chain", "publisher.sig payload is not the closed {package_hash, manifest_hash} object: %v", err)
	}
	if pair.PackageHash != packageHash || pair.ManifestHash != manifestHash {
		return "", fail(ReasonQuartetMismatch, "publisher-chain", "publisher.sig binds package_hash %s / manifest_hash %s, recomputed %s / %s", pair.PackageHash, pair.ManifestHash, packageHash, manifestHash)
	}
	return cert.PublisherID, nil
}

// .
// .
func verifyAttestation(attBytes []byte, roots TrustRoots, m *Manifest, packageHash, manifestHash string) ([]string, *Error) {
	if roots.Reviewer == nil {
		return nil, fail(ReasonTrustRootUnavailable, "attestation", "certifier.attestation present but no plugin_reviewer root is pinned")
	}
	if verr := validatePinnedRoot(roots.Reviewer, keyTypeReviewer, ReasonAttestationInvalid, "attestation"); verr != nil {
		return nil, verr
	}
	attRaw, err := sigenvelope.VerifyPayload(attBytes, roots.Reviewer, artifactKindAttestation, crypto.ProfileRoot)
	if err != nil {
		return nil, fail(ReasonAttestationInvalid, "attestation", "certifier.attestation does not verify against the pinned reviewer root: %v", err)
	}
	// .
	// .
	// .
	if verr := checkRevocation(roots, keyTypeReviewer, artifactKindAttestation, attRaw, "attestation"); verr != nil {
		return nil, verr
	}
	var att attestationPayload
	if err := strictDecode(attRaw, &att); err != nil {
		return nil, fail(ReasonAttestationInvalid, "attestation", "attestation payload is not the closed five-member object: %v", err)
	}
	if att.ReviewedCapabilities == nil {
		return nil, fail(ReasonAttestationInvalid, "attestation", "attestation payload is missing reviewed_capabilities")
	}
	if att.PluginID != m.ID || att.Version != m.Version ||
		att.PackageHash != packageHash || att.ManifestHash != manifestHash {
		return nil, fail(ReasonQuartetMismatch, "attestation", "attestation binds %s@%s %s/%s, expected %s@%s %s/%s", att.PluginID, att.Version, att.PackageHash, att.ManifestHash, m.ID, m.Version, packageHash, manifestHash)
	}
	return att.ReviewedCapabilities, nil
}

// .
// .
// .
func verifyPlatformSig(platBytes []byte, roots TrustRoots, packageHash, manifestHash string) *Error {
	if roots.PlatformRelease == nil {
		return fail(ReasonTrustRootUnavailable, "platform-sig", "platform.sig present but no platform_release root is pinned")
	}
	if verr := validatePinnedRoot(roots.PlatformRelease, keyTypePlatformRelease, ReasonPlatformSigInvalid, "platform-sig"); verr != nil {
		return verr
	}
	platRaw, err := sigenvelope.VerifyPayload(platBytes, roots.PlatformRelease, artifactKindPlatformSig, crypto.ProfileRoot)
	if err != nil {
		return fail(ReasonPlatformSigInvalid, "platform-sig", "platform.sig does not verify against the pinned platform_release root: %v", err)
	}
	if verr := checkRevocation(roots, keyTypePlatformRelease, artifactKindPlatformSig, platRaw, "platform-sig"); verr != nil {
		return verr
	}
	var pair hashPairPayload
	if err := strictDecode(platRaw, &pair); err != nil {
		return fail(ReasonPlatformSigInvalid, "platform-sig", "platform.sig payload is not the closed {package_hash, manifest_hash} object: %v", err)
	}
	if pair.PackageHash != packageHash || pair.ManifestHash != manifestHash {
		return fail(ReasonQuartetMismatch, "platform-sig", "platform.sig binds package_hash %s / manifest_hash %s, recomputed %s / %s", pair.PackageHash, pair.ManifestHash, packageHash, manifestHash)
	}
	return nil
}
