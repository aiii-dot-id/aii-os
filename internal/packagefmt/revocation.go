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
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
// .
const ArtifactKindRevocationStatus = "plugin.revocation_status"

// .
// .
// .
// .
// .
type RevocationDomain struct {
	RootKeyType   string
	FileName      string
	ArtifactKinds []string
}

// .
var revocationDomains = []RevocationDomain{
	{keyTypePublisherCertifier, "aiii_plugin_publisher_certifier_status.json",
		[]string{artifactKindPublisherCert, artifactKindManifestSig}},
	{keyTypeReviewer, "aiii_plugin_reviewer_status.json",
		[]string{artifactKindAttestation}},
	{keyTypePlatformRelease, "aiii_platform_release_status.json",
		[]string{artifactKindPlatformSig}},
}

// .
// .
func RevocationDomains() []RevocationDomain {
	out := make([]RevocationDomain, len(revocationDomains))
	for i, d := range revocationDomains {
		kinds := make([]string, len(d.ArtifactKinds))
		copy(kinds, d.ArtifactKinds)
		out[i] = RevocationDomain{RootKeyType: d.RootKeyType, FileName: d.FileName, ArtifactKinds: kinds}
	}
	return out
}

// .
// .
type RevokedEntry struct {
	ArtifactKind  string `json:"artifact_kind"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// .
// .
// .
type revocationPayload struct {
	SchemaVersion *int            `json:"schema_version"`
	TrustEpoch    *int64          `json:"trust_epoch"`
	Revoked       *[]RevokedEntry `json:"revoked"`
}

// .
// .
// .
// .
// .
// .
// .
type EpochGuard interface {
	// .
	// .
	// .
	TrustEpochHighWater(root string) (epoch int64, payloadSHA256 string, ok bool, err error)
	// .
	// .
	// .
	// .
	// .
	AcceptTrustEpoch(root string, epoch int64, payloadSHA256 string) error
}

// .
// .
type rootSnapshot struct {
	domain        RevocationDomain
	trustEpoch    int64
	payloadSHA256 string
	revoked       map[string]bool
	err           error
}

// .
// .
// .
// .
type RevocationStatusSet struct {
	byRoot map[string]*rootSnapshot
}

// .
// .
func (s *RevocationStatusSet) lookup(rootKeyType string) *rootSnapshot {
	if s == nil {
		return nil
	}
	return s.byRoot[rootKeyType]
}

// .
// .
func (s *RevocationStatusSet) Epoch(rootKeyType string) (int64, bool) {
	snap := s.lookup(rootKeyType)
	if snap == nil || snap.err != nil {
		return 0, false
	}
	return snap.trustEpoch, true
}

// .
// .
// .
func (s *RevocationStatusSet) Describe() []string {
	var out []string
	for _, d := range revocationDomains {
		snap := s.lookup(d.RootKeyType)
		switch {
		case snap == nil:
			out = append(out, fmt.Sprintf("revocation %s: ABSENT (no status set loaded)", d.RootKeyType))
		case snap.err != nil:
			out = append(out, fmt.Sprintf("revocation %s: ABSENT (%v)", d.RootKeyType, snap.err))
		default:
			out = append(out, fmt.Sprintf("revocation %s: epoch %d, %d revoked", d.RootKeyType, snap.trustEpoch, len(snap.revoked)))
		}
	}
	return out
}

// .
// .
// .
// .
// .
// .
// .
// .
func LoadRevocationStatus(dir string, roots TrustRoots, guard EpochGuard) *RevocationStatusSet {
	set := &RevocationStatusSet{byRoot: make(map[string]*rootSnapshot, len(revocationDomains))}
	for _, d := range revocationDomains {
		set.byRoot[d.RootKeyType] = loadRootSnapshot(dir, d, rootForDomain(roots, d.RootKeyType), guard)
	}
	return set
}

// .
// .
func rootForDomain(roots TrustRoots, keyType string) *sigenvelope.PublicKeyEnvelope {
	switch keyType {
	case keyTypePublisherCertifier:
		return roots.PublisherCertifier
	case keyTypeReviewer:
		return roots.Reviewer
	case keyTypePlatformRelease:
		return roots.PlatformRelease
	}
	return nil
}

// .
// .
// .
func loadRootSnapshot(dir string, d RevocationDomain, root *sigenvelope.PublicKeyEnvelope, guard EpochGuard) *rootSnapshot {
	absent := func(format string, args ...interface{}) *rootSnapshot {
		return &rootSnapshot{domain: d, err: fmt.Errorf(format, args...)}
	}
	if root == nil {
		return absent("no %s root pinned — snapshot unverifiable", d.RootKeyType)
	}
	shipped := false
	raw, err := os.ReadFile(filepath.Join(dir, d.FileName))
	if err != nil {
		if !os.IsNotExist(err) {
			return absent("status file %s unreadable: %v", d.FileName, err)
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
		if !sameRoot(root, shippedRoot(d.RootKeyType)) {
			return absent("status file %s missing", d.FileName)
		}
		if raw = shippedSnapshot(d.FileName); raw == nil {
			return absent("status file %s missing", d.FileName)
		}
		shipped = true
	}
	snap, err := parseRootSnapshot(raw, d, root)
	if err != nil {
		return absent("status file %s: %v", d.FileName, err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if guard != nil && !shipped {
		if err := applyEpochGuard(snap, guard); err != nil {
			return absent("status file %s: %v", d.FileName, err)
		}
	}
	return snap
}

// .
// .
func sameRoot(a, b *sigenvelope.PublicKeyEnvelope) bool {
	return a != nil && b != nil && a.KeyID == b.KeyID
}

// .
// .
// .
// .
func parseRootSnapshot(raw []byte, d RevocationDomain, root *sigenvelope.PublicKeyEnvelope) (*rootSnapshot, error) {
	if err := sigenvelope.ValidatePublicKeyEnvelope(root, crypto.ProfileRoot); err != nil {
		return nil, fmt.Errorf("pinned %s root invalid: %v", d.RootKeyType, err)
	}
	if root.KeyType != d.RootKeyType {
		return nil, fmt.Errorf("pinned root key_type %q is not %q — trust domains are separate keys", root.KeyType, d.RootKeyType)
	}
	payloadRaw, err := sigenvelope.VerifyPayload(raw, root, ArtifactKindRevocationStatus, crypto.ProfileRoot)
	if err != nil {
		return nil, fmt.Errorf("snapshot does not verify against the pinned %s root: %v", d.RootKeyType, err)
	}
	var p revocationPayload
	if err := strictDecode(payloadRaw, &p); err != nil {
		return nil, fmt.Errorf("payload is not the closed {schema_version, trust_epoch, revoked} object: %v", err)
	}
	if p.SchemaVersion == nil || p.TrustEpoch == nil || p.Revoked == nil {
		return nil, fmt.Errorf("payload is missing a required member (schema_version, trust_epoch, revoked)")
	}
	if *p.SchemaVersion != 1 {
		return nil, fmt.Errorf("schema_version must be exactly 1, got %d", *p.SchemaVersion)
	}
	if *p.TrustEpoch < 1 {
		return nil, fmt.Errorf("trust_epoch must be a positive integer, got %d", *p.TrustEpoch)
	}
	allowed := make(map[string]bool, len(d.ArtifactKinds))
	for _, k := range d.ArtifactKinds {
		allowed[k] = true
	}
	revoked := make(map[string]bool, len(*p.Revoked))
	for i, e := range *p.Revoked {
		if !allowed[e.ArtifactKind] {
			return nil, fmt.Errorf("revoked[%d] artifact_kind %q is outside the %s domain", i, e.ArtifactKind, d.RootKeyType)
		}
		if err := validateRevokedDigest(e.PayloadSHA256); err != nil {
			return nil, fmt.Errorf("revoked[%d]: %v", i, err)
		}
		if i > 0 {
			prev := (*p.Revoked)[i-1]
			// .
			// .
			// .
			if prev.ArtifactKind > e.ArtifactKind ||
				(prev.ArtifactKind == e.ArtifactKind && prev.PayloadSHA256 >= e.PayloadSHA256) {
				return nil, fmt.Errorf("revoked[%d] is not strictly sorted by (artifact_kind, payload_sha256)", i)
			}
		}
		revoked[revokedKey(e.ArtifactKind, e.PayloadSHA256)] = true
	}
	// .
	// .
	// .
	canonical, err := canonicaljson.CanonicalizeV1(payloadRaw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize payload: %v", err)
	}
	return &rootSnapshot{
		domain:        d,
		trustEpoch:    *p.TrustEpoch,
		payloadSHA256: sigenvelope.SHA256Prefixed(canonical),
		revoked:       revoked,
	}, nil
}

// .
// .
// .
// .
func validateRevokedDigest(v string) error {
	if err := sigenvelope.ValidatePayloadSHA256(v); err != nil {
		return err
	}
	if hexPart := strings.TrimPrefix(v, "sha256:"); hexPart != strings.ToLower(hexPart) {
		return fmt.Errorf("payload_sha256 hex must be lowercase")
	}
	return nil
}

func revokedKey(kind, digest string) string { return kind + "\n" + digest }

// .
// .
// .
// .
// .
// .
func applyEpochGuard(snap *rootSnapshot, guard EpochGuard) error {
	hw, hwSHA, ok, err := guard.TrustEpochHighWater(snap.domain.RootKeyType)
	if err != nil {
		return fmt.Errorf("trust_epoch high-water read failed: %v", err)
	}
	if ok {
		if snap.trustEpoch < hw {
			return fmt.Errorf("trust_epoch %d is below the ledgered high-water mark %d — ROLLBACK refused", snap.trustEpoch, hw)
		}
		if snap.trustEpoch == hw {
			if snap.payloadSHA256 != hwSHA {
				return fmt.Errorf("trust_epoch %d matches the high-water mark but the snapshot differs (%s != accepted %s) — FORK refused", snap.trustEpoch, snap.payloadSHA256, hwSHA)
			}
			return nil
		}
	}
	if err := guard.AcceptTrustEpoch(snap.domain.RootKeyType, snap.trustEpoch, snap.payloadSHA256); err != nil {
		return fmt.Errorf("trust_epoch %d acceptance could not be ledgered: %v", snap.trustEpoch, err)
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
func checkRevocation(roots TrustRoots, ownerKeyType, artifactKind string, payloadRaw json.RawMessage, step string) *Error {
	snap := roots.Revocation.lookup(ownerKeyType)
	if snap == nil {
		return fail(ReasonRevocationStatusUnavailable, step, "%s evidence cannot be revocation-checked: no %s revocation snapshot loaded (tier unavailable)", artifactKind, ownerKeyType)
	}
	if snap.err != nil {
		return fail(ReasonRevocationStatusUnavailable, step, "%s evidence cannot be revocation-checked: %s snapshot absent: %v (tier unavailable)", artifactKind, ownerKeyType, snap.err)
	}
	canonical, err := canonicaljson.CanonicalizeV1(payloadRaw)
	if err != nil {
		// .
		// .
		// .
		return fail(ReasonRevocationStatusUnavailable, step, "%s payload cannot be canonicalized for the revocation check: %v", artifactKind, err)
	}
	digest := sigenvelope.SHA256Prefixed(canonical)
	if snap.revoked[revokedKey(artifactKind, digest)] {
		return fail(ReasonTrustPayloadRevoked, step, "%s payload %s is revoked by the %s snapshot (trust_epoch %d)", artifactKind, digest, ownerKeyType, snap.trustEpoch)
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
func CheckReleaseRevocation(roots TrustRoots, artifactKind string, payload json.RawMessage) error {
	if err := checkRevocation(roots, keyTypePlatformRelease, artifactKind, payload, "release-revocation"); err != nil {
		return err
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
func ReleaseTrustRoots(trustDir string, root *sigenvelope.PublicKeyEnvelope) TrustRoots {
	roots := TrustRoots{PlatformRelease: root}
	roots.Revocation = LoadRevocationStatus(trustDir, roots, nil)
	return roots
}
