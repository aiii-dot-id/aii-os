package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// packageRig is an install the system's package manager made: a program
// path in the package's file list, a data directory with the trust
// directory provisioned, and a checker holding a release whose package
// and signature a recording server answers.
type packageRig struct {
	c       *Checker
	signer  *testReleaseSigner
	sig     []byte
	dir     string
	ver     string
	mu      sync.Mutex
	fetched []string
}

// newPackageRig signs pkg as the release would, after signed has changed
// what the statement says (nil: nothing changed), and offers assets (nil:
// the package and its signature under their contract names).
func newPackageRig(t *testing.T, pkg []byte, signed func(*releaseArchivePayload), assets ...string) *packageRig {
	t.Helper()
	signer := newTestReleaseSigner(t)
	dir := t.TempDir()
	signer.provisionTrustDir(t, dir)
	rig := &packageRig{dir: dir, ver: "9.9.9", signer: signer}
	_, arch := hostTarget()
	name := PackageAssetName(rig.ver, arch)
	statement := releaseArchivePayload{ArchiveHash: sha256hex(pkg), Version: rig.ver, Platform: "linux", Arch: arch, SourceRev: "package0"}
	if signed != nil {
		signed(&statement)
	}
	sig := signer.signReleasePayload(t, statement)
	rig.sig = sig
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.mu.Lock()
		rig.fetched = append(rig.fetched, r.URL.Path)
		rig.mu.Unlock()
		switch r.URL.Path {
		case "/package":
			_, _ = w.Write(pkg)
		case "/sig":
			_, _ = w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if assets == nil {
		assets = []string{name, SigAssetName(name)}
	}
	release := &githubRelease{TagName: "v" + rig.ver}
	for _, a := range assets {
		url := srv.URL + "/package"
		if strings.HasSuffix(a, ".platform.sig") {
			url = srv.URL + "/sig"
		}
		release.Assets = append(release.Assets, githubAsset{Name: a, BrowserDownloadURL: url})
	}
	rig.c = newTestChecker(signer.env, dir, release)
	exe := filepath.Join(dir, "usr", "bin", "aii")
	list := filepath.Join(dir, "aii-os.list")
	writeFile(t, dir, "aii-os.list", "/.\n/usr\n/usr/bin\n"+exe+"\n/usr/share/doc/aii-os/copyright\n")
	rig.c.pkgListPath, rig.c.pkgExe = list, exe
	rig.c.state.setAvailable(rig.ver)
	return rig
}

func (rig *packageRig) prepared() PackageUpdate {
	rig.c.preparePackage(context.Background(), rig.ver)
	return rig.c.state.Snapshot("1.0.0").Package
}

func (rig *packageRig) fetches() int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return len(rig.fetched)
}

// THE PACKAGE THAT IS OFFERED IS ONE THIS PROGRAM VERIFIED. For an install
// the package manager made, the announced version's package and its
// signature are fetched, verified against the release's root as an update
// archive is, and kept in the identity's own directory; what is then said
// is that file's path. A second check verifies the kept file again from
// the disk and fetches nothing.
func TestAPackageInstallIsOfferedOnlyAPackageItVerified(t *testing.T) {
	pkg := []byte("the package's bytes, as the release built them")
	rig := newPackageRig(t, pkg, nil)
	got := rig.prepared()
	_, arch := hostTarget()
	want := filepath.Join(rig.dir, "updates", PackageAssetName(rig.ver, arch))
	if got.Version != rig.ver || got.Path != want || got.Refusal != "" {
		t.Fatalf("the verified package was not offered: %+v, want path %s", got, want)
	}
	if kept, err := os.ReadFile(want); err != nil || string(kept) != string(pkg) {
		t.Fatalf("the file offered is not the package that verified: %v", err)
	}
	first := rig.fetches()
	if again := rig.prepared(); again.Path != want || rig.fetches() != first {
		t.Fatalf("a second check fetched again (%d then %d) or lost the offer: %+v", first, rig.fetches(), again)
	}

	// THE FILE ON THE DISK IS VERIFIED EACH TIME IT IS OFFERED. Written
	// over after it verified, it is not offered as it stands: it is
	// fetched and verified again.
	if err := os.WriteFile(want, []byte("something else, put here later"), 0o644); err != nil {
		t.Fatal(err)
	}
	if healed := rig.prepared(); healed.Path != want || rig.fetches() == first {
		t.Fatalf("a package changed on the disk was offered without being fetched and verified again: %+v", healed)
	}
	if kept, _ := os.ReadFile(want); string(kept) != string(pkg) {
		t.Fatal("the changed file was left in place and offered")
	}

	// Up to date: nothing is announced, nothing is offered, nothing kept.
	rig.c.forgetPackages()
	if left, _ := filepath.Glob(filepath.Join(rig.dir, "updates", "*")); len(left) != 0 || rig.c.state.Snapshot("").Package != (PackageUpdate{}) {
		t.Fatalf("a package outlived the version it was for: %v", left)
	}
}

