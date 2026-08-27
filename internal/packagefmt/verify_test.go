package packagefmt

// The acceptance currency here is hostile, not happy-path: every trust
// decision is exercised from the attacker's side — tampered bytes,
// self-claimed tiers, wrong keys, wrong kinds, malformed shapes — and
// the only accepted outcome is a typed rejection naming its reason.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustVerify(t *testing.T, pkg []byte, roots TrustRoots) *Result {
	t.Helper()
	result, err := Verify(bytes.NewReader(pkg), roots)
	if err != nil {
		t.Fatalf("verification must succeed: %v", err)
	}
	return result
}

// --- happy paths ---

func TestVerifyT0Unsigned(t *testing.T) {
	f := fixtures(t)
	// No pinned roots at all: a package carrying no trust evidence is
	// exactly T0 — unsigned-beyond-self, installable only by policy.
	result := mustVerify(t, f.t0Pkg(t), TrustRoots{})
	if result.Tier != TierT0 {
		t.Fatalf("tier = %s, want T0", result.Tier)
	}
	if result.PackageHash != f.communityPkgHash || result.ManifestHash != f.communityManHash {
		t.Fatal("recomputed hashes do not match the reference computation")
	}
	if result.PublisherID != "" || result.ReviewedCapabilities != nil {
		t.Fatal("T0 must carry no publisher identity and no reviewed capabilities")
	}
}

func TestVerifyT1PublisherSigned(t *testing.T) {
	f := fixtures(t)
	result := mustVerify(t, f.t1Pkg(t), f.roots)
	if result.Tier != TierT1 {
		t.Fatalf("tier = %s, want T1", result.Tier)
	}
	if result.PublisherID != "org.example" {
		t.Fatalf("publisher identity must come from the certificate, got %q", result.PublisherID)
	}
}

func TestVerifyT2Attested(t *testing.T) {
	f := fixtures(t)
	result := mustVerify(t, f.t2Pkg(t), f.roots)
	if result.Tier != TierT2 {
		t.Fatalf("tier = %s, want T2", result.Tier)
	}
	if len(result.ReviewedCapabilities) != 1 || result.ReviewedCapabilities[0] != "net.outbound:api.example.test" {
		t.Fatalf("reviewed capabilities lost: %v", result.ReviewedCapabilities)
	}
}

func TestVerifyT3PlatformSigned(t *testing.T) {
	f := fixtures(t)
	result := mustVerify(t, f.t3Pkg(t), f.roots)
	if result.Tier != TierT3 {
		t.Fatalf("tier = %s, want T3", result.Tier)
	}
	if result.PublisherID != "" {
		t.Fatal("T3 has no community publisher")
	}
}

func TestVerifyFileReadsFromDisk(t *testing.T) {
	f := fixtures(t)
	path := filepath.Join(t.TempDir(), "echo.aiiospkg")
	if err := os.WriteFile(path, f.t1Pkg(t), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := VerifyFile(path, f.roots)
	if err != nil || result.Tier != TierT1 {
		t.Fatalf("VerifyFile: %v (tier %v)", err, result)
	}
}

// --- tampering after signing ---

func TestVerifyRejectsTamperedArtifactAfterSigning(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
	})
	tampered := map[string][]byte{}
	for k, v := range f.communityFiles {
		tampered[k] = v
	}
	tampered["variants/linux-x86_64-wasm/plugin.wasm"] = []byte("\x00asm\x01\x00\x00\x00EVIL")
	spec.installFiles = tampered
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonPackageHashMismatch)
}

func TestVerifyRejectsTamperedManifestAfterSigning(t *testing.T) {
	f := fixtures(t)
	// Same install-root (package_hash still matches), one extension
	// field added to the manifest: manifest_hash changes, so the
	// publisher's exact-release binding no longer holds.
	var m map[string]interface{}
	if err := json.Unmarshal(f.communityManifest, &m); err != nil {
		t.Fatal(err)
	}
	m["x-injected"] = "tamper"
	raw, _ := json.Marshal(m)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
	})
	spec.manifest = raw
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonQuartetMismatch)
}

func TestVerifyRejectsManifestDeclaredHashTamper(t *testing.T) {
	f := fixtures(t)
	var m map[string]interface{}
	if err := json.Unmarshal(f.communityManifest, &m); err != nil {
		t.Fatal(err)
	}
	m["package_hash"] = "sha256:" + strings16x4()
	raw, _ := json.Marshal(m)
	spec := f.communitySpec(nil)
	spec.manifest = raw
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonPackageHashMismatch)
}

