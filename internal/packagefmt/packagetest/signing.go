package packagetest

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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"
)

// .
const (
	ArtifactKindPublisherCert    = "plugin.publisher_certificate"
	ArtifactKindManifestSig      = "plugin.manifest"
	ArtifactKindAttestation      = "plugin.attestation"
	ArtifactKindPlatformSig      = "plugin.platform_release"
	ArtifactKindRevocationStatus = "plugin.revocation_status"

	KeyTypePublisher          = "plugin_publisher"
	KeyTypePublisherCertifier = "plugin_publisher_certifier"
	KeyTypeReviewer           = "plugin_reviewer"
	KeyTypePlatformRelease    = "platform_release"

	// .
	SigFilePublisherSig = "publisher.sig"
	SigFilePublisherCrt = "publisher.cert"
	SigFileAttestation  = "certifier.attestation"
	SigFilePlatformSig  = "platform.sig"

	// .
	// .
	StatusFileCertifier = "aiii_plugin_publisher_certifier_status.json"
	StatusFileReviewer  = "aiii_plugin_reviewer_status.json"
	StatusFilePlatform  = "aiii_platform_release_status.json"
)

// .
// .
type Role struct {
	KeyID string
	Env   *sigenvelope.PublicKeyEnvelope

	ml    *crypto.KeyPair
	slhSK *slh.SecretKey
}

// .
func NewRole(keyID, keyType string) (*Role, error) {
	now := time.Now().UTC()
	ml, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	slhSecret, slhPub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		return nil, err
	}
	pubB64 := base64.StdEncoding.EncodeToString(slhPub.Bytes())
	env := &sigenvelope.PublicKeyEnvelope{
		V: 1, Kind: "aiii.server_key.public", KeyID: keyID, KeyType: keyType,
		Profile:   crypto.ProfileRoot,
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Keys: []sigenvelope.PublicKeyMaterial{
			{Alg: crypto.SigAlg, PublicKeyB64: ml.PublicKeyB64(),
				PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SigAlg, keyID, ml.PublicKeyB64())},
			{Alg: crypto.SLHAlg, PublicKeyB64: pubB64,
				PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SLHAlg, keyID, pubB64)},
		},
	}
	return &Role{KeyID: keyID, Env: env, ml: ml, slhSK: &slhSecret}, nil
}

// .
// .
func (r *Role) Sign(artifactKind string, payload interface{}) ([]byte, error) {
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicaljson.CanonicalizeV1(payloadRaw)
	if err != nil {
		return nil, err
	}
	payloadSHA := sigenvelope.SHA256Prefixed(canonical)

	var sigs []sigenvelope.SignatureEntry
	for _, alg := range []string{crypto.SigAlg, crypto.SLHAlg} {
		key, ok := r.Env.FindPublicKey(alg)
		if !ok {
			return nil, fmt.Errorf("role %s missing %s", r.KeyID, alg)
		}
		input := sigenvelope.SignatureInput(artifactKind, crypto.ProfileRoot, alg, r.KeyID, key.PublicKeyFingerprint, payloadSHA)
		var raw []byte
		switch alg {
		case crypto.SigAlg:
			raw, err = crypto.Sign(r.ml, []byte(input))
		case crypto.SLHAlg:
			raw, err = r.slhSK.Sign(rand.Reader, []byte(input), nil)
		}
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sigenvelope.SignatureEntry{
			Alg: alg, KeyID: r.KeyID, PublicKeyFingerprint: key.PublicKeyFingerprint,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(input)),
			SigB64:               base64.StdEncoding.EncodeToString(raw),
		})
	}
	return json.Marshal(sigenvelope.Envelope{
		ArtifactKind: artifactKind, Payload: canonical, PayloadSHA256: payloadSHA,
		Canonicalization: sigenvelope.CanonicalizationV1,
		SignatureProfile: crypto.ProfileRoot, Signatures: sigs,
	})
}

