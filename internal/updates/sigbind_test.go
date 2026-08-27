package updates

import (
	"strings"
	"testing"
)

// sigbind_test.go — the relabel/replay family (external review P1,
// 2026-08-26). The signature must bind every claim the verifier acts
// on; each test here is one unsigned claim an attacker with the
// GitHub account (but not the platform key) would swap.

// An old validly-signed archive republished under a higher version tag.
func TestVerifyReleaseSigRelabeledVersion(t *testing.T) {
	sig, root := releaseSignature(t, "old_archive_v1")
	hash := sha256hex([]byte("old archive"))

	err := verifyReleaseSig(sig, root, hash, "v9.9.9", "linux", "amd64")
	if err == nil {
		t.Fatal("a version relabel verified — the downgrade/upgrade confusion attack is open")
	}
	if !strings.Contains(err.Error(), "relabel refused") {
		t.Fatalf("refusal must name the relabel, got: %v", err)
	}
}

// One platform's archive served under another platform's asset name.
func TestVerifyReleaseSigCrossPlatformReplay(t *testing.T) {
	sig, root := releaseSignature(t, "linux_archive_v1")
	hash := sha256hex([]byte("linux archive"))

	err := verifyReleaseSig(sig, root, hash, "v1.0.0", "darwin", "arm64")
	if err == nil {
		t.Fatal("a cross-platform replay verified")
	}
	if !strings.Contains(err.Error(), "cross-platform replay refused") {
		t.Fatalf("refusal must name the replay, got: %v", err)
	}

	// ARCH ALONE must refuse too (Method review 2026-08-26: with both
	// coordinates swapped above, an implementation that checks platform
	// and forgets arch still passes — the copy-paste mutant this pins).
	err = verifyReleaseSig(sig, root, hash, "v1.0.0", "linux", "arm64")
	if err == nil {
		t.Fatal("an arch-only replay verified — the arch field is not actually checked")
	}
}

// A signature from the pre-binding format ({archive_hash} only) is not
// grandfathered: no public release was ever signed with it, so any
// such signature in the wild is a fabrication or a stale artifact.
func TestVerifyReleaseSigLegacyPayloadRefused(t *testing.T) {
	sig, root := releaseSignature(t, "legacy")
	hash := sha256hex([]byte("archive content"))

	err := verifyReleaseSig(sig, root, hash, "v1.0.0", "linux", "amd64")
	if err == nil {
		t.Fatal("a pre-binding payload verified")
	}
	if !strings.Contains(err.Error(), "pre-binding format") {
		t.Fatalf("refusal must name the format age, got: %v", err)
	}
}

// The signer must state build provenance; an empty source_rev is a
// signer that skipped the claim.
func TestVerifyReleaseSigEmptySourceRevRefused(t *testing.T) {
	sig, root := releaseSignature(t, "empty_source")
	hash := sha256hex([]byte("archive content"))

	err := verifyReleaseSig(sig, root, hash, "v1.0.0", "linux", "amd64")
	if err == nil {
		t.Fatal("a payload with no source_rev verified")
	}
	if !strings.Contains(err.Error(), "source_rev") {
		t.Fatalf("refusal must name the missing provenance, got: %v", err)
	}
}