func strings16x4() string {
	s := ""
	for i := 0; i < 64; i++ {
		s += "0"
	}
	return s
}

func TestVerifyRejectsTamperedSignaturePayload(t *testing.T) {
	f := fixtures(t)
	// Rewrite the signed payload hashes inside publisher.sig: the
	// envelope's payload_sha256 no longer matches, and nothing verifies.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(f.publisherSig, &env); err != nil {
		t.Fatal(err)
	}
	env["payload"] = json.RawMessage(`{"manifest_hash":"sha256:` + strings16x4() + `","package_hash":"sha256:` + strings16x4() + `"}`)
	tampered, _ := json.Marshal(env)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: tampered,
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonPublisherSigInvalid)
}

// --- envelope grammar abuse ---

func flipEnvelopeField(t *testing.T, envBytes []byte, field, value string) []byte {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(envBytes, &env); err != nil {
		t.Fatal(err)
	}
	env[field] = json.RawMessage(`"` + value + `"`)
	out, _ := json.Marshal(env)
	return out
}

func TestVerifyRejectsWrongArtifactKind(t *testing.T) {
	f := fixtures(t)
	// A validly signed envelope presented under the wrong kind must
	// fail — kind is part of the signed input, not advisory labeling.
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes,
		sigFilePublisherSig: flipEnvelopeField(t, f.publisherSig, "artifact_kind", artifactKindPlatformSig),
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonPublisherSigInvalid)
}

func TestVerifyRejectsWrongProfile(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes,
		sigFilePublisherSig: flipEnvelopeField(t, f.publisherSig, "signature_profile", "AIII-PQ-SIGNATURE-V1-FAST"),
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonPublisherSigInvalid)
}

func TestVerifyRejectsWidenedSignaturePayload(t *testing.T) {
	f := fixtures(t)
	// The closed payload rule: an extra member in {package_hash,
	// manifest_hash} is malformed even if the two hashes are right.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(f.publisherSig, &env); err != nil {
		t.Fatal(err)
	}
	env["payload"] = json.RawMessage(`{"extra":"x","manifest_hash":"` + f.communityManHash + `","package_hash":"` + f.communityPkgHash + `"}`)
	widened, _ := json.Marshal(env)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: widened,
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonPublisherSigInvalid)
}

// --- trust-object shape ---

func TestVerifyRejectsPartialPublisherPair(t *testing.T) {
	f := fixtures(t)
	for _, sigs := range []map[string][]byte{
		{sigFilePublisherCrt: f.certBytes},
		{sigFilePublisherSig: f.publisherSig},
	} {
		_, err := Verify(bytes.NewReader(buildPkg(t, f.communitySpec(sigs))), f.roots)
		expectReason(t, err, ReasonTrustObjectShape)
	}
}

func TestVerifyRejectsAttestationWithoutPair(t *testing.T) {
	f := fixtures(t)
	_, err := Verify(bytes.NewReader(buildPkg(t, f.communitySpec(map[string][]byte{
		sigFileAttestation: f.attestation,
	}))), f.roots)
	expectReason(t, err, ReasonTrustObjectShape)
}

func TestVerifyRejectsEmptyPlatformSigOnCommunityPackage(t *testing.T) {
	f := fixtures(t)
	// A present-but-empty platform.sig is present invalid evidence — it
	// must poison the community package, never read as absence.
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
		sigFilePlatformSig: {},
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonTrustObjectShape)
}

func TestVerifyRejectsMixedT3Community(t *testing.T) {
	f := fixtures(t)
	// A platform proof mixed with community objects is malformed — not
	// an ambiguous tier (§4: the T3 row is intentionally not additive).
	pkg := buildPkg(t, pkgSpec{
		root:         f.platformID + "-" + f.platformVersion,
		manifest:     f.platformManifest,
		installFiles: f.platformFiles,
		signatures: map[string][]byte{
			sigFilePlatformSig:  f.platformSig,
			sigFilePublisherCrt: f.certBytes,
			sigFilePublisherSig: f.publisherSig,
		},
	})
	_, err := Verify(bytes.NewReader(pkg), f.roots)
	expectReason(t, err, ReasonTrustObjectShape)
}

// --- quartet binding ---

func TestVerifyRejectsAttestationQuartetMismatch(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
		sigFileAttestation: f.attestationWrong,
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonQuartetMismatch)
}

func TestVerifyRejectsRootDirManifestMismatch(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(nil)
	spec.root = f.communityID + "-9.9.9"
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonEnvelopeMalformed)
}

