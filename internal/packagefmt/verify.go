// Package packagefmt is the offline, fail-closed verifier for the
// shared `.aiiospkg` plugin bundle format — build-order step 2 of
// docs/PLUGIN_FRAMEWORK.md §15. Go consumes the C stack's package
// contract, it does not fork it:
//
//   - PLUGIN_BUNDLE_FORMAT.md owns the envelope and install-time
//     validation order (ported here as Verify's step sequence);
//   - PACKAGE_DIGEST.md owns the digest computation (digest.go);
//   - TRUST_AND_SIGNING.md owns the three-party trust chain (trust.go);
//   - contracts/trust-tiers.json (embedded verbatim) owns the tier
//     invariants — policy as data, never re-invented in code.
//
// Verification is offline (§8.5: no online dependency — the pinned
// roots and the bundle decide) and fail-closed: absence of evidence may
// select a lower tier, invalid evidence never does. The verifier
// assigns the tier from which signatures validate — a manifest can
// claim nothing about its own trust.
//
// Revocation-status snapshots (revocation.go; TRUST_AND_SIGNING §3.5,
// AIII_SERVER_KEYS §7.1, docs/PLUGIN_REVOCATION_DESIGN.md) are consulted
// through TrustRoots.Revocation: after every trust object's signature
// verifies, its canonical payload digest is membership-tested against
// the owning root's signed snapshot — revoked evidence rejects, and an
// absent snapshot makes its dependent tier unavailable while T0 stays
// independent.
//
// Deliberately out of scope for this step (KISS, later build-order
// steps own them): registry/requirement checks (§7 step 9), interface
// schema-hash resolution against packaged schema files (§7 step 8's
// interface half — the id→filename mapping is owned by the SDK build
// tooling), and install/activation/replacement.
package packagefmt

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// Result is a fully proven verification outcome. Every field is derived
// from verified evidence; nothing here restates unproven manifest claims.
type Result struct {
	Tier         Tier
	Manifest     *Manifest
	PackageHash  string
	ManifestHash string
	// PublisherID is the certified publisher identity (T1/T2 only) —
	// sourced from publisher.cert, the sole publisher-identity source.
	PublisherID string
	// ReviewedCapabilities is the reviewer-attested envelope (T2 only).
	ReviewedCapabilities []string
	// FileDigests maps every install-root file (normalized path relative
	// to install-root/) to its verified sha256:<hex> digest — the same
	// per-file digests the package-hash aggregation consumed (digest.go).
	// This is the verified-bytes-are-loaded-bytes seam: a loader that
	// later extracts a member (ReadMember) compares the extracted bytes
	// against THIS map, so what runs is provably what was verified, even
	// if the file on disk changed between the two passes.
	FileDigests map[string]string
}

// VerifyFile verifies an .aiiospkg at path.
func VerifyFile(path string, roots TrustRoots) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fail(ReasonEnvelopeMalformed, "open", "%v", err)
	}
	defer f.Close()
	return Verify(f, roots)
}

// bundleContents is everything the single streaming pass retains: the
// two materialized JSON planes (manifest + signature envelopes), the
// running install-root digest, and the member presence sets needed for
// the §7 shape checks. install-root artifact bytes are never held.
type bundleContents struct {
	root          string
	manifestRaw   []byte
	signatures    map[string][]byte
	digest        *packageDigest
	dirs          map[string]bool // normalized rel dirs under the root
	hasInstallDir bool
}

