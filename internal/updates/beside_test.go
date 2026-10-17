package updates

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// AN UPDATE INSTALLED aii AND NOT WHAT A RELEASE SHIPS BESIDE IT. The
// Windows installer writes a launcher and the licence texts next to
// aii.exe, and the menu entry of a Linux install runs aii-app; the 0.1.16
// archives carried none of them and the updater took aii alone (and, on
// Linux, the Vulkan helper), so an updated Windows install ran its first
// launcher for good. The archive and the updater now read one list
// (ShippedPrograms, installedBeside); these hold the updater to it.

// contractArchive is a platform's update archive exactly as the release
// contract lays it out (ArchiveFiles), each file's bytes its own path.
func contractArchive(t *testing.T, platform string) []byte {
	t.Helper()
	files := ArchiveFiles(platform)
	if platform == "windows" {
		entries := make([]zipEntry, len(files))
		for i, f := range files {
			entries[i] = zipEntry{f, 0o755, []byte(f)}
		}
		return zipOf(t, entries...)
	}
	entries := make([]tarEntry, len(files))
	for i, f := range files {
		entries[i] = regularEntry(f, []byte(f))
	}
	return tarGzOf(t, entries...)
}

func extractFor(platform string, archive []byte) ([]byte, []installedFile, error) {
	if platform == "windows" {
		return extractFromZip(platform, archive)
	}
	return extractFromTarGz(platform, archive)
}

func TestAnUpdateTakesEveryFileItsPlatformInstalls(t *testing.T) {
	for _, tgt := range SupportedTargets() {
		t.Run(tgt.Platform+"/"+tgt.Arch, func(t *testing.T) {
			binary, beside, err := extractFor(tgt.Platform, contractArchive(t, tgt.Platform))
			if err != nil {
				t.Fatal(err)
			}
			if want := ShippedPrograms(tgt.Platform)[0].Name; string(binary) != want {
				t.Fatalf("the binary taken is %q, want the archive's %q", binary, want)
			}
			var names []string
			for _, f := range beside {
				names = append(names, f.name)
				if string(f.data) != f.name {
					t.Errorf("%s was taken with another entry's bytes: %q", f.name, f.data)
				}
			}
			want := installedBeside(tgt.Platform)
			if !slices.Equal(names, want) {
				t.Fatalf("an update of a %s install takes %q beside aii, want every file the release installs there, in its order: %q", tgt.Platform, names, want)
			}
			// Every other shipped program is one of them, on every platform.
			for _, p := range ShippedPrograms(tgt.Platform)[1:] {
				if !slices.Contains(names, p.Name) {
					t.Errorf("%s ships %s (%s) and an update does not install it", tgt.Platform, p.Name, p.Command)
				}
			}
		})
	}
	if _, _, err := extractFromTarGz("plan9", contractArchive(t, "linux")); err == nil {
		t.Fatal("an archive was opened for a platform no release is made for")
	}
}

// What an update writes on Windows is what the installer wrote: both
// programs and both licence texts. On Unix it writes programs only — an
// archive is unpacked wherever its person chose, and a licence text or a
// menu entry dropped into a bin directory is not an install.
func TestAnUpdateWritesProgramsOnlyOutsideWindows(t *testing.T) {
	if got, want := installedBeside("windows"), []string{windowsLauncher, "LICENSE", "THIRD_PARTY_LICENSES"}; !slices.Equal(got, want) {
		t.Fatalf("a Windows update installs %q beside aii.exe, want what the installer writes there: %q", got, want)
	}
	for _, platform := range []string{"linux", "macos"} {
		for _, name := range installedBeside(platform) {
			if slices.Contains(ShippedDocuments(), name) || strings.Contains(name, "/") {
				t.Errorf("a %s update would write %s beside aii", platform, name)
			}
		}
	}
}

