//go:build windows

package updates

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// TestMain's stand-in for a launcher that is running while its update is
// applied: this test binary, copied to the launcher's name, waits here.
func init() {
	if os.Getenv("AII_TEST_RUNNING_LAUNCHER") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// THE WINDOWS UPDATE, THROUGH THE REAL APPLY. The installer writes aii.exe,
// the launcher and both licence texts into the program's directory; an
// update writes all four from its archive — and the launcher while it is
// RUNNING, which Windows lets a program rename aside and never write over.
// It waits up to a minute for the dashboard at every sign-in, so an update
// applied then meets exactly that.
func TestAWindowsUpdateInstallsWhatTheInstallerInstalls(t *testing.T) {
	signer := newTestReleaseSigner(t)
	dir := t.TempDir()
	signer.provisionTrustDir(t, dir)
	exe := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, markerFile, "ok")

	// The launcher first installed, running.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, windowsLauncher)
	if err := os.WriteFile(launcher, image, 0o755); err != nil {
		t.Fatal(err)
	}
	running := exec.Command(launcher)
	running.Env = append(os.Environ(), "AII_TEST_RUNNING_LAUNCHER=1")
	if err := running.Start(); err != nil {
		t.Fatalf("start the launcher that is to be replaced while it runs: %v", err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			_ = running.Process.Kill()
			_, _ = running.Process.Wait()
			stopped = true
		}
	}
	t.Cleanup(stop)

	archive := contractArchive(t, "windows")
	const ver = "9.9.9"
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "besidewindows",
	})
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
	c := newTestChecker(signer.env, dir, &githubRelease{
		TagName: "v" + ver,
		Assets: []githubAsset{
			{Name: assetName(ver), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(ver) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	})
	c.state.setAvailable(ver)

	if err := c.applyTo(context.Background(), exe); err != nil {
		t.Fatalf("apply while the launcher runs: %v", err)
	}
	assertApplyFile(t, exe, "aii.exe")
	for _, name := range installedBeside("windows") {
		assertApplyFile(t, filepath.Join(dir, name), name)
	}
	// The image that was running is aside, still what it was.
	if got, err := os.ReadFile(launcher + ".old"); err != nil || len(got) != len(image) {
		t.Fatalf("the running launcher was not renamed aside whole: %d bytes (%v), want %d", len(got), err, len(image))
	}

	// Once that process is gone, a boot removes what it left. Windows
	// lets go of an ended process's image a moment after the process is
	// reaped, so the boot is asked until it has, within a bound: any later
	// boot is the cleanup, not this one in particular. The update's own
	// first boot finishes first: boots that only began would be failed
	// ones, and the second of them rolls the launcher back with aii.
	stop()
	checkRollbackAt(dir, exe)
	writeBootMarkerAt(dir, exe)
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		checkRollbackAt(dir, exe)
		if !exists(launcher + ".old") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the displaced launcher survived every boot for 15 s after its process ended")
		}
	}
	assertApplyFile(t, launcher, windowsLauncher)
}

// A ROLLBACK WHILE THE UPDATE'S LAUNCHER RUNS. An update's first start is
// its launcher's: the sign-in starts it, it starts aii.exe and waits up to
// a minute for the dashboard. A start that never finishes is rolled back
// inside that minute, so the launcher the rollback puts back goes where the
// update's own is running: renamed aside, never written over, as an update
// installs one.
func TestAWindowsRollbackRestoresTheLauncherWhileTheUpdatesRuns(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(self) // the launcher first installed
	if err != nil {
		t.Fatal(err)
	}
	// The update's launcher: another image that runs as the first does
	// (what follows an image's end in its file is not loaded).
	updated := append(slices.Clone(first), "the update's launcher"...)
	var entries []zipEntry
	for _, f := range ArchiveFiles("windows") {
		body := []byte(f)
		if f == windowsLauncher {
			body = updated
		}
		entries = append(entries, zipEntry{f, 0o755, body})
	}
	in := newHelperRig(t).install(t, zipOf(t, entries...))
	launcher := filepath.Join(in.dir, windowsLauncher)
	if err := os.WriteFile(launcher, first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := in.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	running := exec.Command(launcher)
	running.Env = append(os.Environ(), "AII_TEST_RUNNING_LAUNCHER=1")
	if err := running.Start(); err != nil {
		t.Fatalf("start the update's launcher: %v", err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			_ = running.Process.Kill()
			_, _ = running.Process.Wait()
			stopped = true
		}
	}
	t.Cleanup(stop)

	if failedBoots(in) == "" {
		t.Fatal("two boots of the update that never finished did not roll it back")
	}
	assertApplyFile(t, in.exe, "old binary")
	if got, err := os.ReadFile(launcher); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("the rollback did not put back the launcher first installed while the update's ran: %d bytes (%v), want the %d first installed", len(got), err, len(first))
	}
	// The image that was running is aside, still what it was.
	if got, err := os.ReadFile(launcher + ".old"); err != nil || !bytes.Equal(got, updated) {
		t.Fatalf("the update's running launcher was not renamed aside whole: %d bytes (%v), want %d", len(got), err, len(updated))
	}
	if tgt := targetForTest(t, in.exe); exists(tgt.backup) || exists(tgt.record) || exists(besideBackup(tgt, windowsLauncher)) {
		t.Error("the rollback is done and its backup, its record or what it kept of the launcher is still there")
	}
	// And once that process is gone, a boot removes what it left.
	stop()
	for deadline := time.Now().Add(15 * time.Second); exists(launcher + ".old"); time.Sleep(100 * time.Millisecond) {
		checkRollbackAt(in.dir, in.exe)
		if time.Now().After(deadline) {
			t.Fatal("the displaced launcher survived every boot for 15 s after its process ended")
		}
	}
	if got, err := os.ReadFile(launcher); err != nil || !bytes.Equal(got, first) {
		t.Fatalf("a later boot changed the restored launcher: %d bytes (%v)", len(got), err)
	}
}