// Verify reads one .aiiospkg from r and verifies it offline against the
// pinned roots, returning the proven Result or a typed, reason-coded
// *Error. The default is reject: only a bundle that survives the full
// PLUGIN_BUNDLE_FORMAT §7 order comes back verified.
func Verify(r io.Reader, roots TrustRoots) (*Result, error) {
	contents, verr := streamBundle(r)
	if verr != nil {
		return nil, verr
	}

	// §7 step 1 (first half): manifest.json must be present before
	// anything can be trusted about the package.
	if contents.manifestRaw == nil {
		return nil, fail(ReasonManifestInvalid, "shape", "bundle has no manifest.json")
	}

	// §7 step 2: manifest schema (required surface).
	m, verr := parseManifest(contents.manifestRaw)
	if verr != nil {
		return nil, verr
	}

	// §7 step 1 (second half — these shape checks need the manifest):
	// sole top-level directory is exactly <id>-<version>; install-root
	// exists and is non-empty; interfaces/ exists when interfaces are
	// declared; every declared variant directory and entrypoint exists.
	if want := m.ID + "-" + m.Version; contents.root != want {
		return nil, fail(ReasonEnvelopeMalformed, "shape", "top-level directory %q is not the manifest's %q", contents.root, want)
	}
	if !contents.hasInstallDir || contents.digest.files == 0 {
		return nil, fail(ReasonEnvelopeMalformed, "shape", "install-root has no package payload files")
	}
	if m.Kind == "plugin" {
		if !contents.dirs["install-root/interfaces"] {
			return nil, fail(ReasonEnvelopeMalformed, "shape", "interfaces are declared but install-root/interfaces/ is absent")
		}
		for _, v := range m.Variants {
			if !contents.dirs["install-root/variants/"+v.VariantID] {
				return nil, fail(ReasonEnvelopeMalformed, "shape", "declared variant directory install-root/variants/%s/ is absent", v.VariantID)
			}
			if _, ok := contents.digest.perFile[v.Entrypoint]; !ok {
				return nil, fail(ReasonEnvelopeMalformed, "shape", "variant %s entrypoint %s is not in the package", v.VariantID, v.Entrypoint)
			}
		}
	}

	// §7 step 3: manifest hash (package_hash member omitted, §3.5).
	manifestHashValue, verr := manifestHash(contents.manifestRaw)
	if verr != nil {
		return nil, verr
	}

	// §7 step 4: recomputed package hash must match the manifest.
	packageHashValue := contents.digest.sum()
	if m.PackageHash != packageHashValue {
		return nil, fail(ReasonPackageHashMismatch, "package-hash", "manifest declares %s, install-root hashes to %s", m.PackageHash, packageHashValue)
	}

	// §7 step 5: trust-object shape. A T3 platform proof travels
	// alone — canonical T3 packaging omits community objects, and a
	// mixed package is malformed, never an ambiguous tier. Community
	// evidence is a strict pair; a reviewer attestation without the
	// pair is malformed too.
	// Presence is map membership, never nil-ness of the bytes: a
	// present-but-empty trust object is present invalid evidence, and
	// invalid evidence must reject, not read as absence.
	certBytes, hasCert := contents.signatures[sigFilePublisherCrt]
	sigBytes, hasSig := contents.signatures[sigFilePublisherSig]
	attBytes, hasAtt := contents.signatures[sigFileAttestation]
	platBytes, hasPlat := contents.signatures[sigFilePlatformSig]
	if hasPlat && (hasCert || hasSig || hasAtt) {
		return nil, fail(ReasonTrustObjectShape, "trust-shape", "platform.sig mixed with community trust objects — a mixed T3/community package is malformed")
	}
	if hasCert != hasSig {
		return nil, fail(ReasonTrustObjectShape, "trust-shape", "publisher.cert and publisher.sig are a pair; neither is valid alone")
	}
	if hasAtt && !hasCert {
		return nil, fail(ReasonTrustObjectShape, "trust-shape", "certifier.attestation requires the complete publisher pair (t2_requires_valid_t1)")
	}

	// §7 steps 6-7: the applicable chain, verified once. Trust
	// resolution per TRUST_AND_SIGNING §5 — evidence decides the tier.
	result := &Result{
		Manifest:     m,
		PackageHash:  packageHashValue,
		ManifestHash: manifestHashValue,
		Tier:         TierT0,
	}
	switch {
	case hasPlat:
		if !contract.Invariants.T3RequiresPlatformReleaseSig {
			// The contract cannot say T3 needs no platform signature —
			// refuse to run under a contract weaker than the protocol.
			return nil, fail(ReasonPlatformSigInvalid, "platform-sig", "embedded tier contract does not require the platform_release signature for T3")
		}
		if verr := verifyPlatformSig(platBytes, roots, packageHashValue, manifestHashValue); verr != nil {
			return nil, verr
		}
		result.Tier = TierT3
	case hasCert:
		publisherID, verr := verifyPublisherChain(certBytes, sigBytes, roots, packageHashValue, manifestHashValue)
		if verr != nil {
			return nil, verr
		}
		result.Tier = TierT1
		result.PublisherID = publisherID
		if hasAtt {
			reviewed, verr := verifyAttestation(attBytes, roots, m, packageHashValue, manifestHashValue)
			if verr != nil {
				return nil, verr
			}
			result.Tier = TierT2
			result.ReviewedCapabilities = reviewed
		}
	}

	// §7 step 8 (variant half): every declared variant's artifact_hash
	// must match the packaged entrypoint bytes — the declaration and
	// the artifact are both signed, so they must agree.
	for _, v := range m.Variants {
		if got := "sha256:" + contents.digest.perFile[v.Entrypoint]; got != v.ArtifactHash {
			return nil, fail(ReasonVariantIntegrity, "variant-integrity", "variant %s artifact_hash %s does not match packaged entrypoint digest %s", v.VariantID, v.ArtifactHash, got)
		}
	}

	// §7 step 10: tier classification against the embedded contract.
	// No self-elevation: a manifest whose variants claim more than the
	// verified signatures prove is rejected, not downgraded.
	if verr := checkTierEligibility(m, result.Tier); verr != nil {
		return nil, verr
	}

	// Only a fully proven Result carries digests: exposed last, so a
	// rejected bundle never hands out per-file evidence.
	result.FileDigests = make(map[string]string, len(contents.digest.perFile))
	for rel, hexDigest := range contents.digest.perFile {
		result.FileDigests[rel] = "sha256:" + hexDigest
	}

	return result, nil
}

