package packagefmt

// The three-party trust chain per TRUST_AND_SIGNING.md: publisher
// certificate → publisher exact-release signature → reviewer
// attestation (T2) / platform release signature (T3). Every signed
// object travels in the one AIII-SIGNATURE-V1 envelope
// (internal/sigenvelope — the same grammar genesis verifies births
// with), profile AIII-PQ-SIGNATURE-V1-ROOT. Verification is offline:
// the only key inputs are the caller's pinned trust roots; the bundle
// itself supplies the publisher key solely through its certified
// envelope.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// Artifact kinds and key types — wire constants from TRUST_AND_SIGNING
// §3 (they name what a signature claims, never our package layout).
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

// Signature member filenames inside signatures/ (PLUGIN_BUNDLE_FORMAT §3).
const (
	sigFilePublisherSig = "publisher.sig"
	sigFilePublisherCrt = "publisher.cert"
	sigFileAttestation  = "certifier.attestation"
	sigFilePlatformSig  = "platform.sig"
)

// Tier is the resolved trust tier — derived from verified evidence
// only, never from a manifest string (no self-elevation).
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

// TrustRoots are the locally pinned AIII plugin trust roots
// (TRUST_AND_SIGNING §11: the package-owned trust store publishes
// exactly these three). A nil root makes its domain unverifiable:
// evidence from that domain then REJECTS — unverifiable is not
// unsigned, and invalid evidence never soft-passes (§8.5).
type TrustRoots struct {
	PublisherCertifier *sigenvelope.PublicKeyEnvelope
	Reviewer           *sigenvelope.PublicKeyEnvelope
	PlatformRelease    *sigenvelope.PublicKeyEnvelope

	// Revocation is the loaded revocation-status snapshot set
	// (LoadRevocationStatus) — design §2.1: Verify gains the set as an
	// input, and it travels HERE because roots and snapshots are the one
	// pinned trust state (same directory, same operator act) and because
	// the zero value is exactly the nil-safe rule: nil ≡ all three
	// snapshots missing ≡ every signed tier unavailable, T0 unaffected.
	Revocation *RevocationStatusSet
}

// LoadPinnedRoot reads a pinned public-key envelope from disk — THE
// loader for operator-named trust-root paths, shared by `aii plugin
// verify` and the app's plugins.*_root config (one validation, two
// callers — a root that one surface accepts and the other rejects would
// be a trust fork). An empty path pins nothing (nil, nil): packages
// carrying evidence for that domain then reject (unverifiable is not
// unsigned) while T0 still verifies. Validation happens at load so a
// malformed root file is reported against the path that named it, not
// later as a mid-chain verification error.
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
	// ProfileRoot is the only profile a pinned trust root may carry —
	// omitting the argument passed an EMPTY accepted set, which (by the
	// fail-closed rule) accepts nothing: every config-loaded root was
	// refused at load. Latent since the loader was extracted; caught by
	// the first test that loaded a REAL emitted envelope through this
	// path (devsign, 2026-08-19 — the uncited-surface lesson in action).
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, crypto.ProfileRoot); err != nil {
		return nil, fmt.Errorf("invalid key envelope: %v", err)
	}
	return &env, nil
}

// hashPairPayload is the closed {package_hash, manifest_hash} payload
// shared by publisher.sig and platform.sig (§3.2, §3.4: "contains
// exactly").
type hashPairPayload struct {
	PackageHash  string `json:"package_hash"`
	ManifestHash string `json:"manifest_hash"`
}

// certPayload is the closed publisher.cert payload (§3.1: only
// publisher_id and the publisher public-key envelope).
type certPayload struct {
	PublisherID  string          `json:"publisher_id"`
	PublisherKey json.RawMessage `json:"publisher_key"`
}

// attestationPayload is the closed certifier.attestation payload (§3.3).
type attestationPayload struct {
	PluginID             string   `json:"plugin_id"`
	Version              string   `json:"version"`
	PackageHash          string   `json:"package_hash"`
	ManifestHash         string   `json:"manifest_hash"`
	ReviewedCapabilities []string `json:"reviewed_capabilities"`
}

// strictDecode enforces the "contains exactly" closed-payload rule:
// unknown members reject, and exactly one JSON value.
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

// validatePinnedRoot checks a pinned root's own envelope contract plus
// its declared key domain — key separation matters (§1.5): a reviewer
// key must not quietly stand in for a platform key.
func validatePinnedRoot(root *sigenvelope.PublicKeyEnvelope, wantKeyType string, reason Reason, step string) *Error {
	if err := sigenvelope.ValidatePublicKeyEnvelope(root, crypto.ProfileRoot); err != nil {
		return fail(reason, step, "pinned %s root invalid: %v", wantKeyType, err)
	}
	if root.KeyType != wantKeyType {
		return fail(reason, step, "pinned root key_type %q is not %q — trust domains are separate keys", root.KeyType, wantKeyType)
	}
	return nil
}

// verifyPublisherChain runs §7 step 6: certificate against the pinned
// certifier, publisher key from the certificate ONLY, then the
// exact-release publisher signature over exactly the two recomputed
// hashes. Returns the certified publisher identity.
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
	// Revocation membership AFTER the signature verifies (design §2.1):
	// the certifier's snapshot owns plugin.publisher_certificate.
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
	// The signed key envelope owns the key's validity window (§3.1);
	// an expired publisher key is invalid evidence, not T0.
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
	// publisher.sig is signed by the CERTIFIED publisher key, but its
	// revocation domain owner is the certifier (§7.1: the certifier
	// snapshot may revoke plugin.manifest — pulling one bad release
	// without pulling the publisher).
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

// verifyAttestation runs the T2 half of §7 step 7: the reviewer root
// signs exactly the T1 release's quartet plus reviewed_capabilities.
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
	// A revoked attestation REJECTS the package — it never downgrades to
	// T1 (TRUST_AND_SIGNING §5.2: remove the invalid attestation and
	// explicitly reinstall as T1 if that is the intended package).
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

// verifyPlatformSig runs the T3 half of §7 step 7: the platform_release
// root signs exactly the two recomputed hashes; no publisher binding is
// required or accepted as a substitute.
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
