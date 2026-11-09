package updates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// guardedFixture is a checker offered a signed release 9.9.9 whose trust
// directory holds an empty list, with its signer and the digest of the
// release's signed payload, so a test can hand it a set of its own (setOf).
func guardedFixture(t *testing.T, dir string) (*Checker, *testReleaseSigner, string) {
	t.Helper()
	const ver = "9.9.9"
	archive := releaseArchive(t, []byte("new binary"))
	signer := sharedReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "guarded0",
	})
	var env sigenvelope.Envelope
	if err := json.Unmarshal(sig, &env); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			_, _ = w.Write(archive)
		case "/sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := newTestChecker(signer.env, dir, &githubRelease{TagName: "v" + ver, Assets: []githubAsset{
		{Name: assetName(ver), BrowserDownloadURL: srv.URL + "/archive"},
		{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
	}})
	c.state.setAvailable(ver)
	return c, signer, env.PayloadSHA256
}

// setOf loads the signer's list at epoch from a directory of its own.
func setOf(t *testing.T, s *testReleaseSigner, epoch int, revoked ...string) *packagefmt.RevocationStatusSet {
	t.Helper()
	entries := []any{}
	sort.Strings(revoked) // a list is strictly sorted, or it does not load
	for _, d := range revoked {
		entries = append(entries, map[string]any{"artifact_kind": artifactKindReleaseSig, "payload_sha256": d})
	}
	dir := t.TempDir()
	raw := s.signPayloadAs(t, packagefmt.ArtifactKindRevocationStatus, map[string]any{"schema_version": 1, "trust_epoch": epoch, "revoked": entries})
	if err := os.WriteFile(filepath.Join(dir, "aiii_platform_release_status.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return packagefmt.LoadRevocationStatus(dir, packagefmt.TrustRoots{PlatformRelease: s.env}, nil)
}

func TestTheUpdaterAsksTheIdentitysOwnLists(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	c, signer, digest := guardedFixture(t, dir)
	c.SetTrustSet(func() *packagefmt.RevocationStatusSet { return setOf(t, signer, 2, digest) })
	err := c.applyTo(t.Context(), exe)
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("a release the identity's list withdraws was not refused for it: %v", err)
	}
	assertApplyFile(t, exe, "old binary")

	refused := packagefmt.LoadRevocationStatus(t.TempDir(), packagefmt.TrustRoots{PlatformRelease: signer.env}, emptyGuard{})
	c.SetTrustSet(func() *packagefmt.RevocationStatusSet { return refused })
	if err := c.applyTo(t.Context(), exe); err == nil || !strings.Contains(err.Error(), "must be present") {
		t.Fatalf("with the identity's platform list unavailable the release was not refused for it: %v", err)
	}
	assertApplyFile(t, exe, "old binary")
}

// AND ASKS AGAIN AT THE LAST LOOK. A list that withdraws the release lands
// while it is being fetched and verified; the swap does not happen.
func TestAListThatLandsDuringTheDownloadStopsTheSwap(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	c, signer, digest := guardedFixture(t, dir)
	held := setOf(t, signer, 1)
	c.SetTrustSet(func() *packagefmt.RevocationStatusSet { return held })
	c.afterVerified = func() { held = setOf(t, signer, 2, digest) }
	err := c.applyTo(t.Context(), exe)
	if err == nil || !strings.Contains(err.Error(), "at the last look") {
		t.Fatalf("a list that landed during the download did not stop the swap: %v", err)
	}
	assertApplyFile(t, exe, "old binary")
}

// emptyGuard is an identity's guard with nothing accepted yet.
type emptyGuard struct{}

func (emptyGuard) TrustEpochHighWater(string) (int64, string, bool, error) { return 0, "", false, nil }
func (emptyGuard) AcceptTrustEpoch(string, int64, string) error            { return nil }

// ownFixture is a checker running 1.0.0 whose own release carries this
// install's signed asset, served by a test server that counts the fetches
// of its signature.
func ownFixture(t *testing.T) (*Checker, *testReleaseSigner, string, *int) {
	t.Helper()
	dir := t.TempDir()
	signer := sharedReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	platform, arch := hostTarget()
	sig := signer.signReleasePayload(t, releaseArchivePayload{ArchiveHash: strings.Repeat("b", 64), Version: "1.0.0",
		Platform: platform, Arch: arch, SourceRev: "own0"})
	var env sigenvelope.Envelope
	if err := json.Unmarshal(sig, &env); err != nil {
		t.Fatal(err)
	}
	fetched := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/own.sig" {
			*fetched++
			_, _ = w.Write(sig)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := newTestChecker(signer.env, dir, nil)
	c.httpClient = redirectTo(srv) // the release source answers 404: nothing published
	name := assetName("1.0.0")
	c.ownRelease = func(context.Context) (*githubRelease, error) {
		return &githubRelease{TagName: "v1.0.0", Assets: []githubAsset{
			{Name: name, BrowserDownloadURL: srv.URL + "/own"},
			{Name: SigAssetName(name), BrowserDownloadURL: srv.URL + "/own.sig"},
		}}, nil
	}
	return c, signer, env.PayloadSHA256, fetched
}

// THE RUNNING RELEASE CAN BE WITHDRAWN, AND IS SAID (the design note's
// item 8). While the identity's platform list withdraws nothing, nothing
// is fetched; once it names releases, this program's own signature is
// fetched once and asked; when the list withdraws it, the card is told;
// when it names others, nothing is said; and what cannot be fetched is
// said as not known.
func TestTheRunningReleaseWithdrawnIsSaid(t *testing.T) {
	c, signer, own, fetched := ownFixture(t)
	held := setOf(t, signer, 1)
	c.SetTrustSet(func() *packagefmt.RevocationStatusSet { return held })
	c.lookAtOwnRelease(t.Context())
	if snap := c.State().Snapshot("1.0.0"); snap.RunningWithdrawn || snap.RunningUnknown != "" || *fetched != 0 {
		t.Fatalf("with nothing withdrawn: %+v, %d fetches", snap, *fetched)
	}
	held = setOf(t, signer, 2, "sha256:"+strings.Repeat("c", 64))
	c.lookAtOwnRelease(t.Context())
	if snap := c.State().Snapshot("1.0.0"); snap.RunningWithdrawn || snap.RunningUnknown != "" || *fetched != 1 {
		t.Fatalf("with another release withdrawn: %+v, %d fetches", snap, *fetched)
	}
	// On the check's own beat.
	held = setOf(t, signer, 3, "sha256:"+strings.Repeat("c", 64), own)
	c.tick(t.Context())
	if snap := c.State().Snapshot("1.0.0"); !snap.RunningWithdrawn || *fetched != 1 {
		t.Fatalf("this program's release is withdrawn and the card is not told, or it was fetched again: %+v, %d fetches", snap, *fetched)
	}

	c2, signer2, _, _ := ownFixture(t)
	c2.ownRelease = func(context.Context) (*githubRelease, error) { return nil, errors.New("offline") }
	withEntries := setOf(t, signer2, 2, "sha256:"+strings.Repeat("c", 64))
	c2.SetTrustSet(func() *packagefmt.RevocationStatusSet { return withEntries })
	c2.lookAtOwnRelease(t.Context())
	if snap := c2.State().Snapshot("1.0.0"); snap.RunningWithdrawn || !strings.Contains(snap.RunningUnknown, "could not be read: offline") {
		t.Fatalf("what could not be told is not said: %+v", snap)
	}
}