// --- key discipline ---

func TestVerifyRejectsMissingRootsWithEvidence(t *testing.T) {
	f := fixtures(t)
	// Evidence present, no pinned root: unverifiable is not unsigned.
	_, err := Verify(bytes.NewReader(f.t1Pkg(t)), TrustRoots{})
	expectReason(t, err, ReasonTrustRootUnavailable)
	_, err = Verify(bytes.NewReader(f.t3Pkg(t)), TrustRoots{})
	expectReason(t, err, ReasonTrustRootUnavailable)
	// Reviewer root missing while the certifier domain is COMPLETE
	// (root + snapshot): the publisher chain proves, then the
	// attestation's missing root refuses — the reason names the root,
	// not the snapshot.
	partialRoots := TrustRoots{PublisherCertifier: f.certifier}
	set, lerr := loadStatusSet(map[string][]byte{
		"aiii_plugin_publisher_certifier_status.json": f.statusCertifier,
	}, partialRoots, nil)
	if lerr != nil {
		t.Fatal(lerr)
	}
	partialRoots.Revocation = set
	_, err = Verify(bytes.NewReader(f.t2Pkg(t)), partialRoots)
	expectReason(t, err, ReasonTrustRootUnavailable)
}

func TestVerifyRejectsCrossDomainRoots(t *testing.T) {
	f := fixtures(t)
	// Key separation (TRUST_AND_SIGNING §1.5): a reviewer key standing
	// in as the certifier must fail even before signature math — the
	// domains are separate keys by contract.
	_, err := Verify(bytes.NewReader(f.t1Pkg(t)), TrustRoots{
		PublisherCertifier: f.reviewer,
	})
	expectReason(t, err, ReasonPublisherCertInvalid)
}

func TestVerifyRejectsWrongCertifierKey(t *testing.T) {
	f := fixtures(t)
	// A different certifier keypair (right key_type, wrong key): the
	// certificate signature cannot verify.
	_, verr := Verify(bytes.NewReader(f.t1Pkg(t)), TrustRoots{
		PublisherCertifier: f.otherCertifier,
	})
	expectReason(t, verr, ReasonPublisherCertInvalid)
}

func TestVerifyRejectsExpiredPublisherKey(t *testing.T) {
	f := fixtures(t)
	// The certificate verifies, but the embedded publisher key envelope
	// is expired — the key envelope owns the validity window (§3.1).
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certExpiredKey, sigFilePublisherSig: f.publisherSig,
	})
	_, err := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, err, ReasonPublisherCertInvalid)
}

func TestVerifyRejectsManifestEmbeddedPublisherKey(t *testing.T) {
	f := fixtures(t)
	// The certificate is the SOLE publisher-key source; a manifest that
	// carries one is schema-invalid, not politely ignored.
	var m map[string]interface{}
	if err := json.Unmarshal(f.communityManifest, &m); err != nil {
		t.Fatal(err)
	}
	m["publisher_key"] = map[string]string{"alg": "ML-DSA-87", "public_key_b64": "AAAA"}
	raw, _ := json.Marshal(m)
	spec := f.communitySpec(nil)
	spec.manifest = raw
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonManifestInvalid)
}

// --- tier eligibility from evidence ---

func selfElevatedManifest(t *testing.T, f *fixtureSet, runtime, profile string) pkgSpec {
	t.Helper()
	files := map[string][]byte{}
	for k, v := range f.communityFiles {
		files[k] = v
	}
	files["variants/linux-x86_64-native/plugin.bin"] = []byte("\x7fELFnative")
	manifest := buildManifestJSON(f.communityID, f.communityVersion,
		[]variantSpec{
			{id: "linux-x86_64-wasm", platform: "linux", arch: "x86_64",
				topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
				entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"},
			{id: "linux-x86_64-native", platform: "linux", arch: "x86_64",
				topology: "full_identity_host", runtime: runtime, profile: profile,
				entrypoint: "variants/linux-x86_64-native/plugin.bin"},
		},
		files, nil)
	return pkgSpec{
		root:         f.communityID + "-" + f.communityVersion,
		manifest:     manifest,
		installFiles: files,
	}
}

func TestVerifyRejectsSelfElevatedCertifiedNative(t *testing.T) {
	f := fixtures(t)
	// certified_native without T2 evidence: the manifest claims more
	// than its signatures prove — rejected with the contract's own
	// reason string, never downgraded.
	spec := selfElevatedManifest(t, f, "service_process", "certified_native")
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), reasonNativeTierIneligible())
}

