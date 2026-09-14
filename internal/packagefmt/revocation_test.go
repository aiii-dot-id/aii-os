package packagefmt

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
	"path/filepath"
	"strings"
	"testing"
)

const (
	statusFileCertifier = "aiii_plugin_publisher_certifier_status.json"
	statusFileReviewer  = "aiii_plugin_reviewer_status.json"
	statusFilePlatform  = "aiii_platform_release_status.json"
)

// .
// .
type fakeGuard struct {
	epochs  map[string]int64
	shas    map[string]string
	accepts int
	failMsg string
}

func newFakeGuard() *fakeGuard {
	return &fakeGuard{epochs: map[string]int64{}, shas: map[string]string{}}
}

func (g *fakeGuard) TrustEpochHighWater(root string) (int64, string, bool, error) {
	e, ok := g.epochs[root]
	return e, g.shas[root], ok, nil
}

func (g *fakeGuard) AcceptTrustEpoch(root string, epoch int64, sha string) error {
	if g.failMsg != "" {
		return fmt.Errorf("%s", g.failMsg)
	}
	g.accepts++
	g.epochs[root], g.shas[root] = epoch, sha
	return nil
}

// .
// .
func expectAbsent(t *testing.T, set *RevocationStatusSet, rootKeyType, want string) {
	t.Helper()
	snap := set.lookup(rootKeyType)
	if snap == nil {
		t.Fatalf("root %s has no entry at all", rootKeyType)
	}
	if snap.err == nil {
		t.Fatalf("root %s snapshot must be absent (want reason containing %q), got a usable snapshot at epoch %d", rootKeyType, want, snap.trustEpoch)
	}
	if !strings.Contains(snap.err.Error(), want) {
		t.Fatalf("root %s absence reason %q does not mention %q", rootKeyType, snap.err, want)
	}
}

// .

func TestRevocationValidEmptySnapshots(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	for _, root := range []string{keyTypePublisherCertifier, keyTypeReviewer, keyTypePlatformRelease} {
		if epoch, ok := f.roots.Revocation.Epoch(root); !ok || epoch != 1 {
			t.Fatalf("root %s: want usable empty snapshot at epoch 1, got ok=%v epoch=%d", root, ok, epoch)
		}
	}
	for _, line := range f.roots.Revocation.Describe() {
		if !strings.Contains(line, "epoch 1, 0 revoked") {
			t.Fatalf("describe line %q is not the empty-snapshot shape", line)
		}
	}
}

func TestRevocationSingleEntrySnapshot(t *testing.T) {
	f := fixtures(t)
	entry := RevokedEntry{ArtifactKind: artifactKindManifestSig, PayloadSHA256: "sha256:" + strings.Repeat("a", 64)}
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: f.statusSingle}, f.roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := set.lookup(keyTypePublisherCertifier)
	if snap.err != nil {
		t.Fatalf("single-entry snapshot must load: %v", snap.err)
	}
	if !snap.revoked[revokedKey(entry.ArtifactKind, entry.PayloadSHA256)] {
		t.Fatal("the revoked entry is not in the loaded set")
	}
	if snap.trustEpoch != 2 || len(snap.revoked) != 1 {
		t.Fatalf("snapshot state wrong: epoch=%d entries=%d", snap.trustEpoch, len(snap.revoked))
	}
}

func TestRevocationRejectsMalformedSnapshots(t *testing.T) {
	f := fixtures(t)
	cases := []struct {
		name string
		want string
	}{
		{"unsorted", "not strictly sorted"},
		{"unsorted-by-kind", "not strictly sorted"},
		{"duplicate", "not strictly sorted"},
		{"cross-domain-kind", "outside the plugin_publisher_certifier domain"},
		{"uppercase-digest", "lowercase"},
		{"short-digest", "64 hex"},
		{"unprefixed-digest", "sha256:"},
		{"schema-version-2", "schema_version"},
		{"epoch-zero", "positive"},
		{"epoch-negative", "positive"},
		{"missing-revoked", "missing a required member"},
		{"missing-epoch", "missing a required member"},
		{"extra-member", "closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, ok := f.statusMalformed[tc.name]
			if !ok {
				t.Fatalf("signed verifier vector %q is missing", tc.name)
			}
			set, err := loadStatusSet(map[string][]byte{statusFileCertifier: raw}, f.roots, nil)
			if err != nil {
				t.Fatal(err)
			}
			expectAbsent(t, set, keyTypePublisherCertifier, tc.want)
		})
	}
}

func TestRevocationRejectsWrongSigner(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	// .
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: f.statusReviewer}, f.roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectAbsent(t, set, keyTypePublisherCertifier, "does not verify")
}

func TestRevocationRejectsWrongArtifactKindEnvelope(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: f.certBytes}, f.roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectAbsent(t, set, keyTypePublisherCertifier, "does not verify")
}