// WHAT DOES NOT VERIFY IS NOT OFFERED, AND THE REASON IS SAID. A package
// whose bytes are not the ones the statement names, a statement for
// another version or another platform, a release with a package and no
// signature, and a release with no package for this machine each leave no
// file and a refusal.
func TestAPackageThatDoesNotVerifyIsNotOffered(t *testing.T) {
	_, arch := hostTarget()
	name := PackageAssetName("9.9.9", arch)
	pkg := []byte("the package's bytes")
	for what, c := range map[string]struct {
		rig  func() *packageRig
		says string
	}{
		"bytes that are not the ones signed": {func() *packageRig {
			return newPackageRig(t, pkg, func(p *releaseArchivePayload) { p.ArchiveHash = sha256hex([]byte("other bytes")) })
		}, "did not verify"},
		"a statement for another version": {func() *packageRig {
			return newPackageRig(t, pkg, func(p *releaseArchivePayload) { p.Version = "9.9.8" })
		}, "did not verify"},
		"a statement for another platform": {func() *packageRig {
			return newPackageRig(t, pkg, func(p *releaseArchivePayload) { p.Platform = "macos" })
		}, "did not verify"},
		"a package and no signature":  {func() *packageRig { return newPackageRig(t, pkg, nil, name) }, "no signed package"},
		"no package for this machine": {func() *packageRig { return newPackageRig(t, pkg, nil, "aii-os_9.9.9_other.deb") }, "no signed package"},
	} {
		rig := c.rig()
		got := rig.prepared()
		if got.Path != "" || got.Version != rig.ver || !strings.Contains(got.Refusal, c.says) {
			t.Errorf("%s: %+v, want a refusal saying %q and no path", what, got, c.says)
		}
		if kept, _ := filepath.Glob(filepath.Join(rig.dir, "updates", "*.deb")); len(kept) != 0 {
			t.Errorf("%s: a package that did not verify was kept: %v", what, kept)
		}
	}
}

// A SIGNATURE BY A ROOT THIS PROGRAM DOES NOT HOLD OFFERS NOTHING. The
// release is whole and honestly signed, by someone else.
func TestAPackageSignedByAnotherRootIsNotOffered(t *testing.T) {
	rig := newPackageRig(t, []byte("the package's bytes"), nil)
	stranger := newTestReleaseSigner(t)
	rig.c.platformRoot = stranger.root
	if got := rig.prepared(); got.Path != "" || !strings.Contains(got.Refusal, "did not verify") {
		t.Fatalf("a package signed by a root this program does not hold was offered: %+v", got)
	}
}

// A PROGRAM THE PACKAGE MANAGER DID NOT INSTALL IS OFFERED NO PACKAGE. A
// copy an administrator put in a system directory by hand cannot replace
// itself either, and installing the package would not update that copy.
func TestAProgramThePackageManagerDidNotInstallIsOfferedNoPackage(t *testing.T) {
	rig := newPackageRig(t, []byte("pkg"), nil)
	rig.c.pkgExe = filepath.Join(rig.dir, "usr", "local", "bin", "aii")
	if got := rig.prepared(); got != (PackageUpdate{}) || rig.fetches() != 0 {
		t.Fatalf("a program outside the package was offered %+v after %d fetches", got, rig.fetches())
	}
	rig.c.pkgListPath = filepath.Join(rig.dir, "no-such-list")
	if got := rig.prepared(); got != (PackageUpdate{}) {
		t.Fatalf("with no package file list a package was offered: %+v", got)
	}
}

