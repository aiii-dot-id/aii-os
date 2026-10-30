package updates

import (
	"os"
	"path/filepath"
	"testing"
)

// earlierAppInstall lays out what 0.1.23's app update leaves (its
// installBundle, bundle_darwin.go:227 at c17bdeb4): the new app installed,
// the one it replaced kept beside it as previousBundleName, a record that
// names no release, and the boot marker of the identity's last healthy
// start, which that install does not clear. version, when given, is the
// release a record of this program's own names.
func earlierAppInstall(t *testing.T, version string) (tgt target, exe, dataDir string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(dir, "AII OS.app", "Contents", "MacOS", "aii")
	kept := filepath.Join(dir, previousBundleName, "Contents", "MacOS")
	for _, d := range []string{filepath.Dir(exe), kept} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Dir(exe), "aii", "this version")
	writeFile(t, kept, "aii", "0.1.23")
	dataDir = t.TempDir()
	writeFile(t, dataDir, markerFile, "ok")
	tgt = targetForTest(t, exe)
	if err := writeRecord(tgt.record, updatePending{BackupSHA256: sha256hex([]byte("0.1.23")), NewSHA256: sha256hex([]byte("this version")), Version: version, Owner: ownerForTest(t, dataDir)}); err != nil {
		t.Fatal(err)
	}
	return tgt, exe, dataDir
}

func TestTheCopyAnEarlierReleasesAppUpdateKeptStays(t *testing.T) {
	t.Run("an update this version did not apply", func(t *testing.T) {
		tgt, exe, dataDir := earlierAppInstall(t, "")
		if got := previousKeptAt(exe); got != "" {
			t.Fatalf("a copy its record still claims is said to be kept for the operator: %q", got)
		}
		if rolled := checkRollbackAt(dataDir, exe); rolled != "" {
			t.Fatalf("the first start rolled back an update it cannot count: %q", rolled)
		}
		if !exists(tgt.backup) {
			t.Fatal("the first start removed the copy of the app the update replaced")
		}
		if exists(tgt.record) {
			t.Fatal("the record of an update that finished its boots was kept")
		}
		writeBootMarkerAt(dataDir, exe)
		checkRollbackAt(dataDir, exe)
		if got := previousKeptAt(exe); got != tgt.backup {
			t.Fatalf("after a healthy start the copy is said at %q, want %q", got, tgt.backup)
		}
		if got, err := os.ReadFile(filepath.Join(tgt.backup, "Contents", "MacOS", "aii")); err != nil || string(got) != "0.1.23" {
			t.Fatalf("the copy kept is not the app the update replaced: %q %v", got, err)
		}
	})
	t.Run("an update this version applied", func(t *testing.T) {
		tgt, exe, dataDir := earlierAppInstall(t, "9.9.9")
		checkRollbackAt(dataDir, exe)
		if exists(tgt.backup) || exists(tgt.record) {
			t.Fatal("a settled update of this version's kept its copy or its record")
		}
		if got := previousKeptAt(exe); got != "" {
			t.Fatalf("no copy is kept, and one is said: %q", got)
		}
	})
	t.Run("a program that is not an app", func(t *testing.T) {
		exe := filepath.Join(t.TempDir(), "aii")
		writeFile(t, filepath.Dir(exe), "aii", "binary")
		writeFile(t, filepath.Dir(exe), ".aii.previous", "binary before")
		if got := previousKeptAt(exe); got != "" {
			t.Fatalf("a bare binary's backup is said as an app kept: %q", got)
		}
	})
}
