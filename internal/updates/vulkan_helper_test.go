package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

// A LINUX TARBALL INSTALL THAT UPDATED IN-APP NEVER RECEIVED ITS VULKAN
// HELPER. aii is static on Linux and reads Vulkan memory by running
// vulkancap.Helper from beside itself; packaging/release.sh puts that helper
// in every Linux update archive, at the root beside aii — and the updater
// extracted aii alone. Vulkan stayed unmeasured, every Vulkan component set
// was refused, and the host fell back to CPU without a word. These tests run
// the real apply against a signed archive in release.sh's layout. The path
// they drive is the bare tar.gz path, which Linux and a bare macOS install
// share; only the archive's contents decide whether a helper comes with it.

func skipWithoutTarball(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("this test lays out a tar.gz by hand; the Windows archive is a zip")
	}
}

// hostBeside is the file these tests drive through the real apply: the last
// one this host's release installs beside aii — the Vulkan helper on Linux,
// the launcher on a bare macOS install, which has no helper.
func hostBeside() string {
	names := installedBeside(packagefmt.HostPlatform())
	return names[len(names)-1]
}

// hostProgram is the name the host's archive carries its program under
// (aii.exe on Windows), and so the bytes contractArchive gives it.
func hostProgram() string { return ShippedPrograms(packagefmt.HostPlatform())[0].Name }

// installedExecutable reports the mode an install leaves a file with, where
// the system has one: Windows keeps no execute bit to hold a file to.
func installedExecutable(fi os.FileInfo) bool {
	return runtime.GOOS == "windows" || fi.Mode().Perm() == 0o755
}

// besideNamed is the bytes an extraction took for one file beside aii, nil
// when it took none.
func besideNamed(beside []installedFile, name string) []byte {
	for _, f := range beside {
		if f.name == name {
			return f.data
		}
	}
	return nil
}

type tarEntry struct {
	hdr  tar.Header
	body []byte
}

func regularEntry(name string, body []byte) tarEntry {
	return tarEntry{hdr: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}, body: body}
}

