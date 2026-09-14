package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestAssetName(t *testing.T) {
	name := assetName("0.2.0")
	if !strings.Contains(name, "0.2.0") {
		t.Errorf("assetName should contain version, got %q", name)
	}
	if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
		t.Errorf("assetName should end with archive extension, got %q", name)
	}
}

func TestStateSetAvailable(t *testing.T) {
	s := &State{}
	s.SetAvailable("0.2.0")
	if s.Available() != "0.2.0" {
		t.Errorf("Available() = %q, want 0.2.0", s.Available())
	}
	s.SetAvailable("")
	if s.Available() != "" {
		t.Errorf("Available() = %q, want empty", s.Available())
	}
}

func TestStateSetInstalled(t *testing.T) {
	s := &State{}
	s.SetAvailable("0.2.0")
	s.SetInstalled("0.2.0")
	snap := s.Snapshot("0.1.0")
	if snap.InstalledVersion != "0.2.0" {
		t.Errorf("InstalledVersion = %q, want 0.2.0", snap.InstalledVersion)
	}
	if !snap.NeedsRestart {
		t.Error("NeedsRestart should be true after install")
	}
	if snap.AvailableVersion != "" {
		t.Errorf("AvailableVersion should be cleared after install, got %q", snap.AvailableVersion)
	}
}

func TestCheckRollbackNoBackup(t *testing.T) {
	// .
	result := CheckRollback(t.TempDir())
	if result != "" {
		t.Errorf("CheckRollback with no backup should return empty, got %q", result)
	}
}

