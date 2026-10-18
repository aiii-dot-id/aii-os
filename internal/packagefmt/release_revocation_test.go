package packagefmt_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// digestOf is the digest a revocation names: the sha256 of a signed
// payload in its canonical form.
func digestOf(t *testing.T, payload json.RawMessage) string {
	t.Helper()
	canonical, err := canonicaljson.CanonicalizeV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	return sigenvelope.SHA256Prefixed(canonical)
}

// releaseRoots is a platform_release root whose snapshot, at epoch 2,
// revokes the given entries.
func releaseRoots(t *testing.T, signer *packagetest.Role, entries []packagetest.RevocationEntry) packagefmt.TrustRoots {
	t.Helper()
	status, err := signer.SignRevocationStatus(2, entries)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), status, 0o644); err != nil {
		t.Fatal(err)
	}
	return packagefmt.ReleaseTrustRoots(dir, signer.Env)
}

// A HOST RELEASE CAN BE REVOKED. The updater asks the platform_release
// root's snapshot about every release it verifies, and the answer could
// only ever be "not revoked": the root's row of kinds named the plugin
// kind alone, so a snapshot that listed a host release was refused whole
// as outside the root's domain.
//
// Here the root's snapshot lists one host release. It loads. The release
// it names is refused as revoked; another release is not; and the same
// digest listed as a plugin's platform signature does not revoke the
// host release, because a revocation names a kind and a payload together.
func TestAHostReleaseCanBeRevokedByTheRootThatSignsIt(t *testing.T) {
	root, _, err := packagetest.NewPlatformRelease("aiii_platform_release_pkgtest")
	if err != nil {
		t.Fatal(err)
	}
	bad := json.RawMessage(`{"archive_hash":"` + strings.Repeat("a", 64) + `","version":"0.1.99","platform":"linux","arch":"amd64","source_rev":"0000000"}`)
	good := json.RawMessage(`{"archive_hash":"` + strings.Repeat("b", 64) + `","version":"0.1.100","platform":"linux","arch":"amd64","source_rev":"1111111"}`)

	roots := releaseRoots(t, root, []packagetest.RevocationEntry{{ArtifactKind: packagefmt.ArtifactKindHostRelease, PayloadSHA256: digestOf(t, bad)}})
	err = packagefmt.CheckReleaseRevocation(roots, packagefmt.ArtifactKindHostRelease, bad)
	var refusal *packagefmt.Error
	if !errors.As(err, &refusal) || refusal.Reason != packagefmt.ReasonTrustPayloadRevoked {
		t.Fatalf("a host release the snapshot lists must be refused as revoked: %v", err)
	}
	if err := packagefmt.CheckReleaseRevocation(roots, packagefmt.ArtifactKindHostRelease, good); err != nil {
		t.Fatalf("a host release the snapshot does not list: %v", err)
	}

	asPlugin := releaseRoots(t, root, []packagetest.RevocationEntry{{ArtifactKind: "plugin.platform_release", PayloadSHA256: digestOf(t, bad)}})
	if err := packagefmt.CheckReleaseRevocation(asPlugin, packagefmt.ArtifactKindHostRelease, bad); err != nil {
		t.Fatalf("a digest listed as a plugin's signature revoked a host release: %v", err)
	}
}

// AND ONLY BY THAT ROOT. The other two roots sign nothing of the host's,
// and a snapshot of theirs that lists a host release is refused whole,
// as any kind outside a root's domain is.
func TestAHostReleaseIsOutsideTheOtherRootsDomains(t *testing.T) {
	signer, err := packagetest.NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		role *packagetest.Role
		file string
	}{{signer.Certifier, packagetest.StatusFileCertifier}, {signer.Reviewer, packagetest.StatusFileReviewer}} {
		status, err := m.role.SignRevocationStatus(2, []packagetest.RevocationEntry{{ArtifactKind: packagefmt.ArtifactKindHostRelease, PayloadSHA256: "sha256:" + strings.Repeat("c", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, m.file), status, 0o644); err != nil {
			t.Fatal(err)
		}
		roots := packagefmt.TrustRoots{PublisherCertifier: signer.Certifier.Env, Reviewer: signer.Reviewer.Env}
		set := packagefmt.LoadRevocationStatus(dir, roots, nil)
		says := strings.Join(set.Describe(), "\n")
		if _, usable := set.Epoch(m.role.Env.KeyType); usable || !strings.Contains(says, "revocation "+m.role.Env.KeyType+": ABSENT") || !strings.Contains(says, `"release.platform_release" is outside the `+m.role.Env.KeyType+` domain`) {
			t.Fatalf("%s: a snapshot listing a host release must be refused as outside the root's domain: usable=%v\n%s", m.file, usable, says)
		}
	}
}
