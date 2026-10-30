package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// A SERVING RELEASE IS ASKED ABOUT WHAT WAS VERIFIED, NOT ABOUT ITS FILE.
// Its package file can be replaced by an update or removed; Recheck reads
// none, and tests the signed objects kept from its verification against the
// trust as it stands now: unchanged, it holds; its release revoked by the
// certifier, it is refused for that.
func TestARecheckTestsTheKeptEvidenceWithNoPackageFile(t *testing.T) {
	s := signer(t)
	spec := brokerPkgSpec("id.example.kept", "1.0.0", []string{"plain"}, wasmgen.Responder(), []string{"ring4.kv"})
	if err := s.SignT1(&spec); err != nil {
		t.Fatal(err)
	}
	path := writePkg(t, spec)
	roots := signerRoots(t)
	h := NewRuntime(RuntimeWires{Options: func() *Options { return &Options{Roots: roots} }})
	ev, err := h.Verify(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := h.Recheck(context.Background(), ev); err != nil {
		t.Fatalf("the kept evidence under the roots it was verified by: %v", err)
	}

	// The certifier revokes this release's signature (plugin.manifest).
	pair, err := canonicaljson.CanonicalizeV1([]byte(`{"package_hash":"` + ev.PackageHash + `","manifest_hash":"` + ev.ManifestHash + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	revoking, err := s.Certifier.SignRevocationStatus(2, []packagetest.RevocationEntry{{ArtifactKind: packagetest.ArtifactKindManifestSig, PayloadSHA256: sigenvelope.SHA256Prefixed(pair)}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.MintEmptyStatus(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFileCertifier), revoking, 0o600); err != nil {
		t.Fatal(err)
	}
	roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
	err = h.Recheck(context.Background(), ev)
	var verr *packagefmt.Error
	if !errors.As(err, &verr) || verr.Reason != packagefmt.ReasonTrustPayloadRevoked {
		t.Fatalf("a revoked release's kept evidence must be refused as revoked: %v", err)
	}
}