func TestCheckRollbackStaleBackup(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	writeFile(t, dir, "aii.previous", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	if err := writePending(dir, updatePending{Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if result := checkRollbackAt(dir, filepath.Join(dir, "aii")); result != "" {
		t.Errorf("CheckRollback with marker present should return empty, got %q", result)
	}
	if fileExists(dir, "aii.previous") {
		t.Error("stale aii.previous should have been removed")
	}
	if fileExists(dir, ".update_pending") {
		t.Error("stale tombstone should have been removed")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAnUpdateSurvivesItsFirstBoot(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	// .
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("THE UPDATE ROLLED ITSELF BACK ON FIRST BOOT (returned %q)", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "new binary" {
		t.Fatalf("after first boot the binary is %q — the update did not survive", got)
	}
	// .
	WriteBootMarker(dir)
	if fileExists(dir, "aii.previous") || fileExists(dir, ".update_pending") {
		t.Error("a healthy boot must retire the backup and the tombstone")
	}
	// .
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("a completed update rolled back on a later boot: %q", rolled)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("binary after the marker boot: %q", got)
	}
}

// .
// .
// .
func TestAFailedUpdateBootRollsBack(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	// .
	// .
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("first boot must not roll back, got %q", rolled)
	}
	// .
	rolled := checkRollbackAt(dir, exePath)
	if rolled == "" {
		t.Fatal("a failed update boot did not roll back")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "old binary" {
		t.Fatalf("after rollback the binary is %q, want the backup", got)
	}
	if fileExists(dir, "aii.previous") || fileExists(dir, ".update_pending") {
		t.Error("rollback must retire the backup and the tombstone")
	}
}

// .
// .
// .
// .
// .
func TestACorruptBackupIsRefusedNotInstalled(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	// .
	writeFile(t, dir, "aii.previous", "old bin")
	// .
	checkRollbackAt(dir, exePath)
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("A CORRUPT BACKUP WAS INSTALLED (returned %q)", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "new binary" {
		t.Fatalf("the executable was clobbered with the corrupt backup: %q", got)
	}
	if !fileExists(dir, "aii.previous") {
		t.Error("the refused backup should be kept for forensics")
	}
}

// .
// .
// .
// .
func TestAHandRepairedBinaryIsNotClobbered(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	if err := swapBinary(exePath, []byte("new binary"), dir); err != nil {
		t.Fatalf("swap: %v", err)
	}
	checkRollbackAt(dir, exePath)
	writeFile(t, dir, "aii", "operator repair")
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("an operator repair was rolled back: %q", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "operator repair" {
		t.Fatalf("the operator repair was clobbered: %q", got)
	}
	if fileExists(dir, "aii.previous") || fileExists(dir, ".update_pending") {
		t.Error("update state must be retired once the operator has taken over")
	}
}

// .
// .
// .
// .
// .
func TestALegacyBackupIsAdoptedThenJudged(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "current binary")
	writeFile(t, dir, "aii.previous", "legacy backup")
	// .

	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("a legacy backup was restored on first sighting: %q", rolled)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "current binary" {
		t.Fatalf("first sighting changed the binary: %q", got)
	}
	// .
	rolled := checkRollbackAt(dir, exePath)
	if rolled == "" {
		t.Fatal("the adopted legacy backup was never used")
	}
	if got, _ := os.ReadFile(exePath); string(got) != "legacy backup" {
		t.Fatalf("after legacy rollback the binary is %q", got)
	}
}

func TestRollbackToUnwritableTargetFails(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	exePath := filepath.Join(dir, "gone", "aii")
	writeFile(t, dir, "aii.previous", "fake old binary")
	pend := updatePending{Attempts: 1, BackupSHA256: sha256hex([]byte("fake old binary"))}
	if result := rollbackToPrev(dir, filepath.Join(dir, "aii.previous"), exePath, pend); result != "" {
		t.Errorf("rollback to an unwritable target should return empty, got %q", result)
	}
	if !fileExists(dir, "aii.previous") {
		t.Error("a failed restore must keep the backup")
	}
}

func TestWriteBootMarker(t *testing.T) {
	// .
	dirRetire := t.TempDir()
	writeFile(t, dirRetire, "aii.previous", "x")
	if err := writePending(dirRetire, updatePending{Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	WriteBootMarker(dirRetire)
	if fileExists(dirRetire, "aii.previous") || fileExists(dirRetire, ".update_pending") {
		t.Error("WriteBootMarker must retire the backup and the tombstone")
	}

	dir := t.TempDir()
	WriteBootMarker(dir)
	if !fileExists(dir, ".boot_completed") {
		t.Error("boot marker should exist after WriteBootMarker")
	}
}

// .

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// .

// .
// .
// .
type testReleaseSigner struct {
	env   *sigenvelope.PublicKeyEnvelope
	ml    *crypto.KeyPair
	slhSk *slh.SecretKey
}

func newTestReleaseSigner(t *testing.T) *testReleaseSigner {
	t.Helper()
	ml, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	slhSkVal, slhPub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		t.Fatalf("SLH keygen: %v", err)
	}
	now := time.Now().UTC()
	keyID := "aiii_test_" + ml.Fingerprint()[:12]
	slhPubB64 := base64.StdEncoding.EncodeToString(slhPub.Bytes())
	env := &sigenvelope.PublicKeyEnvelope{
		V: 1, Kind: "aiii.server_key.public", KeyID: keyID, KeyType: "platform_release", Profile: crypto.ProfileRoot,
		CreatedAt: now.Format(time.RFC3339),
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Keys: []sigenvelope.PublicKeyMaterial{
			{Alg: crypto.SigAlg, PublicKeyB64: ml.PublicKeyB64(), PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SigAlg, keyID, ml.PublicKeyB64())},
			{Alg: crypto.SLHAlg, PublicKeyB64: slhPubB64, PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(crypto.SLHAlg, keyID, slhPubB64)},
		},
	}
	return &testReleaseSigner{env: env, ml: ml, slhSk: &slhSkVal}
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
func (s *testReleaseSigner) provisionTrustDir(t *testing.T, dir string) {
	t.Helper()
	trust := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trust, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := s.signPayloadAs(t, packagefmt.ArtifactKindRevocationStatus, map[string]interface{}{
		"schema_version": 1, "trust_epoch": 1, "revoked": []interface{}{},
	})
	if err := os.WriteFile(filepath.Join(trust, "aiii_platform_release_status.json"), snap, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *testReleaseSigner) signReleasePayload(t *testing.T, payloadObj interface{}) []byte {
	return s.signPayloadAs(t, artifactKindReleaseSig, payloadObj)
}

func (s *testReleaseSigner) signPayloadAs(t *testing.T, artifactKind string, payloadObj interface{}) []byte {
	t.Helper()
	payload, err := json.Marshal(payloadObj)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPayload, err := canonicaljson.CanonicalizeV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := sigenvelope.SHA256Prefixed(canonicalPayload)
	entries := make([]sigenvelope.SignatureEntry, 0, 2)
	for _, alg := range []string{crypto.SigAlg, crypto.SLHAlg} {
		mat, ok := s.env.FindPublicKey(alg)
		if !ok {
			t.Fatalf("signer has no %s material", alg)
		}
		in := sigenvelope.SignatureInput(artifactKind, crypto.ProfileRoot, alg, s.env.KeyID, mat.PublicKeyFingerprint, payloadSHA)
		var sig []byte
		switch alg {
		case crypto.SigAlg:
			sig, err = crypto.Sign(s.ml, []byte(in))
		case crypto.SLHAlg:
			sig, err = s.slhSk.Sign(rand.Reader, []byte(in), nil)
		}
		if err != nil {
			t.Fatalf("sign %s: %v", alg, err)
		}
		entries = append(entries, sigenvelope.SignatureEntry{
			Alg: alg, KeyID: s.env.KeyID, PublicKeyFingerprint: mat.PublicKeyFingerprint,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(in)), SigB64: base64.StdEncoding.EncodeToString(sig),
		})
	}
	out, err := json.Marshal(sigenvelope.Envelope{
		ArtifactKind: artifactKind, Payload: payload, PayloadSHA256: payloadSHA,
		Canonicalization: sigenvelope.CanonicalizationV1, SignatureProfile: crypto.ProfileRoot, Signatures: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFreshReleaseSigningRoundTrip(t *testing.T) {
	s := newTestReleaseSigner(t)
	hash := sha256hex([]byte("fresh release archive"))
	sig := s.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: hash,
		Version:     "v9.8.7",
		Platform:    "linux",
		Arch:        "amd64",
		SourceRev:   "fresh123456",
	})
	if _, err := verifyReleaseSig(sig, s.env, hash, "v9.8.7", "linux", "amd64"); err != nil {
		t.Fatalf("fresh dual-PQ release signature did not verify: %v", err)
	}
}

func TestVerifyReleaseSigValid(t *testing.T) {
	sig, root := releaseSignature(t, "valid_fake_archive_v123")
	archiveHash := sha256hex([]byte("fake archive content"))

	if _, err := verifyReleaseSig(sig, root, archiveHash, "v1.2.3", "linux", "amd64"); err != nil {
		t.Fatalf("valid signature must verify: %v", err)
	}
}

func TestVerifyReleaseSigTamperedHash(t *testing.T) {
	sig, root := releaseSignature(t, "valid_archive_v123")
	// .
	wrongHash := sha256hex([]byte("tampered content"))

	_, err := verifyReleaseSig(sig, root, wrongHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("signature with wrong archive hash must reject")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("should fail with hash mismatch, got: %v", err)
	}
}

func TestVerifyReleaseSigWrongRoot(t *testing.T) {
	// .
	v := releaseVectors(t)
	sig := v.Signatures["valid_archive_v123"]
	archiveHash := sha256hex([]byte("archive content"))

	_, err := verifyReleaseSig(sig, v.OtherRoot, archiveHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("signature with wrong root must reject")
	}
}

func TestVerifyReleaseSigUnknownFields(t *testing.T) {
	sig, root := releaseSignature(t, "unknown_fields")
	archiveHash := sha256hex([]byte("archive content"))

	_, err := verifyReleaseSig(sig, root, archiveHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("payload with unknown fields must reject")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("should fail with unknown field error, got: %v", err)
	}
}

func TestVerifyReleaseSigTrailingData(t *testing.T) {
	sig, root := releaseSignature(t, "valid_archive_v123")
	archiveHash := sha256hex([]byte("archive content"))

	// .
	tampered := append(append([]byte(nil), sig...), []byte(`{"extra":"data"}`)...)
	_, err := verifyReleaseSig(tampered, root, archiveHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("payload with trailing data must reject")
	}
}

func TestVerifyReleaseSigWrongArtifactKind(t *testing.T) {
	sig, root := releaseSignature(t, "valid_archive_v123")
	archiveHash := sha256hex([]byte("archive content"))
	// .
	var m map[string]json.RawMessage
	if err := json.Unmarshal(sig, &m); err != nil {
		t.Fatal(err)
	}
	m["artifact_kind"] = json.RawMessage(`"wrong.kind"`)
	sig, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	_, err = verifyReleaseSig(sig, root, archiveHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("wrong artifact kind must reject")
	}
}

func TestSwapBinaryAtomic(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	// .
	if err := os.WriteFile(exePath, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("new binary")

	if err := swapBinary(exePath, newBinary, dir); err != nil {
		t.Fatalf("swapBinary: %v", err)
	}

	// .
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Errorf("after swap, exe should be new, got %q", string(got))
	}
	// .
	prevPath := filepath.Join(dir, "aii.previous")
	prev, err := os.ReadFile(prevPath)
	if err != nil {
		t.Fatalf("backup should exist: %v", err)
	}
	if string(prev) != "old binary" {
		t.Errorf("backup should contain old binary, got %q", string(prev))
	}
	// .
	if fileExists(dir, ".boot_completed") {
		t.Error("boot marker should have been deleted before swap")
	}
	// .
	pend, ok := readPending(dir)
	if !ok {
		t.Fatal("swap left no tombstone — the next boot cannot tell first-boot from failed-boot")
	}
	if pend.Attempts != 0 {
		t.Errorf("a fresh swap must record attempts 0, got %d", pend.Attempts)
	}
	if pend.BackupSHA256 != sha256hex([]byte("old binary")) {
		t.Error("tombstone does not carry the backup's hash")
	}
	if pend.NewSHA256 != sha256hex(newBinary) {
		t.Error("tombstone does not carry the new binary's hash")
	}
}

func TestSwapBinaryFailureCleanup(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	// .
	// .
	// .
	writeFile(t, dir, ".boot_completed", "ok")
	exePath := filepath.Join(dir, "subdir", "aii")
	if err := swapBinary(exePath, []byte("new"), dir); err == nil {
		t.Fatal("swapBinary with no readable current binary should fail")
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("a swap that failed before installing anything must not disarm boot health")
	}
	if fileExists(dir, "aii.previous") || fileExists(dir, ".update_pending") {
		t.Error("a failed swap must leave no update state behind")
	}
}

func TestExtractFromTarGz(t *testing.T) {
	// .
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	binaryContent := []byte("fake aii binary content")
	hdr := &tar.Header{
		Name: "aii",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()

	got, err := extractFromTarGz(buf.Bytes())
	if err != nil {
		t.Fatalf("extractFromTarGz happy path: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("extracted content = %q, want %q", string(got), string(binaryContent))
	}
}

func TestExtractFromTarGzNotFound(t *testing.T) {
	// .
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	hdr := &tar.Header{Name: "readme.txt", Mode: 0644, Size: 5}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("hello"))
	tw.Close()
	gzw.Close()

	_, err := extractFromTarGz(buf.Bytes())
	if err == nil {
		t.Fatal("archive without 'aii' entry should fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("should fail with 'not found', got: %v", err)
	}
}

func TestExtractFromZipNotFound(t *testing.T) {
	// .
	_, err := extractFromZip([]byte("PK\x03\x04"))
	if err == nil {
		t.Fatal("invalid zip should return an error, not nil")
	}
}

// .
func (s *testReleaseSigner) root() *sigenvelope.PublicKeyEnvelope { return s.env }