func TestRevocationMutationProbe(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	// .
	// .
	raw := append([]byte(nil), f.statusCertifier...)
	// .
	// .
	// .
	idx := bytes.Index(raw, []byte("trust_epoch"))
	if idx < 0 {
		t.Fatal("fixture snapshot has no trust_epoch bytes")
	}
	raw[idx] ^= 0x20
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: raw}, f.roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := set.lookup(keyTypePublisherCertifier)
	if snap.err == nil {
		t.Fatal("a one-byte mutation of the snapshot still loaded — the format is not byte-exact")
	}
	// .
	roots := f.roots
	roots.Revocation = set
	_, verr := Verify(bytes.NewReader(f.t1Pkg(t)), roots)
	expectReason(t, verr, ReasonRevocationStatusUnavailable)
}

// .

func TestRevocationEpochGuardLifecycle(t *testing.T) {
	f := fixtures(t)
	guard := newFakeGuard()

	// .
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: f.statusCertifier}, f.roots, guard)
	if err != nil {
		t.Fatal(err)
	}
	if snap := set.lookup(keyTypePublisherCertifier); snap.err != nil {
		t.Fatalf("first-seen epoch must be accepted: %v", snap.err)
	}
	if guard.accepts != 1 || guard.epochs[keyTypePublisherCertifier] != 1 {
		t.Fatalf("first-seen acceptance not recorded: %+v", guard)
	}

	// .
	set, _ = loadStatusSet(map[string][]byte{statusFileCertifier: f.statusCertifier}, f.roots, guard)
	if snap := set.lookup(keyTypePublisherCertifier); snap.err != nil {
		t.Fatalf("re-load of the accepted snapshot must stay usable: %v", snap.err)
	}
	if guard.accepts != 1 {
		t.Fatalf("equal-epoch same-content re-load must not re-record (accepts=%d)", guard.accepts)
	}

	// .
	set, _ = loadStatusSet(map[string][]byte{statusFileCertifier: f.statusEpoch3}, f.roots, guard)
	if snap := set.lookup(keyTypePublisherCertifier); snap.err != nil {
		t.Fatalf("advanced epoch must be accepted: %v", snap.err)
	}
	if guard.epochs[keyTypePublisherCertifier] != 3 {
		t.Fatalf("high-water mark did not advance: %+v", guard.epochs)
	}

	// .
	set, _ = loadStatusSet(map[string][]byte{statusFileCertifier: f.statusCertifier}, f.roots, guard)
	expectAbsent(t, set, keyTypePublisherCertifier, "ROLLBACK")
	// .
	roots := f.roots
	roots.Revocation = set
	_, verr := Verify(bytes.NewReader(f.t1Pkg(t)), roots)
	expectReason(t, verr, ReasonRevocationStatusUnavailable)

	// .
	set, _ = loadStatusSet(map[string][]byte{statusFileCertifier: f.statusFork3}, f.roots, guard)
	expectAbsent(t, set, keyTypePublisherCertifier, "FORK")
}

func TestRevocationUnrecordableAcceptanceIsAbsent(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	guard := newFakeGuard()
	guard.failMsg = "ledger frozen (SAFE)"
	set, err := loadStatusSet(map[string][]byte{statusFileCertifier: f.statusCertifier}, f.roots, guard)
	if err != nil {
		t.Fatal(err)
	}
	expectAbsent(t, set, keyTypePublisherCertifier, "could not be ledgered")
}

// .

// .
// .
func (f *fixtureSet) rootsRevoking(t *testing.T, file string, raw []byte) TrustRoots {
	t.Helper()
	files := map[string][]byte{
		statusFileCertifier: f.statusCertifier,
		statusFileReviewer:  f.statusReviewer,
		statusFilePlatform:  f.statusPlatform,
	}
	files[file] = raw
	return f.rootsWithStatus(t, files, nil)
}

func TestVerifyRejectsRevokedManifest(t *testing.T) {
	f := fixtures(t)
	roots := f.rootsRevoking(t, statusFileCertifier, f.statusRevokeManifest)
	_, err := Verify(bytes.NewReader(f.t1Pkg(t)), roots)
	expectReason(t, err, ReasonTrustPayloadRevoked)
}

func TestVerifyRejectsRevokedPublisherCert(t *testing.T) {
	f := fixtures(t)
	roots := f.rootsRevoking(t, statusFileCertifier, f.statusRevokeCert)
	_, err := Verify(bytes.NewReader(f.t1Pkg(t)), roots)
	expectReason(t, err, ReasonTrustPayloadRevoked)
	// .
	_, err = Verify(bytes.NewReader(f.t2Pkg(t)), roots)
	expectReason(t, err, ReasonTrustPayloadRevoked)
}

