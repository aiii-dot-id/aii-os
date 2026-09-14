package updates

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestEvidenceAndReleaseSignaturesNeverPassForEachOther(t *testing.T) {
	dir := t.TempDir()
	signer := newTestReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	trust := dir + "/trust"
	bundle := []byte("the sealed evidence tarball")
	release := releaseArchive(t, []byte("the shipped binary"))

	evidenceWant := ReleasePayload{ArchiveHash: ArchiveHash(bundle), Version: "0.1.0", Platform: EvidencePlatform, Arch: EvidenceArch, SourceRev: "0adb92a"}
	evidenceSig := signer.signPayloadAs(t, ArtifactKindEvidence, map[string]any{
		"archive_hash": evidenceWant.ArchiveHash, "version": evidenceWant.Version,
		"platform": evidenceWant.Platform, "arch": evidenceWant.Arch, "source_rev": evidenceWant.SourceRev,
	})
	if err := VerifyEvidence(evidenceSig, bundle, signer.root(), trust, evidenceWant); err != nil {
		t.Fatalf("an evidence signature must verify as evidence: %v", err)
	}
	if err := VerifyRelease(evidenceSig, bundle, signer.root(), trust, evidenceWant); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("an evidence signature must never pass as a release: %v", err)
	}

	releaseWant := ReleasePayload{ArchiveHash: ArchiveHash(release), Version: "1.2.3", Platform: "linux", Arch: "amd64", SourceRev: "cafebabe1234"}
	releaseSig := signer.signReleasePayload(t, map[string]any{
		"archive_hash": releaseWant.ArchiveHash, "version": releaseWant.Version,
		"platform": releaseWant.Platform, "arch": releaseWant.Arch, "source_rev": releaseWant.SourceRev,
	})
	if err := VerifyRelease(releaseSig, release, signer.root(), trust, releaseWant); err != nil {
		t.Fatalf("fixture: the release signature must verify as a release: %v", err)
	}
	if err := VerifyEvidence(releaseSig, release, signer.root(), trust, releaseWant); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a release signature must never pass as evidence: %v", err)
	}

	// .
	if err := VerifyEvidence(evidenceSig, append([]byte("x"), bundle...), signer.root(), trust, evidenceWant); err == nil || !strings.Contains(err.Error(), "hashes to") {
		t.Fatalf("an altered bundle must fail by hash: %v", err)
	}
}
