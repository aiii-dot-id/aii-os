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
	s.setAvailable("0.2.0")
	if s.Available() != "0.2.0" {
		t.Errorf("Available() = %q, want 0.2.0", s.Available())
	}
	s.setAvailable("")
	if s.Available() != "" {
		t.Errorf("Available() = %q, want empty", s.Available())
	}
}

func TestStateSetInstalled(t *testing.T) {
	s := &State{}
	s.setAvailable("0.2.0")
	s.setInstalled("0.2.0")
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
	// No aii.previous file → no rollback, no error.
	result := CheckRollback(t.TempDir())
	if result != "" {
		t.Errorf("CheckRollback with no backup should return empty, got %q", result)
	}
}

func TestCheckRollbackStaleBackup(t *testing.T) {
	dir := t.TempDir()
	// aii.previous AND the marker AND a tombstone → the last boot
	// succeeded; everything is stale and must be retired.
	writeFile(t, dir, "aii.previous", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")
	if err := writeRecord(filepath.Join(dir, pendingFile), updatePending{Attempts: 1}); err != nil {
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

// THE SEQUENCE A REAL UPDATE RUNS: swap, restart, boot to health. The
// old code failed this test at its second step — the updated binary's
// first boot saw backup-present + marker-absent (the swap itself
// deletes the marker), read that as "the update failed", and restored
// the old binary over itself. Every update self-reverted, and every
// test passed, because every test built its state by hand and none ran
// this sequence.
func TestAnUpdateSurvivesItsFirstBoot(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok") // the old binary booted healthy once

	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	// Restart: the updated binary's first boot.
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("THE UPDATE ROLLED ITSELF BACK ON FIRST BOOT (returned %q)", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "new binary" {
		t.Fatalf("after first boot the binary is %q — the update did not survive", got)
	}
	// The boot completes healthy.
	writeBootMarkerAt(dir, exePath)
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("a healthy boot must retire the backup and the record")
	}
	// And the next restart changes nothing.
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("a completed update rolled back on a later boot: %q", rolled)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("binary after the marker boot: %q", got)
	}
}

// And the case the marker machinery exists FOR: the updated binary
// boots, dies before writing the marker, and the next boot restores
// the old one — verified against the hash the swap recorded.
func TestAFailedUpdateBootRollsBack(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	// First boot of the update: begins (attempt counted), then dies
	// before WriteBootMarker — so nothing else happens here.
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("first boot must not roll back, got %q", rolled)
	}
	// Second boot: attempt 1 recorded, marker still absent → restore.
	rolled := checkRollbackAt(dir, exePath)
	if rolled == "" {
		t.Fatal("a failed update boot did not roll back")
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "old binary" {
		t.Fatalf("after rollback the binary is %q, want the backup", got)
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("rollback must retire the backup and the record")
	}
}

func TestRollbackArmedIsTheRollbackTheNextStartPerforms(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		dir := t.TempDir()
		exePath := filepath.Join(dir, "aii")
		writeFile(t, dir, "aii", "old binary")
		writeFile(t, dir, ".boot_completed", "ok")
		if rollbackArmedAt(dir, exePath) {
			t.Fatal("armed with no update in flight")
		}
		if _, err := swapForTest(t, exePath, []byte("new binary"), dir); err != nil {
			t.Fatalf("swap: %v", err)
		}
		if rollbackArmedAt(dir, exePath) {
			t.Fatal("armed before the update's first boot: the next start counts that boot, it does not roll back")
		}
		if rolled := checkRollbackAt(dir, exePath); rolled != "" {
			t.Fatalf("the first boot rolled back: %q", rolled)
		}
		if !rollbackArmedAt(dir, exePath) {
			t.Fatal("a counted boot with no marker is not armed")
		}
		want := "old binary"
		if healthy {
			writeBootMarkerAt(dir, exePath)
			if rollbackArmedAt(dir, exePath) {
				t.Fatal("armed after the boot marker")
			}
			want = "new binary"
		}
		checkRollbackAt(dir, exePath) // the next start
		if got, _ := os.ReadFile(exePath); string(got) != want {
			t.Fatalf("healthy=%v: after the next start the binary is %q, want %q", healthy, got, want)
		}
		if rollbackArmedAt(dir, exePath) {
			t.Fatalf("healthy=%v: still armed after the next start", healthy)
		}
	}
}

// A BACKUP THAT DOES NOT HASH TO WHAT THE SWAP RECORDED IS NOT A
// BACKUP. The old restore path would write it over the binary anyway —
// a crash that truncated aii.previous became, one boot later, a
// truncated EXECUTABLE: the safety net bricking the identity it
// protects. The fixed path refuses and keeps the current binary.
func TestACorruptBackupIsRefusedNotInstalled(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	// Corrupt the backup the way a torn write would.
	if err := os.WriteFile(tgt.backup, []byte("old bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Boot, die, boot again.
	checkRollbackAt(dir, exePath)
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("A CORRUPT BACKUP WAS INSTALLED (returned %q)", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "new binary" {
		t.Fatalf("the executable was clobbered with the corrupt backup: %q", got)
	}
	if !exists(tgt.backup) {
		t.Error("the refused backup should be kept for forensics")
	}
}

// An operator who replaced the binary by hand has already done the
// rollback's job, better informed. Their bytes win; the update state
// is retired rather than clobbering the repair with the thing that
// already failed to boot.
func TestAHandRepairedBinaryIsNotClobbered(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	tgt, err := swapForTest(t, exePath, []byte("new binary"), dir)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	checkRollbackAt(dir, exePath) // first boot, dies
	writeFile(t, dir, "aii", "operator repair")
	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("an operator repair was rolled back: %q", rolled)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "operator repair" {
		t.Fatalf("the operator repair was clobbered: %q", got)
	}
	if exists(tgt.backup) || exists(tgt.record) {
		t.Error("update state must be retired once the operator has taken over")
	}
}

// A backup left by a build from before the tombstone existed cannot be
// judged on its first sighting: adopt it (hash it, count one attempt)
// and decide on the next boot with real information — instead of the
// old behavior, which was to roll back immediately and revert every
// update on its first boot.
func TestALegacyBackupIsAdoptedThenJudged(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "current binary")
	writeFile(t, dir, "aii.previous", "legacy backup")
	// No marker, no tombstone: exactly what an old-build swap leaves.

	if rolled := checkRollbackAt(dir, exePath); rolled != "" {
		t.Fatalf("a legacy backup was restored on first sighting: %q", rolled)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "current binary" {
		t.Fatalf("first sighting changed the binary: %q", got)
	}
	// Still no marker on the next boot → now it is a failed update.
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
	// The restore target sits in a directory that does not exist, so
	// the atomic write's temp file cannot be created.
	exePath := filepath.Join(dir, "gone", "aii")
	writeFile(t, dir, "aii.previous", "fake old binary")
	pend := updatePending{Attempts: 1, BackupSHA256: sha256hex([]byte("fake old binary"))}
	r := recovery{dataDir: dir, backup: filepath.Join(dir, previousFile), record: filepath.Join(dir, pendingFile), tgt: targetAt(exePath), legacy: true}
	if result := r.rollback(pend); result != "" {
		t.Errorf("rollback to an unwritable target should return empty, got %q", result)
	}
	if !fileExists(dir, "aii.previous") {
		t.Error("a failed restore must keep the backup")
	}
}

func TestWriteBootMarker(t *testing.T) {
	// A healthy boot retires the whole update state, not just the marker.
	dirRetire := t.TempDir()
	writeFile(t, dirRetire, "aii.previous", "x")
	if err := writeRecord(filepath.Join(dirRetire, pendingFile), updatePending{Attempts: 1}); err != nil {
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

// --- test helpers ---

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

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// targetForTest is the target applyTo resolves for exePath.
func targetForTest(t *testing.T, exePath string) target {
	t.Helper()
	tgt, err := targetOf(exePath)
	if err != nil {
		t.Fatal(err)
	}
	return tgt
}

// ownerForTest is how an update record names the identity at dataDir.
func ownerForTest(t *testing.T, dataDir string) string {
	t.Helper()
	owner, err := ownerOf(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

// swapForTest is swapBinary as applyTo runs it inside target.replace:
// the target resolved from the executable, the identity at dataDir its
// owner.
func swapForTest(t *testing.T, exePath string, newBinary []byte, dataDir string) (target, error) {
	t.Helper()
	tgt := targetForTest(t, exePath)
	return tgt, swapBinary(tgt, newBinary, nil, dataDir, ownerForTest(t, dataDir), "")
}

// assertRecord holds the record beside tgt to the swap that wrote it.
func assertRecord(t *testing.T, tgt target, owner string, attempts int, backup, installed string) {
	t.Helper()
	p, present, err := readClaim(tgt.record)
	if !present || err != nil {
		t.Fatalf("no readable update record beside %s: present=%v err=%v", tgt.path, present, err)
	}
	if p.Owner != owner || p.Attempts != attempts || p.BackupSHA256 != sha256hex([]byte(backup)) || p.NewSHA256 != sha256hex([]byte(installed)) {
		t.Fatalf("the update record is %+v; want owner %s, attempts %d, backup %q, installed %q", p, owner, attempts, backup, installed)
	}
}

// --- Signature verification tests (Issue 4) ---

// testReleaseSigner is used only by the dedicated fresh-signing lane below.
// Verifier tests consume immutable public vectors instead of regenerating
// SLH-DSA material.
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

// signReleasePayload signs a payload object with the release artifact kind.
// provisionTrustDir writes a signed, EMPTY revocation snapshot for this
// signer's root into <dir>/trust.
//
// A release is now revocation-checked through packagefmt, and a root the
// binary did not ship has no snapshot to fall back on — so a test using
// its own root must provision one, exactly as an operator running their
// own platform_release root must. That requirement IS the behaviour
// change, so a test that could not express it would be hiding it.
func (s *testReleaseSigner) provisionTrustDir(t *testing.T, dir string) {
	t.Helper()
	trust := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trust, 0o755); err != nil {
		t.Fatal(err)
	}
	snap := s.signPayloadAs(t, packagefmt.ArtifactKindRevocationStatus, map[string]any{
		"schema_version": 1, "trust_epoch": 1, "revoked": []any{},
	})
	if err := os.WriteFile(filepath.Join(trust, "aiii_platform_release_status.json"), snap, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *testReleaseSigner) signReleasePayload(t *testing.T, payloadObj any) []byte {
	return s.signPayloadAs(t, artifactKindReleaseSig, payloadObj)
}

func (s *testReleaseSigner) signPayloadAs(t *testing.T, artifactKind string, payloadObj any) []byte {
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
	// Sign one hash, verify against a different one.
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
	// Sign with one root, verify against a different root.
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

	// Append trailing data after the payload object.
	tampered := append(append([]byte(nil), sig...), []byte(`{"extra":"data"}`)...)
	_, err := verifyReleaseSig(tampered, root, archiveHash, "v1.2.3", "linux", "amd64")
	if err == nil {
		t.Fatal("payload with trailing data must reject")
	}
}

func TestVerifyReleaseSigWrongArtifactKind(t *testing.T) {
	sig, root := releaseSignature(t, "valid_archive_v123")
	archiveHash := sha256hex([]byte("archive content"))
	// Sign with the correct kind, then mangle the artifact_kind in the bundle.
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
	// Write a "running" binary.
	if err := os.WriteFile(exePath, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("new binary")

	tgt, err := swapForTest(t, exePath, newBinary, dir)
	if err != nil {
		t.Fatalf("swapBinary: %v", err)
	}

	// The running binary should now be the new content.
	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary" {
		t.Errorf("after swap, exe should be new, got %q", string(got))
	}
	// The backup should exist beside the target (the boot marker is
	// retired by the swap; the next healthy boot rearms it).
	prev, err := os.ReadFile(tgt.backup)
	if err != nil {
		t.Fatalf("backup should exist: %v", err)
	}
	if string(prev) != "old binary" {
		t.Errorf("backup should contain old binary, got %q", string(prev))
	}
	// The boot marker should be deleted.
	if fileExists(dir, ".boot_completed") {
		t.Error("boot marker should have been deleted before swap")
	}
	// And the record beside the target records the swap: whose, not yet
	// booted, both hashes — so the next boot can tell first-boot from
	// failed-boot, and only its owner's.
	assertRecord(t, tgt, ownerForTest(t, dir), 0, "old binary", string(newBinary))
	// Nothing is kept in the identity's own data directory any more.
	if fileExists(dir, previousFile) || fileExists(dir, pendingFile) {
		t.Error("a swap wrote recovery state into the data directory")
	}
}

func TestSwapBinaryFailureCleanup(t *testing.T) {
	dir := t.TempDir()
	// The target exe does not exist, so the swap fails at its FIRST
	// step — before the backup, the tombstone, or the marker deletion.
	// A swap that fails must leave the system exactly as it found it:
	// the old order deleted the marker first, so a swap that then
	// failed had disarmed boot health for nothing.
	writeFile(t, dir, ".boot_completed", "ok")
	exePath := filepath.Join(dir, "subdir", "aii")
	tgt := targetAt(exePath)
	if err := swapBinary(tgt, []byte("new"), nil, dir, ownerForTest(t, dir), ""); err == nil {
		t.Fatal("swapBinary with no readable current binary should fail")
	}
	if !fileExists(dir, ".boot_completed") {
		t.Error("a swap that failed before installing anything must not disarm boot health")
	}
	if exists(tgt.backup) || exists(tgt.record) || fileExists(dir, previousFile) || fileExists(dir, pendingFile) {
		t.Error("a failed swap must leave no update state behind")
	}
}

func TestExtractFromTarGz(t *testing.T) {
	// Build a real tar.gz with an "aii" entry containing a known binary.
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

	got, _, err := extractFromTarGz("linux", buf.Bytes())
	if err != nil {
		t.Fatalf("extractFromTarGz happy path: %v", err)
	}
	if string(got) != string(binaryContent) {
		t.Errorf("extracted content = %q, want %q", string(got), string(binaryContent))
	}
}

func TestExtractFromTarGzNotFound(t *testing.T) {
	// Build a tar.gz with a non-aii entry — should fail with "not found".
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

	_, _, err := extractFromTarGz("linux", buf.Bytes())
	if err == nil {
		t.Fatal("archive without 'aii' entry should fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("should fail with 'not found', got: %v", err)
	}
}

func TestExtractFromZipNotFound(t *testing.T) {
	// An invalid zip should fail gracefully — no panic, no binary returned.
	_, _, err := extractFromZip("windows", []byte("PK\x03\x04"))
	if err == nil {
		t.Fatal("invalid zip should return an error, not nil")
	}
}

// root exposes the signer's public envelope for contract tests.
func (s *testReleaseSigner) root() *sigenvelope.PublicKeyEnvelope { return s.env }