// A PACKAGE WHOSE SIGNATURE THE ROOT HAS REVOKED IS NOT OFFERED. The
// statement verifies, bytes and all; the authority behind it has since
// said it no longer stands, in the status file the identity holds.
func TestAPackageWhoseSignatureIsRevokedIsNotOffered(t *testing.T) {
	rig := newPackageRig(t, []byte("the package's bytes"), nil)
	var env sigenvelope.Envelope
	if err := json.Unmarshal(rig.sig, &env); err != nil {
		t.Fatal(err)
	}
	revoking := rig.signer.signPayloadAs(t, packagefmt.ArtifactKindRevocationStatus, map[string]any{
		"schema_version": 1, "trust_epoch": 2,
		"revoked": []any{map[string]any{"artifact_kind": artifactKindReleaseSig, "payload_sha256": env.PayloadSHA256}},
	})
	if err := os.WriteFile(filepath.Join(rig.dir, "trust", "aiii_platform_release_status.json"), revoking, 0o600); err != nil {
		t.Fatal(err)
	}
	got := rig.prepared()
	if got.Path != "" || !strings.Contains(got.Refusal, "refused") {
		t.Fatalf("a package whose signature is revoked was offered: %+v", got)
	}
	// AND IT IS REFUSED FOR THAT. Until the root's row of kinds named a
	// host release, this snapshot was itself refused whole at load, the
	// platform tier went absent with it, and the package was refused for
	// want of any snapshot: this test passed, and no release could be
	// revoked. The refusal must be the revocation.
	if !strings.Contains(got.Refusal, "revoked") || strings.Contains(got.Refusal, "outside the") {
		t.Fatalf("the package must be refused because its signature is revoked, and the refusal says: %s", got.Refusal)
	}
}

// A CHECK ON AN INSTALL THAT CANNOT REPLACE ITSELF PREPARES THE PACKAGE,
// AND AN UP-TO-DATE ONE KEEPS NONE. The governed check is the one place
// this is decided: with a newer release named and the program where only
// an administrator writes, the package is fetched and verified before
// anything is said; once the running version is the latest, what was
// kept is removed and nothing is offered.
func TestACheckPreparesThePackageAndAnUpToDateOneKeepsNone(t *testing.T) {
	pkg := []byte("the package's bytes")
	rig := newPackageRig(t, pkg, nil)
	var assets []map[string]any
	for _, a := range rig.c.cachedRelease.Assets {
		assets = append(assets, map[string]any{"name": a.Name, "browser_download_url": a.BrowserDownloadURL})
	}
	latest := "v" + rig.ver
	// One stand-in for the release source and its downloads: the test
	// transport sends every request here, keeping its path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/latest"):
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": latest, "assets": assets})
		case r.URL.Path == "/package":
			_, _ = w.Write(pkg)
		case r.URL.Path == "/sig":
			_, _ = w.Write(rig.sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	rig.c.httpClient = redirectTo(srv)
	rig.c.canStage = func() (bool, string) { return false, "the program is where only an administrator writes" }
	rig.c.state.setAvailable("")

	rig.c.tick(context.Background())
	got := rig.c.state.Snapshot("1.0.0")
	if got.AvailableVersion != rig.ver || got.Package.Path == "" || got.Package.Version != rig.ver {
		t.Fatalf("a check on an install that cannot replace itself prepared no package: %+v", got)
	}
	if kept, err := os.ReadFile(got.Package.Path); err != nil || string(kept) != string(pkg) {
		t.Fatalf("the package the check names is not on the disk as verified: %v", err)
	}

	// The operator installed it: the running version is the latest.
	rig.c.currentVer = func() string { return rig.ver }
	rig.c.tick(context.Background())
	after := rig.c.state.Snapshot(rig.ver)
	if after.AvailableVersion != "" || after.Package != (PackageUpdate{}) {
		t.Fatalf("an up-to-date install is still offered a package: %+v", after)
	}
	if left, _ := filepath.Glob(filepath.Join(rig.dir, "updates", "*")); len(left) != 0 {
		t.Fatalf("the package outlived the version it was for: %v", left)
	}
}