// Every file an update installs is taken only in aii's own shape. The
// shapes are the helper's (malformedHelpers), driven for each name of each
// archive format.
func TestEveryFileBesideAiiIsHeldToAiisShape(t *testing.T) {
	for _, name := range installedBeside("linux") {
		for shape, bad := range malformedHelpers(name) {
			t.Run("linux/"+name+"/"+shape, func(t *testing.T) {
				binary, _, err := extractFromTarGz("linux", tarGzOf(t, withAii(bad...)...))
				refused(t, binary, err, bad[0].hdr.Name)
			})
		}
	}
	real, license := zipEntry{"aii.exe", 0o755, []byte("new binary")}, zipEntry{"NOTICE", 0o644, []byte("a note")}
	for _, name := range installedBeside("windows") {
		other := []byte("another " + name)
		for shape, bad := range map[string][]zipEntry{
			"outside the archive": {{"../" + name, 0o755, other}},
			"in a subdirectory":   {{"bin/" + name, 0o755, other}},
			"a backslash path":    {{`bin\` + name, 0o755, other}},
			"a symlink":           {{name, fs.ModeSymlink | 0o777, []byte(`C:\Windows\System32\cmd.exe`)}},
			"a directory":         {{name + "/", fs.ModeDir | 0o755, nil}},
			"empty":               {{name, 0o755, nil}},
			"twice":               {{name, 0o755, other}, {name, 0o755, []byte("the other one")}},
		} {
			t.Run("windows/"+name+"/"+shape, func(t *testing.T) {
				binary, _, err := extractFromZip("windows", zipOf(t, append([]zipEntry{real, license}, bad...)...))
				refused(t, binary, err, bad[0].name)
			})
		}
	}
}

// Through the real apply, on the host's own archive: every file the
// release installs beside aii is there afterwards with the archive's
// bytes, executable, and nothing else of the archive was written.
func TestAnUpdateInstallsEveryFileBesideAii(t *testing.T) {
	rig := newHelperRig(t)
	platform := packagefmt.HostPlatform()
	in := rig.install(t, contractArchive(t, platform))
	// A launcher from the release before, not executable: the installed
	// mode must be the install's own.
	launcher := filepath.Join(in.dir, installedBeside(platform)[0])
	if err := os.WriteFile(launcher, []byte("the launcher first installed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := in.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertApplyFile(t, in.exe, hostProgram())
	for _, name := range installedBeside(platform) {
		path := filepath.Join(in.dir, name)
		assertApplyFile(t, path, name)
		if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() || !installedExecutable(fi) {
			t.Errorf("%s is %v (%v), want a regular executable file", name, fi, err)
		}
	}
	for _, f := range ArchiveFiles(platform) {
		if f == "aii" || slices.Contains(installedBeside(platform), f) {
			continue
		}
		if exists(filepath.Join(in.dir, filepath.FromSlash(f))) {
			t.Errorf("the update wrote %s beside aii; a %s update installs programs only", f, platform)
		}
	}
	if debris, _ := filepath.Glob(filepath.Join(in.dir, ".*.stage-*")); len(debris) != 0 {
		t.Errorf("the update left staging files behind: %v", debris)
	}
}

// An archive from before a release carried these files — every one up to
// 0.1.16 — updates as it always has and leaves what stands beside aii alone.
func TestAnArchiveFromBeforeTheLauncherLeavesItAlone(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	in := rig.install(t, tarGzOf(t, regularEntry("aii", []byte("new binary")), regularEntry("LICENSE", []byte("the license"))))
	for _, name := range installedBeside(packagefmt.HostPlatform()) {
		if err := os.WriteFile(filepath.Join(in.dir, name), []byte("already here: "+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := in.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertApplyFile(t, in.exe, "new binary")
	for _, name := range installedBeside(packagefmt.HostPlatform()) {
		assertApplyFile(t, filepath.Join(in.dir, name), "already here: "+name)
	}
}

// installBeside publishes over a file that is there and into a place that
// is empty, and a place it cannot publish into leaves no staging file.
func TestInstallBesidePublishesWholeOrNotAtAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aii-app")
	for _, body := range []string{"first", "second"} {
		if err := installBeside(path, []byte(body)); err != nil {
			t.Fatalf("install %q: %v", body, err)
		}
		assertApplyFile(t, path, body)
	}
	if runtime.GOOS == "windows" {
		return // a directory renames aside there; the refusal below is Unix's
	}
	occupied := filepath.Join(dir, "LICENSE")
	if err := os.MkdirAll(filepath.Join(occupied, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installBeside(occupied, []byte("the license")); err == nil {
		t.Fatal("a file was reported installed over a non-empty directory")
	}
	if debris, _ := filepath.Glob(filepath.Join(dir, ".LICENSE.stage-*")); len(debris) != 0 {
		t.Errorf("the failed install left %v behind", debris)
	}
	// A licence text is not installed executable.
	doc := filepath.Join(dir, "THIRD_PARTY_LICENSES")
	if err := installBeside(doc, []byte("the licences")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(doc); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("a licence text was installed %v (%v), want -rw-r--r--", fi.Mode(), err)
	}
}

// A Windows update leaves a displaced running launcher as .old, as it does
// the displaced aii.exe; the same boot that removes the one removes the
// other, on every platform's every boot.
func TestBootRemovesWhatAnUpdateDisplacedBesideAii(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "current")
	var names []string
	for _, p := range ShippedPrograms(packagefmt.HostPlatform())[1:] {
		names = append(names, p.Name)
	}
	for _, name := range names {
		writeFile(t, dir, name, "current")
		writeFile(t, dir, name+".old", "displaced by the last update")
	}
	// Not an update's: a person's file, and a licence text's namesake —
	// a licence text is written over in place and leaves no .old.
	writeFile(t, dir, "notes.old", "a person's own file")
	writeFile(t, dir, "LICENSE.old", "a person's own file")

	checkRollbackAt(dir, exePath)

	for _, name := range names {
		if fileExists(dir, name+".old") {
			t.Errorf("the displaced %s.old survived a boot", name)
		}
		assertApplyFile(t, filepath.Join(dir, name), "current")
	}
	for _, name := range []string{"notes.old", "LICENSE.old"} {
		if !fileExists(dir, name) {
			t.Errorf("a boot removed %s, which no update made", name)
		}
	}
}

func TestInstallBesideIntoAMissingDirectoryIsAnError(t *testing.T) {
	err := installBeside(filepath.Join(t.TempDir(), "gone", "aii-app"), []byte("x"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installing into a directory that is not there: %v", err)
	}
	if strings.HasPrefix(err.Error(), "published but not durable") {
		t.Errorf("nothing was published: %v", err)
	}
}