func TestVerifyRejectsRevokedAttestationNotDowngrade(t *testing.T) {
	f := fixtures(t)
	roots := f.rootsRevoking(t, statusFileReviewer, f.statusRevokeAttestation)
	// .
	// .
	// .
	res, err := Verify(bytes.NewReader(f.t2Pkg(t)), roots)
	if err == nil {
		t.Fatalf("revoked attestation must reject, got tier %s", res.Tier)
	}
	expectReason(t, err, ReasonTrustPayloadRevoked)
	// .
	// .
	res = mustVerify(t, f.t1Pkg(t), roots)
	if res.Tier != TierT1 {
		t.Fatalf("T1 under a reviewer-revoked attestation snapshot: got %s", res.Tier)
	}
}

func TestVerifyRejectsRevokedPlatformSig(t *testing.T) {
	f := fixtures(t)
	roots := f.rootsRevoking(t, statusFilePlatform, f.statusRevokePlatform)
	_, err := Verify(bytes.NewReader(f.t3Pkg(t)), roots)
	expectReason(t, err, ReasonTrustPayloadRevoked)
}

func TestVerifyMissingSnapshotMakesTierUnavailable(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	roots := f.rootsWithStatus(t, map[string][]byte{
		statusFileReviewer: f.statusReviewer,
		statusFilePlatform: f.statusPlatform,
	}, nil)
	_, err := Verify(bytes.NewReader(f.t1Pkg(t)), roots)
	expectReason(t, err, ReasonRevocationStatusUnavailable)

	// .
	// .
	roots = f.rootsWithStatus(t, map[string][]byte{
		statusFileCertifier: f.statusCertifier,
		statusFilePlatform:  f.statusPlatform,
	}, nil)
	_, err = Verify(bytes.NewReader(f.t2Pkg(t)), roots)
	expectReason(t, err, ReasonRevocationStatusUnavailable)
	if res := mustVerify(t, f.t1Pkg(t), roots); res.Tier != TierT1 {
		t.Fatalf("T1 with certifier snapshot present: got %s", res.Tier)
	}

	// .
	roots = f.rootsWithStatus(t, map[string][]byte{
		statusFileCertifier: f.statusCertifier,
		statusFileReviewer:  f.statusReviewer,
	}, nil)
	_, err = Verify(bytes.NewReader(f.t3Pkg(t)), roots)
	expectReason(t, err, ReasonRevocationStatusUnavailable)

	// .
	nilRoots := f.roots
	nilRoots.Revocation = nil
	_, err = Verify(bytes.NewReader(f.t1Pkg(t)), nilRoots)
	expectReason(t, err, ReasonRevocationStatusUnavailable)
	_, err = Verify(bytes.NewReader(f.t3Pkg(t)), nilRoots)
	expectReason(t, err, ReasonRevocationStatusUnavailable)
}

func TestVerifyT0IndependentOfRevocationState(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	// .
	nilRoots := TrustRoots{}
	if res := mustVerify(t, f.t0Pkg(t), nilRoots); res.Tier != TierT0 {
		t.Fatalf("T0 under nil revocation state: got %s", res.Tier)
	}
	roots := f.rootsRevoking(t, statusFileCertifier, f.statusRevokeManifest)
	if res := mustVerify(t, f.t0Pkg(t), roots); res.Tier != TierT0 {
		t.Fatalf("T0 under a revoking snapshot: got %s", res.Tier)
	}
}

func TestRevocationPinnedRootInteraction(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	// .
	// .
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "certifier-root.pub.json")
	envRaw, err := json.MarshalIndent(f.certifier, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootPath, envRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPinnedRoot(rootPath)
	if err != nil {
		t.Fatalf("LoadPinnedRoot refused a real emitted envelope: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, statusFileCertifier), f.statusCertifier, 0o644); err != nil {
		t.Fatal(err)
	}
	roots := TrustRoots{PublisherCertifier: loaded}
	roots.Revocation = LoadRevocationStatus(dir, roots, nil)
	if snap := roots.Revocation.lookup(keyTypePublisherCertifier); snap.err != nil {
		t.Fatalf("snapshot must verify under the disk-loaded pinned root: %v", snap.err)
	}
	if res := mustVerify(t, f.t1Pkg(t), roots); res.Tier != TierT1 {
		t.Fatalf("T1 under disk-loaded root + snapshot: got %s", res.Tier)
	}
	// .
	// .
	roots2 := TrustRoots{}
	roots2.Revocation = LoadRevocationStatus(dir, roots2, nil)
	expectAbsent(t, roots2.Revocation, keyTypePublisherCertifier, "no plugin_publisher_certifier root pinned")
}

func TestLoadRevocationStatusMissingDir(t *testing.T) {
	f := fixtures(t)
	// .
	// .
	set := LoadRevocationStatus(filepath.Join(t.TempDir(), "no-such-dir"), f.roots, nil)
	for _, root := range []string{keyTypePublisherCertifier, keyTypeReviewer, keyTypePlatformRelease} {
		expectAbsent(t, set, root, "missing")
	}
}