// checkTierEligibility applies the trust-tiers.json invariants to the
// evidence-derived tier.
func checkTierEligibility(m *Manifest, tier Tier) *Error {
	if m.Kind != "plugin" {
		return nil
	}
	hasWASM := false
	for _, v := range m.Variants {
		switch v.AdmissionProfile {
		case "certified_native":
			if tier < TierT2 {
				return fail(reasonNativeTierIneligible(), "tier-classification", "variant %s declares certified_native but the verified evidence proves only %s", v.VariantID, tier)
			}
		case "platform_reserved":
			if tier != TierT3 {
				return fail(reasonNativeTierIneligible(), "tier-classification", "variant %s declares platform_reserved but the verified evidence proves only %s", v.VariantID, tier)
			}
		}
		if v.ExecutionRuntime == "wasm_component" || v.ExecutionRuntime == "wasm_aot_component" {
			hasWASM = true
		}
	}
	if contract.Invariants.WASMBaselineRequiredT0T1T2 && tier <= TierT2 && !hasWASM {
		return fail(ReasonWASMBaselineMissing, "tier-classification", "the tier contract requires a WASM baseline variant for %s and the manifest declares none", tier)
	}
	return nil
}

// streamBundle performs the single bounded pass over the archive:
// grammar enforcement via the walker, layout admission per member, and
// the running install-root digest. Nothing outside the two JSON planes
// is materialized.
func streamBundle(r io.Reader) (*bundleContents, *Error) {
	gz, verr := newGzipStream(r)
	if verr != nil {
		return nil, verr
	}
	walker := newTarWalker(gz)
	contents := &bundleContents{
		signatures: make(map[string][]byte),
		digest:     newPackageDigest(),
		dirs:       make(map[string]bool),
	}

	for {
		member, done, verr := walker.next()
		if verr != nil {
			return nil, verr
		}
		if done {
			break
		}
		if verr := consumeMember(walker, member, contents); verr != nil {
			return nil, verr
		}
	}
	if verr := gz.finish(); verr != nil {
		return nil, verr
	}
	return contents, nil
}