func tarGzOf(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := e.hdr
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseShLayout is a Linux archive as the release writes it: aii, a
// licence text and the Vulkan helper at the root.
func releaseShLayout(t *testing.T, binary, helper []byte) []byte {
	return layoutWith(t, vulkancap.Helper, binary, helper)
}

// hostLayout is the same archive carrying this host's file beside aii.
func hostLayout(t *testing.T, binary, beside []byte) []byte {
	return layoutWith(t, hostBeside(), binary, beside)
}

func layoutWith(t *testing.T, name string, binary, beside []byte) []byte {
	return tarGzOf(t,
		regularEntry("aii", binary),
		regularEntry("LICENSE", []byte("the license")),
		regularEntry(name, beside))
}

// helperRig is one release signer, and the revocation snapshot its root
// needs, signed once and copied into each install: SLH-DSA signing is the
// cost of these tests, not the apply.
type helperRig struct {
	signer *testReleaseSigner
	trust  string
}

func newHelperRig(t *testing.T) *helperRig {
	t.Helper()
	r := &helperRig{signer: newTestReleaseSigner(t), trust: t.TempDir()}
	r.signer.provisionTrustDir(t, r.trust)
	return r
}

// helperInstall is one bare install in a temp directory: an old aii that
// booted healthy, and a checker holding a signed release of the archive given.
type helperInstall struct {
	dir, exe, helper string
	c                *Checker
	mu               sync.Mutex
	fetched          []string
}

func (rig *helperRig) install(t *testing.T, archive []byte) *helperInstall {
	t.Helper()
	dir := t.TempDir()
	in := &helperInstall{dir: dir, exe: filepath.Join(dir, "aii"), helper: filepath.Join(dir, hostBeside())}
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, markerFile, "ok")
	if err := os.CopyFS(dir, os.DirFS(rig.trust)); err != nil {
		t.Fatal(err)
	}
	const ver = "9.9.9"
	sig := rig.signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "vulkanhelper0",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in.mu.Lock()
		in.fetched = append(in.fetched, r.URL.Path)
		in.mu.Unlock()
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
	in.c = newTestChecker(rig.signer.env, dir, &githubRelease{
		TagName: "v" + ver,
		Assets: []githubAsset{
			{Name: assetName(ver), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	})
	in.c.state.setAvailable(ver)
	return in
}

func (in *helperInstall) apply() error { return in.c.applyTo(context.Background(), in.exe) }

// unswapped asserts the old aii stands exactly as a failure before the swap
// leaves it: same bytes, no backup, no tombstone, boot marker armed, no restart.
func (in *helperInstall) unswapped(t *testing.T) {
	t.Helper()
	assertApplyFile(t, in.exe, "old binary")
	if tgt := targetForTest(t, in.exe); exists(tgt.backup) || exists(tgt.record) {
		t.Error("aii was not swapped, so no backup or record may be left")
	}
	if !fileExists(in.dir, markerFile) {
		t.Error("aii was not swapped, so its healthy-boot marker must still stand")
	}
	if in.c.State().Snapshot("").NeedsRestart {
		t.Error("nothing was installed, so nothing needs a restart")
	}
}

func TestAnUpdateInstallsTheVulkanHelperItsArchiveCarries(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	helper := []byte("\x7fELF the vulkan helper from the signed archive")
	for _, beside := range []string{"", "an older helper"} {
		name := "first helper"
		if beside != "" {
			name = "replaces the helper beside aii"
		}
		t.Run(name, func(t *testing.T) {
			in := rig.install(t, hostLayout(t, []byte("new binary"), helper))
			if beside != "" {
				// Not executable: the installed mode must be the install's own.
				if err := os.WriteFile(in.helper, []byte(beside), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := in.apply(); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertApplyFile(t, in.exe, "new binary")
			assertApplyFile(t, targetForTest(t, in.exe).backup, "old binary")
			got, err := os.ReadFile(in.helper)
			if err != nil {
				t.Fatalf("THE HELPER WAS NOT INSTALLED beside the updated aii: %v — Vulkan stays unmeasured and every Vulkan set is refused", err)
			}
			if !bytes.Equal(got, helper) {
				t.Fatalf("the helper beside aii is %q, want the archive's member %q", got, helper)
			}
			fi, err := os.Lstat(in.helper)
			if err != nil {
				t.Fatal(err)
			}
			if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o755 {
				t.Fatalf("the helper is %v, want a regular executable file (-rwxr-xr-x)", fi.Mode())
			}
			// From the verified archive, not fetched on its own.
			in.mu.Lock()
			fetched := slices.Sorted(slices.Values(in.fetched))
			in.mu.Unlock()
			if !slices.Equal(fetched, []string{"/archive", "/sig"}) {
				t.Fatalf("the update fetched %v; the helper must come out of the signed archive, with nothing fetched beside it", fetched)
			}
			if !in.c.State().Snapshot("").NeedsRestart {
				t.Error("a completed update must still ask for its restart")
			}
		})
	}
}

// A helper that cannot be written fails the update before aii is touched: a
// new aii must never run without the helper it measures Vulkan with.
func TestAnUpdateWhoseHelperCannotBeWrittenLeavesAiiUnswapped(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	helper := []byte("\x7fELF the vulkan helper")
	in := rig.install(t, hostLayout(t, []byte("new binary"), helper))
	// A non-empty directory where the helper goes: no rename lands on it, for
	// root or anyone else, on every unix.
	if err := os.MkdirAll(filepath.Join(in.helper, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := in.apply()
	if err == nil {
		t.Fatal("the update reported success although its helper could not be written")
	}
	if !strings.Contains(err.Error(), hostBeside()) {
		t.Errorf("the failure must name the helper it could not install: %v", err)
	}
	in.unswapped(t)
	if debris, _ := filepath.Glob(filepath.Join(in.dir, "."+hostBeside()+".*")); len(debris) != 0 {
		t.Errorf("the failed helper write left %v behind", debris)
	}

	// The failure is clean, so the next attempt is an ordinary one.
	if err := os.RemoveAll(in.helper); err != nil {
		t.Fatal(err)
	}
	if err := in.apply(); err != nil {
		t.Fatalf("a retry once the helper's place is free: %v", err)
	}
	assertApplyFile(t, in.exe, "new binary")
	assertApplyFile(t, in.helper, string(helper))
}

// The helper is in place BEFORE aii is published — read at the moment
// between the two (replaceStep), when the helper is the update's and aii is
// still the one that booted healthy: a new aii never runs without it.
//
// And a swap that fails leaves no part of the update behind: failed before
// anything beside aii was written (the backup cannot be written), the helper
// is untouched; failed after (aii cannot be published), the helper that was
// displaced is put back from what was kept. The old aii never keeps the
// newer release's helper, and nothing a rollback would need is left over.
func TestAnUpdatePlacesTheHelperBeforeItSwapsAii(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	helper := []byte("\x7fELF the vulkan helper")
	seam := func(t *testing.T, at string, do func()) {
		t.Helper()
		previous := replaceStep
		t.Cleanup(func() { replaceStep = previous })
		replaceStep = func(step string) {
			if step == at {
				do()
			}
		}
	}
	settled := func(t *testing.T, in *helperInstall) {
		t.Helper()
		tgt := targetForTest(t, in.exe)
		if exists(tgt.record) {
			t.Error("the swap failed before publishing, so its record must be withdrawn")
		}
		if exists(besideBackup(tgt, hostBeside())) {
			t.Error("the swap failed before publishing, so what it kept of the helper must be withdrawn")
		}
		if !fileExists(in.dir, markerFile) {
			t.Error("the old aii still stands, so its healthy-boot marker must stand too")
		}
	}

	t.Run("in place when aii is published", func(t *testing.T) {
		in := rig.install(t, hostLayout(t, []byte("new binary"), helper))
		seen := false
		seam(t, "beside-installed", func() {
			seen = true
			assertApplyFile(t, in.helper, string(helper))
			assertApplyFile(t, in.exe, "old binary")
		})
		if err := in.apply(); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if !seen {
			t.Fatal("the swap never stood between the helper's install and aii's publish")
		}
		assertApplyFile(t, in.exe, "new binary")
	})

	t.Run("a swap that fails before it leaves the helper untouched", func(t *testing.T) {
		in := rig.install(t, hostLayout(t, []byte("new binary"), helper))
		if err := os.WriteFile(in.helper, []byte("the helper that stood"), 0o755); err != nil {
			t.Fatal(err)
		}
		tgt := targetForTest(t, in.exe)
		if err := os.MkdirAll(filepath.Join(tgt.backup, "occupied"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := in.apply(); err == nil {
			t.Fatal("the swap's backup could not be written, so the update must fail")
		}
		assertApplyFile(t, in.exe, "old binary")
		assertApplyFile(t, in.helper, "the helper that stood")
		if err := os.RemoveAll(tgt.backup); err != nil {
			t.Fatal(err)
		}
		settled(t, in)
	})

	t.Run("a swap that fails after it puts the helper back", func(t *testing.T) {
		for _, stood := range []string{"the helper that stood", ""} {
			in := rig.install(t, hostLayout(t, []byte("new binary"), helper))
			if stood != "" {
				if err := os.WriteFile(in.helper, []byte(stood), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			// aii's place is taken between the helper's install and its own
			// publish: a non-empty directory, which no rename lands on.
			seam(t, "beside-installed", func() {
				assertApplyFile(t, in.helper, string(helper))
				if err := os.Rename(in.exe, in.exe+".aside"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(in.exe, "occupied"), 0o755); err != nil {
					t.Fatal(err)
				}
			})
			if err := in.apply(); err == nil {
				t.Fatal("aii could not be published, so the update must fail")
			}
			if err := os.RemoveAll(in.exe); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(in.exe+".aside", in.exe); err != nil {
				t.Fatal(err)
			}
			assertApplyFile(t, in.exe, "old binary")
			if stood == "" {
				if exists(in.helper) {
					t.Error("the update's helper was left where none stood before it")
				}
			} else {
				assertApplyFile(t, in.helper, stood)
			}
			settled(t, in)
		}
	})
}

// An archive with no helper — every one before the helper existed, and every
// macOS one — updates exactly as it always has, and leaves whatever stands
// beside aii alone.
func TestAnArchiveWithoutTheHelperUpdatesAsItAlwaysHas(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	for _, tc := range []struct {
		name, beside string
		archive      []byte
	}{
		{"aii only", "", tarGzOf(t, regularEntry("aii", []byte("new binary")))},
		{"aii and LICENSE", "the helper already here", tarGzOf(t, regularEntry("aii", []byte("new binary")), regularEntry("LICENSE", []byte("the license")))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := rig.install(t, tc.archive)
			if tc.beside != "" {
				if err := os.WriteFile(in.helper, []byte(tc.beside), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := in.apply(); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertApplyFile(t, in.exe, "new binary")
			assertApplyFile(t, targetForTest(t, in.exe).backup, "old binary")
			fi, err := os.Lstat(in.helper)
			if tc.beside == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("an archive with no helper put something at %s: %v %v", in.helper, fi, err)
				}
				return
			}
			if err != nil || fi.Mode().Perm() != 0o700 {
				t.Fatalf("the helper beside aii was disturbed: %v %v", fi, err)
			}
			assertApplyFile(t, in.helper, tc.beside)
		})
	}
}

// malformedHelpers is every shape of helper entry release.sh does not write —
// it writes one regular, non-empty file at the archive root, once, within the
// entry limit — except the oversized one, which costs a quarter-gigabyte and
// is driven once, through the real apply.
func malformedHelpers(name string) map[string][]tarEntry {
	helper := []byte("\x7fELF the vulkan helper")
	link := func(flag byte, target string) []tarEntry {
		return []tarEntry{{hdr: tar.Header{Name: name, Typeflag: flag, Linkname: target, Mode: 0o777}}}
	}
	return map[string][]tarEntry{
		"outside the archive": {regularEntry("../"+name, helper)},
		"absolute":            {regularEntry("/"+name, helper)},
		"in a subdirectory":   {regularEntry("bin/"+name, helper)},
		"dot-prefixed":        {regularEntry("./"+name, helper)},
		"a symlink":           link(tar.TypeSymlink, "/bin/sh"),
		"a hard link":         link(tar.TypeLink, "aii"),
		"a directory":         {{hdr: tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755}}},
		"empty":               {regularEntry(name, nil)},
		"twice":               {regularEntry(name, helper), regularEntry(name, []byte("the other one"))},
	}
}

// withAii is a release.sh archive whose helper entries are the ones given.
func withAii(helper ...tarEntry) []tarEntry {
	return append([]tarEntry{regularEntry("aii", []byte("new binary")), regularEntry("LICENSE", []byte("the license"))}, helper...)
}

// A helper entry in any other shape refuses the whole archive. Skipping it
// would install aii without its helper, which is the defect; following it is
// how an archive writes outside its directory.
func TestAnUpdateTakesTheHelperOnlyInTheShapeReleaseShWrites(t *testing.T) {
	helper := []byte("\x7fELF the vulkan helper")
	binary, beside, err := extractFromTarGz("linux", releaseShLayout(t, []byte("new binary"), helper))
	if got := besideNamed(beside, vulkancap.Helper); err != nil || string(binary) != "new binary" || !bytes.Equal(got, helper) {
		t.Fatalf("the release's own layout: binary %q, helper %q, %v", binary, got, err)
	}
	for name, bad := range malformedHelpers(vulkancap.Helper) {
		t.Run(name, func(t *testing.T) {
			binary, got, err := extractFromTarGz("linux", tarGzOf(t, withAii(bad...)...))
			if err == nil {
				t.Fatalf("a malformed helper entry was taken: binary %q, helper %q", binary, got)
			}
			if !strings.Contains(err.Error(), vulkancap.Helper) {
				t.Errorf("the refusal must name the helper entry it refused: %v", err)
			}
		})
	}
}

// And the refusal reaches the install as one: through the real apply, the
// traversal, the symlink and the oversized entry each leave aii as it was and
// nothing where the helper goes.
func TestAMalformedHelperEntryRefusesTheUpdate(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	bad := malformedHelpers(hostBeside())
	cases := map[string]func() []tarEntry{
		"outside the archive": func() []tarEntry { return bad["outside the archive"] },
		"a symlink":           func() []tarEntry { return bad["a symlink"] },
		"over the entry limit": func() []tarEntry {
			return []tarEntry{regularEntry(hostBeside(), bytes.Repeat([]byte("A"), int(maxDownloadSize)+1))}
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			in := rig.install(t, tarGzOf(t, withAii(entries()...)...))
			err := in.apply()
			if err == nil {
				t.Fatal("an archive whose helper entry is malformed was installed")
			}
			if !strings.Contains(err.Error(), hostBeside()) {
				t.Errorf("the refusal must name the helper entry it refused: %v", err)
			}
			in.unswapped(t)
			if _, err := os.Lstat(in.helper); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused archive left something at %s: %v", in.helper, err)
			}
		})
	}
}
