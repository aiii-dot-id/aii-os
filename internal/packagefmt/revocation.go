package packagefmt

// Revocation-status snapshots (docs/PLUGIN_REVOCATION_DESIGN.md; C
// contract: TRUST_AND_SIGNING.md §3.5, AIII_SERVER_KEYS.md §7.1). One
// signed snapshot per trust root, root-scoped: each root may revoke only
// its own artifact kinds, and the match target is the exact signed trust
// payload digest (canonicalize → sha256) — never package bytes, never a
// key id. The snapshot verifier is the ordinary received-payload
// algorithm (strict parse → canonical digest compare → ROOT-profile
// signature verify) — no new verification protocol, no fourth root, no
// online authority.
//
// Fail-closed per tier: a missing, malformed, mis-signed, cross-domain,
// or rolled-back snapshot makes only the dependent signed tier
// unavailable (its evidence rejects — invalid signed evidence never
// becomes unsigned success, §5.2), while T0 stays independent. The
// consequence lands on every ceremony: minting a root without also
// minting its empty signed snapshot leaves that tier permanently
// unverifiable.

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

// ArtifactKindRevocationStatus is the envelope artifact_kind of every
// revocation snapshot (AIII_SERVER_KEYS §7.1 — the ordinary signed
// envelope, a closed payload inside).
const ArtifactKindRevocationStatus = "plugin.revocation_status"

// RevocationDomain is one row of the fixed per-root domain table:
// which root signs which installed status file, and which artifact
// kinds that root may revoke. Domain membership is fixed by the
// existing root — the table is contract data from AIII_SERVER_KEYS
// §7.1, never derived.
type RevocationDomain struct {
	RootKeyType   string   // the signing root's key_type
	FileName      string   // canonical installed filename in the trust dir
	ArtifactKinds []string // the only kinds this root may revoke
}

// revocationDomains is THE table. Order matches the C document.
var revocationDomains = []RevocationDomain{
	{keyTypePublisherCertifier, "aiii_plugin_publisher_certifier_status.json",
		[]string{artifactKindPublisherCert, artifactKindManifestSig}},
	{keyTypeReviewer, "aiii_plugin_reviewer_status.json",
		[]string{artifactKindAttestation}},
	{keyTypePlatformRelease, "aiii_platform_release_status.json",
		[]string{artifactKindPlatformSig}},
}

// RevocationDomains returns a copy of the per-root domain table for
// tooling (devsign/devcert mint one empty snapshot per row they root).
func RevocationDomains() []RevocationDomain {
	out := make([]RevocationDomain, len(revocationDomains))
	for i, d := range revocationDomains {
		kinds := make([]string, len(d.ArtifactKinds))
		copy(kinds, d.ArtifactKinds)
		out[i] = RevocationDomain{RootKeyType: d.RootKeyType, FileName: d.FileName, ArtifactKinds: kinds}
	}
	return out
}