// consumeMember admits one member into the fixed bundle layout
// (PLUGIN_BUNDLE_FORMAT §3) and consumes its payload. Fail closed:
// a member the layout does not name is rejected, because everything
// outside install-root/ sits outside both hash domains and would
// otherwise ride along unsigned.
func consumeMember(walker *tarWalker, member *tarMember, contents *bundleContents) *Error {
	if contents.root == "" {
		// The walker guarantees the first member is the sole top-level
		// directory.
		contents.root = member.path
		return nil
	}
	rel := strings.TrimPrefix(member.path, contents.root+"/")

	if member.isDir {
		switch {
		case rel == "install-root":
			contents.hasInstallDir = true
		case strings.HasPrefix(rel, "install-root/"):
			// Any grammar-legal directory tree is allowed inside the
			// digest domain.
		case rel == "signatures" || rel == "provenance":
			// Fixed flat directories; their members are checked below.
		default:
			return fail(ReasonEnvelopeMalformed, "layout", "directory %q is not part of the fixed bundle layout", rel)
		}
		contents.dirs[rel] = true
		return nil
	}

	switch {
	case rel == "manifest.json":
		raw, verr := materialize(walker, member)
		if verr != nil {
			return verr
		}
		contents.manifestRaw = raw
	case strings.HasPrefix(rel, "install-root/"):
		h := sha256.New()
		if verr := walker.readPayload(member, h); verr != nil {
			return verr
		}
		var sum [sha256.Size]byte
		h.Sum(sum[:0])
		contents.digest.addFile(strings.TrimPrefix(rel, "install-root/"), sum)
	case strings.HasPrefix(rel, "signatures/"):
		name := strings.TrimPrefix(rel, "signatures/")
		switch name {
		case sigFilePublisherSig, sigFilePublisherCrt, sigFileAttestation, sigFilePlatformSig:
		default:
			return fail(ReasonEnvelopeMalformed, "layout", "signatures/%s is not a recognized trust object", name)
		}
		raw, verr := materialize(walker, member)
		if verr != nil {
			return verr
		}
		contents.signatures[name] = raw
	case strings.HasPrefix(rel, "provenance/"):
		name := strings.TrimPrefix(rel, "provenance/")
		switch name {
		case "built-by.json", "source-url.txt", "reproducible-build.txt":
		default:
			return fail(ReasonEnvelopeMalformed, "layout", "provenance/%s is not a recognized provenance record", name)
		}
		// Provenance is advisory build metadata outside both hash
		// domains — layout-checked, streamed past, never trusted.
		if verr := walker.readPayload(member, nil); verr != nil {
			return verr
		}
	case rel == "README.md":
		if verr := walker.readPayload(member, nil); verr != nil {
			return verr
		}
	default:
		return fail(ReasonEnvelopeMalformed, "layout", "file %q is not part of the fixed bundle layout", rel)
	}
	return nil
}

// materialize reads one small JSON-plane member into memory under the
// per-member ceiling; the size gate runs before any byte is read.
func materialize(walker *tarWalker, member *tarMember) ([]byte, *Error) {
	if member.size > maxJSONMemberBytes {
		return nil, fail(ReasonCeilingExceeded, "layout", "member %q exceeds the %d-byte in-memory ceiling", member.path, int64(maxJSONMemberBytes))
	}
	var buf bytes.Buffer
	if verr := walker.readPayload(member, &buf); verr != nil {
		return nil, verr
	}
	raw := buf.Bytes()
	if raw == nil {
		// A present-but-empty member is still present: keep the byte
		// slice non-nil so presence never degrades into absence.
		raw = []byte{}
	}
	return raw, nil
}

// String renders a Result for operator eyes; private key material never
// exists in this package, and no secret ever reaches a Result.
func (r *Result) String() string {
	s := fmt.Sprintf("%s %s %s\n  package_hash  %s\n  manifest_hash %s",
		r.Tier, r.Manifest.ID, r.Manifest.Version, r.PackageHash, r.ManifestHash)
	if r.PublisherID != "" {
		s += fmt.Sprintf("\n  publisher     %s", r.PublisherID)
	}
	if r.Tier == TierT2 {
		s += fmt.Sprintf("\n  reviewed_capabilities %v", r.ReviewedCapabilities)
	}
	return s
}