func TestVerifyRejectsSelfElevatedPlatformReserved(t *testing.T) {
	f := fixtures(t)
	spec := selfElevatedManifest(t, f, "native_t3_component", "platform_reserved")
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), reasonNativeTierIneligible())
}

func TestVerifyRejectsMissingWASMBaseline(t *testing.T) {
	f := fixtures(t)
	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/linux-x86_64-svc/plugin.bin":      []byte("\x7fELFsvc"),
	}
	manifest := buildManifestJSON(f.communityID, f.communityVersion,
		[]variantSpec{{id: "linux-x86_64-svc", platform: "linux", arch: "x86_64",
			topology: "full_identity_host", runtime: "service_process", profile: "standard",
			entrypoint: "variants/linux-x86_64-svc/plugin.bin"}},
		files, nil)
	pkg := buildPkg(t, pkgSpec{
		root: f.communityID + "-" + f.communityVersion, manifest: manifest, installFiles: files,
	})
	expectReason(t, verifyBytes(t, pkg), ReasonWASMBaselineMissing)
}

func TestVerifyRejectsSignedWidenedPayloadAtClosedGate(t *testing.T) {
	f := fixtures(t)
	// The attack strictDecode exists for: a publisher SIGNS a widened
	// payload with their valid key. Signature verifies, payload_sha256
	// matches — the closed-payload rule (TRUST_AND_SIGNING §3.2
	// "contains exactly") is the only gate left standing. The older
	// TestVerifyRejectsWidenedSignaturePayload tampers post-signing and
	// dies at the hash check instead; this one proves the gate itself.
	spec := f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherWidened,
	})
	_, verr := Verify(bytes.NewReader(buildPkg(t, spec)), f.roots)
	expectReason(t, verr, ReasonPublisherSigInvalid)
	if !strings.Contains(verr.Error(), "closed") {
		t.Fatalf("rejection must come from the closed-payload gate, not signature/hash checks; got: %v", verr)
	}
}

