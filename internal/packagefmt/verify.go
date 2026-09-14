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
package packagefmt

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// .
// .
type Result struct {
	Tier         Tier
	Manifest     *Manifest
	PackageHash  string
	ManifestHash string
	// .
	// .
	PublisherID string
	// .
	ReviewedCapabilities []string
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	FileDigests map[string]string
}

// .
func VerifyFile(path string, roots TrustRoots) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fail(ReasonEnvelopeMalformed, "open", "%v", err)
	}
	defer f.Close()
	return Verify(f, roots)
}

// .
// .
// .
// .
type bundleContents struct {
	root          string
	manifestRaw   []byte
	signatures    map[string][]byte
	digest        *packageDigest
	dirs          map[string]bool
	hasInstallDir bool
}

// .
// .
// .
// .
func Verify(r io.Reader, roots TrustRoots) (*Result, error) {
	contents, verr := streamBundle(r)
	if verr != nil {
		return nil, verr
	}

	// .
	// .
	if contents.manifestRaw == nil {
		return nil, fail(ReasonManifestInvalid, "shape", "bundle has no manifest.json")
	}

	// .
	m, verr := parseManifest(contents.manifestRaw)
	if verr != nil {
		return nil, verr
	}

	// .
	// .
	// .
	// .
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

	// .
	manifestHashValue, verr := manifestHash(contents.manifestRaw)
	if verr != nil {
		return nil, verr
	}

	// .
	packageHashValue := contents.digest.sum()
	if m.PackageHash != packageHashValue {
		return nil, fail(ReasonPackageHashMismatch, "package-hash", "manifest declares %s, install-root hashes to %s", m.PackageHash, packageHashValue)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
	result := &Result{
		Manifest:     m,
		PackageHash:  packageHashValue,
		ManifestHash: manifestHashValue,
		Tier:         TierT0,
	}
	switch {
	case hasPlat:
		if !contract.Invariants.T3RequiresPlatformReleaseSig {
			// .
			// .
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

	// .
	// .
	// .
	for _, v := range m.Variants {
		if got := "sha256:" + contents.digest.perFile[v.Entrypoint]; got != v.ArtifactHash {
			return nil, fail(ReasonVariantIntegrity, "variant-integrity", "variant %s artifact_hash %s does not match packaged entrypoint digest %s", v.VariantID, v.ArtifactHash, got)
		}
	}

	// .
	// .
	// .
	if verr := checkTierEligibility(m, result.Tier); verr != nil {
		return nil, verr
	}

	// .
	// .
	result.FileDigests = make(map[string]string, len(contents.digest.perFile))
	for rel, hexDigest := range contents.digest.perFile {
		result.FileDigests[rel] = "sha256:" + hexDigest
	}

	return result, nil
}

// .
// .
func checkTierEligibility(m *Manifest, tier Tier) *Error {
	if m.Kind != "plugin" {
		return nil
	}
	hasWASM := false
	for _, v := range m.Variants {
		if v.ExecutionRuntime == "wasm_component" || v.ExecutionRuntime == "wasm_aot_component" {
			hasWASM = true
		}
	}
	// .
	// .
	if contract.Invariants.WASMBaselineRequiredT0T1T2 && tier <= TierT2 && !hasWASM {
		return fail(ReasonWASMBaselineMissing, "tier-classification", "the tier contract requires a WASM baseline variant for %s and the manifest declares none", tier)
	}
	for _, v := range m.Variants {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if v.AdmissionProfile == "certified_native" || v.AdmissionProfile == "platform_reserved" || isNativeRuntime(v.ExecutionRuntime) {
			if tier != TierT3 {
				return fail(reasonNativeTierIneligible(), "tier-classification", "variant %s is native (%s, %s) and native is T3 only, but the verified evidence proves %s", v.VariantID, v.AdmissionProfile, v.ExecutionRuntime, tier)
			}
		}
	}
	return nil
}

// .
// .
// .
func isNativeRuntime(runtime string) bool {
	return runtime == "service_process" || runtime == "native_t3_component"
}

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
func consumeMember(walker *tarWalker, member *tarMember, contents *bundleContents) *Error {
	if contents.root == "" {
		// .
		// .
		contents.root = member.path
		return nil
	}
	rel := strings.TrimPrefix(member.path, contents.root+"/")

	if member.isDir {
		switch {
		case rel == "install-root":
			contents.hasInstallDir = true
		case strings.HasPrefix(rel, "install-root/"):
			// .
			// .
		case rel == "signatures" || rel == "provenance":
			// .
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
		// .
		// .
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

// .
// .
func materialize(walker *tarWalker, member *tarMember) ([]byte, *Error) {
	return materializeUpTo(walker, member, maxJSONMemberBytes)
}

// .
// .
// .
// .
// .
// .
func materializeUpTo(walker *tarWalker, member *tarMember, ceiling int64) ([]byte, *Error) {
	if member.size > ceiling {
		return nil, fail(ReasonCeilingExceeded, "layout", "member %q exceeds the %d-byte in-memory ceiling", member.path, ceiling)
	}
	var buf bytes.Buffer
	if verr := walker.readPayload(member, &buf); verr != nil {
		return nil, verr
	}
	raw := buf.Bytes()
	if raw == nil {
		// .
		// .
		raw = []byte{}
	}
	return raw, nil
}

// .
// .
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