// .
// .
// .
func ReferenceManifestHash(manifestRaw []byte) string {
	canonical, err := canonicaljson.CanonicalizeV1(manifestRaw)
	if err != nil {
		panic(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &m); err != nil {
		panic(err)
	}
	delete(m, "package_hash")
	stripped, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	view, err := canonicaljson.CanonicalizeV1(stripped)
	if err != nil {
		panic(err)
	}
	return sigenvelope.SHA256Prefixed(view)
}

// .
// .
// .
// .
// .
// .
type Signer struct {
	Certifier, Publisher, Reviewer *Role
	certBytes                      []byte
}

// .
// .
func NewSigner() (*Signer, error) {
	certifier, err := NewRole("aiii_plugin_publisher_certifier_pkgtest", KeyTypePublisherCertifier)
	if err != nil {
		return nil, err
	}
	publisher, err := NewRole("org.example.publisher_pkgtest", KeyTypePublisher)
	if err != nil {
		return nil, err
	}
	reviewer, err := NewRole("aiii_plugin_reviewer_pkgtest", KeyTypeReviewer)
	if err != nil {
		return nil, err
	}
	certBytes, err := certifier.Sign(ArtifactKindPublisherCert, map[string]interface{}{
		"publisher_id": "org.example", "publisher_key": publisher.Env,
	})
	if err != nil {
		return nil, err
	}
	return &Signer{
		Certifier: certifier, Publisher: publisher, Reviewer: reviewer,
		certBytes: certBytes,
	}, nil
}

// .
// .
// .
func (s *Signer) SignT1(spec *PackageSpec) error {
	pubSig, err := s.Publisher.Sign(ArtifactKindManifestSig, map[string]string{
		"package_hash":  ReferencePackageHash(spec.InstallFiles),
		"manifest_hash": ReferenceManifestHash(spec.Manifest),
	})
	if err != nil {
		return err
	}
	if spec.Signatures == nil {
		spec.Signatures = map[string][]byte{}
	}
	spec.Signatures[SigFilePublisherCrt] = s.certBytes
	spec.Signatures[SigFilePublisherSig] = pubSig
	return nil
}

// .
// .
// .
type RevocationEntry struct {
	ArtifactKind  string `json:"artifact_kind"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// .
// .
// .
// .
func (r *Role) SignRevocationStatus(epoch int64, entries []RevocationEntry) ([]byte, error) {
	if entries == nil {
		entries = []RevocationEntry{}
	}
	return r.Sign(ArtifactKindRevocationStatus, map[string]interface{}{
		"schema_version": 1, "trust_epoch": epoch, "revoked": entries,
	})
}

// .
// .
// .
// .
// .
func (s *Signer) MintEmptyStatus(dir string) error {
	for _, m := range []struct {
		role *Role
		file string
	}{
		{s.Certifier, StatusFileCertifier},
		{s.Reviewer, StatusFileReviewer},
	} {
		raw, err := m.role.SignRevocationStatus(1, nil)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, m.file), raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// .
// .
// .
func (s *Signer) SignT2(spec *PackageSpec, pluginID, version string, reviewedCapabilities []string) error {
	if err := s.SignT1(spec); err != nil {
		return err
	}
	if reviewedCapabilities == nil {
		reviewedCapabilities = []string{}
	}
	att, err := s.Reviewer.Sign(ArtifactKindAttestation, map[string]interface{}{
		"plugin_id": pluginID, "version": version,
		"package_hash":          ReferencePackageHash(spec.InstallFiles),
		"manifest_hash":         ReferenceManifestHash(spec.Manifest),
		"reviewed_capabilities": reviewedCapabilities,
	})
	if err != nil {
		return err
	}
	spec.Signatures[SigFileAttestation] = att
	return nil
}

// .
// .
// .
// .
// .
func NewPlatformRelease(keyID string) (root *Role, status []byte, err error) {
	root, err = NewRole(keyID, KeyTypePlatformRelease)
	if err != nil {
		return nil, nil, err
	}
	status, err = root.SignRevocationStatus(1, nil)
	if err != nil {
		return nil, nil, err
	}
	return root, status, nil
}

// .
// .
// .
// .
// .
func (r *Role) SignT3(spec *PackageSpec) error {
	pair := map[string]string{
		"package_hash":  ReferencePackageHash(spec.InstallFiles),
		"manifest_hash": ReferenceManifestHash(spec.Manifest),
	}
	sig, err := r.Sign(ArtifactKindPlatformSig, pair)
	if err != nil {
		return err
	}
	if spec.Signatures == nil {
		spec.Signatures = map[string][]byte{}
	}
	spec.Signatures[SigFilePlatformSig] = sig
	return nil
}