// RevokedEntry is one revoked (artifact_kind, payload_sha256) pair —
// the digest names the exact signed payload in canonical form.
type RevokedEntry struct {
	ArtifactKind  string `json:"artifact_kind"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// revocationPayload is the closed §7.1 snapshot payload. Pointer fields
// so a MISSING member is distinguishable from a zero value — "contains
// exactly" means all three members present, nothing else.
type revocationPayload struct {
	SchemaVersion *int            `json:"schema_version"`
	TrustEpoch    *int64          `json:"trust_epoch"`
	Revoked       *[]RevokedEntry `json:"revoked"`
}

// EpochGuard is the runtime's anti-rollback memory (design §2.3): the
// highest accepted trust_epoch per root is a LEDGERED fact — recorded as
// a trust.epoch_accepted event and read back from its projection, the
// same way witness receipts are (anchor.go restores from
// LastWitnessReceipt). A nil guard is the stateless posture (CLI
// verification, devsign self-verify): snapshots verify, but no
// acceptance memory exists to roll back from.
type EpochGuard interface {
	// TrustEpochHighWater returns the recorded high-water mark for root
	// (its epoch and the accepted snapshot's payload_sha256); ok=false
	// when no acceptance was ever ledgered.
	TrustEpochHighWater(root string) (epoch int64, payloadSHA256 string, ok bool, err error)
	// AcceptTrustEpoch durably records acceptance of (root, epoch,
	// payloadSHA256) — an appended ledger event, materialized. An error
	// means the acceptance is NOT recorded; the caller must treat the
	// snapshot as unusable (an unrecorded acceptance would let a later
	// rollback pass as first-seen).
	AcceptTrustEpoch(root string, epoch int64, payloadSHA256 string) error
}

// rootSnapshot is one root's loaded outcome: a usable snapshot, or the
// loud reason it is ABSENT for enforcement.
type rootSnapshot struct {
	domain        RevocationDomain
	trustEpoch    int64
	payloadSHA256 string          // canonical digest of the snapshot payload itself
	revoked       map[string]bool // key: artifact_kind + "\n" + payload_sha256
	err           error           // non-nil ⇒ absent, with the retained reason
}

// RevocationStatusSet is the loaded three-root snapshot set that Verify
// consults through TrustRoots.Revocation. A nil set behaves as all
// three snapshots absent: every signed tier unavailable, T0 unaffected
// (design §2.1 nil-safe rule).
type RevocationStatusSet struct {
	byRoot map[string]*rootSnapshot
}

// lookup returns the root's snapshot outcome; a nil receiver or unknown
// root reads as absent (fail closed).
func (s *RevocationStatusSet) lookup(rootKeyType string) *rootSnapshot {
	if s == nil {
		return nil
	}
	return s.byRoot[rootKeyType]
}

// Epoch returns root's accepted trust_epoch (ok=false when that root's
// snapshot is absent) — tooling/report surface, never authority.
func (s *RevocationStatusSet) Epoch(rootKeyType string) (int64, bool) {
	snap := s.lookup(rootKeyType)
	if snap == nil || snap.err != nil {
		return 0, false
	}
	return snap.trustEpoch, true
}

// Describe renders one operator-readable line per root — the loud
// reporting surface for boot logs and the CLI (absence reasons are
// retained verbatim from the load).
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

// LoadRevocationStatus loads and verifies the three root-scoped status
// files from dir (the trust directory, default <data>/trust/). Per-root
// outcome, never a load error: a snapshot that is missing, malformed,
// mis-signed, cross-domain, or a rollback leaves THAT root absent with
// its reason retained — the other roots and T0 are untouched. The
// owning pinned root comes from roots (a domain whose root is unpinned
// is unverifiable, therefore absent). guard, when non-nil, applies the
// ledgered anti-rollback high-water mark (design §2.3).
func LoadRevocationStatus(dir string, roots TrustRoots, guard EpochGuard) *RevocationStatusSet {
	set := &RevocationStatusSet{byRoot: make(map[string]*rootSnapshot, len(revocationDomains))}
	for _, d := range revocationDomains {
		set.byRoot[d.RootKeyType] = loadRootSnapshot(dir, d, rootForDomain(roots, d.RootKeyType), guard)
	}
	return set
}

// rootForDomain maps a domain owner's key_type to the caller's pinned
// root envelope.
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

// loadRootSnapshot loads one domain's status file. Every failure path
// returns an absent snapshot carrying its reason — fail closed for the
// tier, loud for the operator.
func loadRootSnapshot(dir string, d RevocationDomain, root *sigenvelope.PublicKeyEnvelope, guard EpochGuard) *rootSnapshot {
	absent := func(format string, args ...interface{}) *rootSnapshot {
		return &rootSnapshot{domain: d, err: fmt.Errorf(format, args...)}
	}
	if root == nil {
		return absent("no %s root pinned — snapshot unverifiable", d.RootKeyType)
	}
	raw, err := os.ReadFile(filepath.Join(dir, d.FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return absent("status file %s missing", d.FileName)
		}
		return absent("status file %s unreadable: %v", d.FileName, err)
	}
	snap, err := parseRootSnapshot(raw, d, root)
	if err != nil {
		return absent("status file %s: %v", d.FileName, err)
	}
	if guard != nil {
		if err := applyEpochGuard(snap, guard); err != nil {
			return absent("status file %s: %v", d.FileName, err)
		}
	}
	return snap
}

// parseRootSnapshot runs the §7.1 received-payload algorithm on one
// snapshot envelope: pinned-root contract + key-domain check, envelope
// verification (canonicalization gate, canonical digest compare, exact
// dual-PQ ROOT signature set), then the closed-payload and domain rules.
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
			// Strictly ascending (artifact_kind, payload_sha256) — one
			// canonical byte order, so equality here is a duplicate and
			// descent is a sort violation, both malformed.
			if prev.ArtifactKind > e.ArtifactKind ||
				(prev.ArtifactKind == e.ArtifactKind && prev.PayloadSHA256 >= e.PayloadSHA256) {
				return nil, fmt.Errorf("revoked[%d] is not strictly sorted by (artifact_kind, payload_sha256)", i)
			}
		}
		revoked[revokedKey(e.ArtifactKind, e.PayloadSHA256)] = true
	}
	// The snapshot's own canonical digest — recorded by the acceptance
	// event so an equal-epoch snapshot with DIFFERENT content is
	// detectable as a fork, not silently interchangeable.
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

// validateRevokedDigest enforces the §7.1 sha256:<64-lowercase-hex>
// entry shape. Stricter than the envelope-level ValidatePayloadSHA256:
// the snapshot is a byte-exact format, so uppercase hex is malformed
// here even though hex.DecodeString would tolerate it.
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

// applyEpochGuard enforces monotonic trust_epoch per root against the
// ledgered high-water mark. First-seen is accepted as-is; a lower epoch
// is a rollback; an equal epoch must be the SAME snapshot (same
// canonical payload digest) — the witness store's exact-retry rule
// (ai3-witnessd store.go), applied to snapshots. Acceptance that cannot
// be ledgered is not acceptance.
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
			return nil // the exact accepted snapshot — nothing new to ledger
		}
	}
	if err := guard.AcceptTrustEpoch(snap.domain.RootKeyType, snap.trustEpoch, snap.payloadSHA256); err != nil {
		return fmt.Errorf("trust_epoch %d acceptance could not be ledgered: %v", snap.trustEpoch, err)
	}
	return nil
}

// checkRevocation is the verify-time membership test (design §2.1),
// run for every trust object AFTER its signature verifies and before
// tier assignment: canonicalize + hash the received payload, then test
// (artifact_kind, digest) against the OWNING root's snapshot. An absent
// snapshot rejects the evidence outright — the same outcome class as
// every other invalid-evidence path (§5.2: invalid signed evidence
// never becomes unsigned success), with the load-time reason retained.
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
		// The payload already survived envelope verification, so this is
		// unreachable in practice — but an unhashable payload must not
		// pass the membership test it cannot take.
		return fail(ReasonRevocationStatusUnavailable, step, "%s payload cannot be canonicalized for the revocation check: %v", artifactKind, err)
	}
	digest := sigenvelope.SHA256Prefixed(canonical)
	if snap.revoked[revokedKey(artifactKind, digest)] {
		return fail(ReasonTrustPayloadRevoked, step, "%s payload %s is revoked by the %s snapshot (trust_epoch %d)", artifactKind, digest, ownerKeyType, snap.trustEpoch)
	}
	return nil
}