func assetManifest(files map[string][]byte, mutate func(map[string]interface{})) []byte {
	m := map[string]interface{}{
		"kind": "asset", "id": "org.example.pack", "version": "0.1.0",
		"package_hash": referencePackageHash(files), "asset_type": "prompt_pack",
	}
	if mutate != nil {
		mutate(m)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestVerifyAssetT0(t *testing.T) {
	// kind=asset is a supported manifest kind: no interfaces/variants
	// shape requirements, no WASM-baseline demand — the plugin-only
	// checks are gated by kind, and an asset must still verify.
	files := map[string][]byte{"prompts/hello.md": []byte("# hello\n")}
	pkg := buildPkg(t, pkgSpec{
		root: "org.example.pack-0.1.0", manifest: assetManifest(files, nil), installFiles: files,
	})
	result := mustVerify(t, pkg, TrustRoots{})
	if result.Tier != TierT0 {
		t.Fatalf("tier = %s, want T0", result.Tier)
	}
}

func TestVerifyRejectsAssetWithVariants(t *testing.T) {
	files := map[string][]byte{"prompts/hello.md": []byte("# hello\n")}
	manifest := assetManifest(files, func(m map[string]interface{}) {
		m["variants"] = []interface{}{}
	})
	pkg := buildPkg(t, pkgSpec{
		root: "org.example.pack-0.1.0", manifest: manifest, installFiles: files,
	})
	expectReason(t, verifyBytes(t, pkg), ReasonManifestInvalid)
}

func TestVerifyRejectsAssetMissingAssetType(t *testing.T) {
	files := map[string][]byte{"prompts/hello.md": []byte("# hello\n")}
	manifest := assetManifest(files, func(m map[string]interface{}) {
		delete(m, "asset_type")
	})
	pkg := buildPkg(t, pkgSpec{
		root: "org.example.pack-0.1.0", manifest: manifest, installFiles: files,
	})
	expectReason(t, verifyBytes(t, pkg), ReasonManifestInvalid)
}

func TestVerifyFileMissingPath(t *testing.T) {
	_, err := VerifyFile(filepath.Join(t.TempDir(), "nope.aiiospkg"), TrustRoots{})
	expectReason(t, err, ReasonEnvelopeMalformed)
}

func TestVerifyFileDirectoryPath(t *testing.T) {
	// A directory opens fine; the failure must still surface as a
	// typed *Error from the envelope layer, never a raw os error.
	_, err := VerifyFile(t.TempDir(), TrustRoots{})
	var perr *Error
	if err == nil || !errors.As(err, &perr) {
		t.Fatalf("want a typed *Error for a directory path, got %v", err)
	}
}

func TestDigestSubtreeOrderTheorem(t *testing.T) {
	f := fixtures(t)
	// The digest folds install-root files in ARCHIVE order and claims
	// (digest.go addFile) that this equals PACKAGE_DIGEST §2.4 relpath
	// byte order — a theorem while both sides are plain byte
	// comparisons. Pin it with the discriminating shape: '-' (0x2D)
	// sorts before '/' (0x2F), so "variants/aa-c/plugin.wasm" precedes
	// "variants/aa/plugin.wasm" bytewise even though a tree walk would
	// emit the aa subtree first. referencePackageHash sorts relpaths
	// independently; a walker that ever switched to tree order would
	// mismatch it here.
	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/aa-c/plugin.wasm":                 []byte("\x00asm\x01\x00\x00\x00later"),
		"variants/aa/plugin.wasm":                   []byte("\x00asm\x01\x00\x00\x00early"),
	}
	manifest := buildManifestJSON(f.communityID, f.communityVersion,
		[]variantSpec{
			{id: "aa", platform: "linux", arch: "x86_64",
				topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
				entrypoint: "variants/aa/plugin.wasm"},
			{id: "aa-c", platform: "linux", arch: "arm64",
				topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
				entrypoint: "variants/aa-c/plugin.wasm"},
		},
		files, nil)
	pkg := buildPkg(t, pkgSpec{
		root: f.communityID + "-" + f.communityVersion, manifest: manifest, installFiles: files,
	})
	result := mustVerify(t, pkg, TrustRoots{})
	if want := referencePackageHash(files); result.PackageHash != want {
		t.Fatalf("archive-order digest diverged from §2.4 sorted-relpath digest:\n got %s\nwant %s", result.PackageHash, want)
	}
}

func TestVerifyT0ServiceProcessBesideWASMBaseline(t *testing.T) {
	f := fixtures(t)
	// trust-tiers.json allows service_process+standard at T0+ — as a
	// variant BESIDE the required WASM baseline, never instead of it
	// (the wasm-baseline invariant holds for T0–T2 regardless;
	// TestVerifyRejectsMissingWASMBaseline covers the instead-of case).
	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":    []byte("\x00asm\x01\x00\x00\x00echo"),
		"variants/linux-x86_64-svc/plugin.bin":      []byte("\x7fELFsvc"),
	}
	manifest := buildManifestJSON(f.communityID, f.communityVersion,
		[]variantSpec{
			{id: "linux-x86_64-wasm", platform: "linux", arch: "x86_64",
				topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
				entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"},
			{id: "linux-x86_64-svc", platform: "linux", arch: "x86_64",
				topology: "full_identity_host", runtime: "service_process", profile: "standard",
				entrypoint: "variants/linux-x86_64-svc/plugin.bin"},
		},
		files, nil)
	pkg := buildPkg(t, pkgSpec{
		root: f.communityID + "-" + f.communityVersion, manifest: manifest, installFiles: files,
	})
	result := mustVerify(t, pkg, TrustRoots{})
	if result.Tier != TierT0 {
		t.Fatalf("tier = %s, want T0", result.Tier)
	}
}

// --- shape checks needing the manifest ---

func TestVerifyRejectsVariantArtifactHashMismatch(t *testing.T) {
	f := fixtures(t)
	var m map[string]interface{}
	if err := json.Unmarshal(f.communityManifest, &m); err != nil {
		t.Fatal(err)
	}
	variants := m["variants"].([]interface{})
	variants[0].(map[string]interface{})["artifact_hash"] = "sha256:" + strings16x4()
	raw, _ := json.Marshal(m)
	spec := f.communitySpec(nil)
	spec.manifest = raw
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonVariantIntegrity)
}

func TestVerifyRejectsMissingDeclaredVariantDir(t *testing.T) {
	f := fixtures(t)
	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
	}
	spec := f.communitySpec(nil)
	spec.installFiles = files // variant dir + entrypoint gone; manifest unchanged
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsEmptyInstallRoot(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(nil)
	spec.installFiles = map[string][]byte{}
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonEnvelopeMalformed)
}

func TestVerifyRejectsMissingManifest(t *testing.T) {
	f := fixtures(t)
	spec := f.communitySpec(nil)
	spec.manifest = nil
	expectReason(t, verifyBytes(t, buildPkg(t, spec)), ReasonManifestInvalid)
}
