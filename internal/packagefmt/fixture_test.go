package packagefmt

// .
// .
// .
// .
// .

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .

type variantSpec struct {
	id, platform, arch, topology, runtime, profile string
	entrypoint                                     string
}

// .
// .
// .
func buildManifestJSON(id, version string, variants []variantSpec, installFiles map[string][]byte, extraTop map[string]interface{}) []byte {
	ifaceHash := "sha256:" + sha256Hex(installFiles["interfaces/channel.control.v1.schema.json"])
	var vlist []map[string]interface{}
	for _, v := range variants {
		vlist = append(vlist, map[string]interface{}{
			"variant_id": v.id, "platform": v.platform, "arch": v.arch,
			"topology": v.topology, "execution_runtime": v.runtime,
			"admission_profile": v.profile, "entrypoint": v.entrypoint,
			"artifact_hash":        "sha256:" + sha256Hex(installFiles[v.entrypoint]),
			"implements":           map[string]interface{}{"core": []string{"channel.control@1"}},
			"variant_capabilities": []string{"net.outbound:api.example.test"},
		})
	}
	m := map[string]interface{}{
		"kind": "plugin", "id": id, "version": version,
		"package_hash":  referencePackageHash(installFiles),
		"plugin_family": "tool_bridge", "bbb_protocol_version": 2,
		"interfaces": map[string]interface{}{
			"core": []map[string]interface{}{{
				"id": "channel.control", "version": 1,
				"schema_hash": ifaceHash, "methods": []string{"channel.open"},
			}},
		},
		"capability_envelope": []string{"net.outbound:api.example.test"},
		"variants":            vlist,
	}
	for k, v := range extraTop {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return raw
}

// .
// .
// .
// .
func referencePackageHash(files map[string][]byte) string {
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	agg := sha256.New()
	for _, p := range paths {
		agg.Write([]byte(p))
		agg.Write([]byte{0})
		agg.Write([]byte(sha256Hex(files[p])))
		agg.Write([]byte{'\n'})
	}
	return fmt.Sprintf("sha256:%x", agg.Sum(nil))
}

// .
// .
func referenceManifestHash(manifestRaw []byte) string {
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

type pkgSpec struct {
	root         string
	manifest     []byte
	installFiles map[string][]byte
	signatures   map[string][]byte
	provenance   map[string][]byte
	readme       []byte
}

// .
// .
func packageMembers(spec pkgSpec) []memberSpec {
	dirs := map[string]bool{spec.root: true}
	var files []memberSpec
	addFile := func(rel string, content []byte) {
		full := spec.root + "/" + rel
		for i := len(spec.root) + 1; i < len(full); i++ {
			if full[i] == '/' {
				dirs[full[:i]] = true
			}
		}
		files = append(files, memberSpec{path: full, content: content})
	}
	if spec.manifest != nil {
		addFile("manifest.json", spec.manifest)
	}
	dirs[spec.root+"/install-root"] = true
	for rel, content := range spec.installFiles {
		addFile("install-root/"+rel, content)
	}
	if len(spec.signatures) > 0 {
		dirs[spec.root+"/signatures"] = true
	}
	for name, content := range spec.signatures {
		addFile("signatures/"+name, content)
	}
	if len(spec.provenance) > 0 {
		dirs[spec.root+"/provenance"] = true
	}
	for name, content := range spec.provenance {
		addFile("provenance/"+name, content)
	}
	if spec.readme != nil {
		addFile("README.md", spec.readme)
	}
	var specs []memberSpec
	for d := range dirs {
		specs = append(specs, memberSpec{path: d, isDir: true})
	}
	specs = append(specs, files...)
	return sortSpecs(specs)
}

func buildPkg(t *testing.T, spec pkgSpec) []byte {
	t.Helper()
	return gzipWrap(t, writeCanonicalTar(t, packageMembers(spec)))
}

// .

type fixtureSet struct {
	certifier, publisher, reviewer, platform *sigenvelope.PublicKeyEnvelope
	otherCertifier                           *sigenvelope.PublicKeyEnvelope

	roots TrustRoots

	communityID, communityVersion string
	communityFiles                map[string][]byte
	communityManifest             []byte
	communityPkgHash              string
	communityManHash              string

	certBytes        []byte
	certExpiredKey   []byte
	publisherSig     []byte
	publisherWidened []byte
	attestation      []byte
	attestationWrong []byte

	// .
	// .
	// .
	// .
	statusCertifier, statusReviewer, statusPlatform []byte
	statusSingle                                    []byte
	statusEpoch3, statusFork3                       []byte
	statusMalformed                                 map[string]json.RawMessage
	statusRevokeManifest, statusRevokeCert          []byte
	statusRevokeAttestation, statusRevokePlatform   []byte

	platformID, platformVersion string
	platformFiles               map[string][]byte
	platformManifest            []byte
	platformSig                 []byte

	err error
}

// .
// .
// .
type signedVectorBundle struct {
	SchemaVersion int `json:"schema_version"`

	Certifier      *sigenvelope.PublicKeyEnvelope `json:"certifier"`
	Publisher      *sigenvelope.PublicKeyEnvelope `json:"publisher"`
	Reviewer       *sigenvelope.PublicKeyEnvelope `json:"reviewer"`
	Platform       *sigenvelope.PublicKeyEnvelope `json:"platform"`
	OtherCertifier *sigenvelope.PublicKeyEnvelope `json:"other_certifier"`

	CertBytes        json.RawMessage `json:"publisher_certificate"`
	CertExpiredKey   json.RawMessage `json:"expired_publisher_certificate"`
	PublisherSig     json.RawMessage `json:"publisher_signature"`
	PublisherWidened json.RawMessage `json:"widened_publisher_signature"`
	Attestation      json.RawMessage `json:"reviewer_attestation"`
	AttestationWrong json.RawMessage `json:"wrong_plugin_attestation"`
	PlatformSig      json.RawMessage `json:"platform_signature"`

	StatusCertifier json.RawMessage `json:"certifier_status"`
	StatusReviewer  json.RawMessage `json:"reviewer_status"`
	StatusPlatform  json.RawMessage `json:"platform_status"`
	StatusSingle    json.RawMessage `json:"single_entry_status"`
	StatusEpoch3    json.RawMessage `json:"epoch_3_status"`
	StatusFork3     json.RawMessage `json:"forked_epoch_3_status"`

	StatusMalformed         map[string]json.RawMessage `json:"malformed_statuses"`
	StatusRevokeManifest    json.RawMessage            `json:"revoke_manifest_status"`
	StatusRevokeCert        json.RawMessage            `json:"revoke_certificate_status"`
	StatusRevokeAttestation json.RawMessage            `json:"revoke_attestation_status"`
	StatusRevokePlatform    json.RawMessage            `json:"revoke_platform_status"`
}

var (
	fixturesOnce sync.Once
	fixturesVal  *fixtureSet
)

func fixtures(t *testing.T) *fixtureSet {
	t.Helper()
	fixturesOnce.Do(func() { fixturesVal = loadVerifierFixtures() })
	if fixturesVal.err != nil {
		t.Fatalf("fixture load failed: %v", fixturesVal.err)
	}
	return fixturesVal
}

func loadVerifierFixtures() *fixtureSet {
	f := &fixtureSet{}
	raw, err := os.ReadFile(filepath.Join("testdata", "signed-verifier-vectors-v1.json"))
	if err != nil {
		f.err = err
		return f
	}
	var v signedVectorBundle
	if err := strictDecode(raw, &v); err != nil {
		f.err = err
		return f
	}
	if err := validateSignedVectorBundle(&v); err != nil {
		f.err = err
		return f
	}

	populateUnsignedFixture(f)
	f.certifier, f.publisher = v.Certifier, v.Publisher
	f.reviewer, f.platform = v.Reviewer, v.Platform
	f.otherCertifier = v.OtherCertifier
	f.certBytes, f.certExpiredKey = v.CertBytes, v.CertExpiredKey
	f.publisherSig, f.publisherWidened = v.PublisherSig, v.PublisherWidened
	f.attestation, f.attestationWrong = v.Attestation, v.AttestationWrong
	f.platformSig = v.PlatformSig
	f.statusCertifier, f.statusReviewer, f.statusPlatform = v.StatusCertifier, v.StatusReviewer, v.StatusPlatform
	f.statusSingle = v.StatusSingle
	f.statusEpoch3, f.statusFork3 = v.StatusEpoch3, v.StatusFork3
	f.statusMalformed = v.StatusMalformed
	f.statusRevokeManifest, f.statusRevokeCert = v.StatusRevokeManifest, v.StatusRevokeCert
	f.statusRevokeAttestation, f.statusRevokePlatform = v.StatusRevokeAttestation, v.StatusRevokePlatform

	f.roots = TrustRoots{
		PublisherCertifier: f.certifier,
		Reviewer:           f.reviewer,
		PlatformRelease:    f.platform,
	}
	f.roots.Revocation, f.err = loadStatusSet(map[string][]byte{
		"aiii_plugin_publisher_certifier_status.json": f.statusCertifier,
		"aiii_plugin_reviewer_status.json":            f.statusReviewer,
		"aiii_platform_release_status.json":           f.statusPlatform,
	}, f.roots, nil)
	return f
}

func validateSignedVectorBundle(v *signedVectorBundle) error {
	if v.SchemaVersion != 1 {
		return fmt.Errorf("signed verifier vector schema_version = %d, want 1", v.SchemaVersion)
	}
	public := map[string]*sigenvelope.PublicKeyEnvelope{
		"certifier": v.Certifier, "publisher": v.Publisher, "reviewer": v.Reviewer,
		"platform": v.Platform, "other_certifier": v.OtherCertifier,
	}
	for name, env := range public {
		if env == nil {
			return fmt.Errorf("signed verifier vector %s public key is missing", name)
		}
	}
	signed := map[string][]byte{
		"publisher_certificate": v.CertBytes, "expired_publisher_certificate": v.CertExpiredKey,
		"publisher_signature": v.PublisherSig, "widened_publisher_signature": v.PublisherWidened,
		"reviewer_attestation": v.Attestation, "wrong_plugin_attestation": v.AttestationWrong,
		"platform_signature": v.PlatformSig, "certifier_status": v.StatusCertifier,
		"reviewer_status": v.StatusReviewer, "platform_status": v.StatusPlatform,
		"single_entry_status": v.StatusSingle,
		"epoch_3_status":      v.StatusEpoch3, "forked_epoch_3_status": v.StatusFork3,
		"revoke_manifest_status": v.StatusRevokeManifest, "revoke_certificate_status": v.StatusRevokeCert,
		"revoke_attestation_status": v.StatusRevokeAttestation, "revoke_platform_status": v.StatusRevokePlatform,
	}
	for name, raw := range signed {
		if len(raw) == 0 {
			return fmt.Errorf("signed verifier vector %s is missing", name)
		}
	}
	if len(v.StatusMalformed) == 0 {
		return fmt.Errorf("signed verifier malformed status vectors are missing")
	}
	return nil
}

func populateUnsignedFixture(f *fixtureSet) {

	// .
	f.communityID, f.communityVersion = "org.example.echo", "0.1.0"
	longName := "long-" + strings.Repeat("n", 120) + ".bin"
	f.communityFiles = map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":    []byte("\x00asm\x01\x00\x00\x00echo"),
		"variants/linux-x86_64-wasm/variant.json":   []byte(`{}`),
		"variants/linux-x86_64-wasm/" + longName:    []byte("pax payload"),
	}
	f.communityManifest = buildManifestJSON(f.communityID, f.communityVersion,
		[]variantSpec{{
			id: "linux-x86_64-wasm", platform: "linux", arch: "x86_64",
			topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
			entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		f.communityFiles, nil)
	f.communityPkgHash = referencePackageHash(f.communityFiles)
	f.communityManHash = referenceManifestHash(f.communityManifest)

	// .
	f.platformID, f.platformVersion = "aiii.voice", "0.2.0"
	f.platformFiles = map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/linux-x86_64-native/aii-voice":    []byte("\x7fELFvoice"),
	}
	f.platformManifest = buildManifestJSON(f.platformID, f.platformVersion,
		[]variantSpec{{
			id: "linux-x86_64-native", platform: "linux", arch: "x86_64",
			topology: "full_identity_host", runtime: "native_t3_component", profile: "platform_reserved",
			entrypoint: "variants/linux-x86_64-native/aii-voice",
		}},
		f.platformFiles, nil)
}

// .
// .
// .
func statusPayload(epoch int64, entries ...RevokedEntry) map[string]interface{} {
	if entries == nil {
		entries = []RevokedEntry{}
	}
	return map[string]interface{}{
		"schema_version": 1, "trust_epoch": epoch, "revoked": entries,
	}
}

// .
// .
// .
// .
func loadStatusSet(files map[string][]byte, roots TrustRoots, guard EpochGuard) (*RevocationStatusSet, error) {
	dir, err := os.MkdirTemp("", "aii-trustdir-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for name, raw := range files {
		if raw == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			return nil, err
		}
	}
	return LoadRevocationStatus(dir, roots, guard), nil
}

// .
// .
// .
func (f *fixtureSet) rootsWithStatus(t *testing.T, files map[string][]byte, guard EpochGuard) TrustRoots {
	t.Helper()
	roots := f.roots
	set, err := loadStatusSet(files, roots, guard)
	if err != nil {
		t.Fatalf("status set load: %v", err)
	}
	roots.Revocation = set
	return roots
}

// .
// .
func (f *fixtureSet) communitySpec(signatures map[string][]byte) pkgSpec {
	return pkgSpec{
		root:         f.communityID + "-" + f.communityVersion,
		manifest:     f.communityManifest,
		installFiles: f.communityFiles,
		signatures:   signatures,
		provenance:   map[string][]byte{"built-by.json": []byte(`{"built_by":"publisher"}`)},
		readme:       []byte("# echo\n"),
	}
}

func (f *fixtureSet) t0Pkg(t *testing.T) []byte {
	return buildPkg(t, f.communitySpec(nil))
}

func (f *fixtureSet) t1Pkg(t *testing.T) []byte {
	return buildPkg(t, f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
	}))
}

func (f *fixtureSet) t2Pkg(t *testing.T) []byte {
	return buildPkg(t, f.communitySpec(map[string][]byte{
		sigFilePublisherCrt: f.certBytes, sigFilePublisherSig: f.publisherSig,
		sigFileAttestation: f.attestation,
	}))
}

func (f *fixtureSet) t3Pkg(t *testing.T) []byte {
	return buildPkg(t, pkgSpec{
		root:         f.platformID + "-" + f.platformVersion,
		manifest:     f.platformManifest,
		installFiles: f.platformFiles,
		signatures:   map[string][]byte{sigFilePlatformSig: f.platformSig},
	})
}
