package updates

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// THE SEQUENCE AN UNRESTARTED HOST RUNS: swap, then the next hourly
// tick. Check compares the latest release against the RUNNING version,
// which a swap does not change, so "available" re-arms every hour until
// the operator restarts. Apply had no guard, so the second swap read
// the exe path — by then the NEW binary — and wrote it over
// aii.previous, the only copy of the image that ever booted. The
// tombstone then recorded BackupSHA256 == NewSHA256, and a failed boot
// would have "rolled back" to a copy of the binary that failed.
func TestASecondApplyKeepsTheRollbackBackup(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok") // the old binary booted healthy

	const newVer = "9.9.9"
	archive := releaseArchive(t, []byte("new binary"))
	signer := newTestReleaseSigner(t)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive),
		Version:     newVer,
		Platform:    packagefmt.HostPlatform(),
		Arch:        packagefmt.HostArch(),
		SourceRev:   "reapplytest0",
	})
	// Every fetch the release costs, counted: the guard has to refuse
	// BEFORE the download, not after — the hourly tick was re-pulling the
	// whole archive on top of destroying the backup.
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/archive":
			w.Write(archive)
		case "/sig":
			w.Write(sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	release := &githubRelease{
		TagName: "v" + newVer,
		Assets: []githubAsset{
			{Name: assetName(newVer), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(newVer) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	}
	c := newTestChecker(signer.env, dir, release)

	c.state.SetAvailable(newVer)
	if err := c.applyTo(context.Background(), exePath); err != nil {
		t.Fatalf("the first apply must install: %v", err)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("after the first apply the binary is %q", got)
	}
	prevPath := filepath.Join(dir, "aii.previous")
	if got, _ := os.ReadFile(prevPath); string(got) != "old binary" {
		t.Fatalf("the first apply did not back up the running binary: %q", got)
	}
	installed := hits.Load()
	if installed == 0 {
		t.Fatal("the first apply must have fetched the release — the counter is wired wrong")
	}

	// The next hourly tick, still un-restarted: Check re-arms the same
	// version because the running version has not changed.
	c.state.SetAvailable(newVer)
	if err := c.applyTo(context.Background(), exePath); !errors.Is(err, ErrUpdatePending) {
		t.Fatalf("a second apply over a staged swap must refuse with ErrUpdatePending, got %v", err)
	}
	if got, _ := os.ReadFile(prevPath); string(got) != "old binary" {
		t.Fatalf("THE ROLLBACK BACKUP WAS DESTROYED: aii.previous holds %q, want the original binary", got)
	}
	pend, ok := readPending(dir)
	if !ok {
		t.Fatal("the refusal must leave the tombstone in place")
	}
	if pend.BackupSHA256 != sha256hex([]byte("old binary")) {
		t.Fatalf("the tombstone no longer binds the original backup: backup_sha256 %.12s", pend.BackupSHA256)
	}
	if got := hits.Load(); got != installed {
		t.Errorf("the refused apply still fetched the release: %d requests, want the %d the install cost", got, installed)
	}

	// A Checker that never saw the swap in memory must refuse too: the
	// evidence is the tombstone on disk, which outlives a process, and a
	// needs-restart flag alone would let this re-entry through.
	fresh := newTestChecker(signer.env, dir, release)
	fresh.state.SetAvailable(newVer)
	if err := fresh.applyTo(context.Background(), exePath); !errors.Is(err, ErrUpdatePending) {
		t.Fatalf("an apply with no in-memory swap state must still refuse, got %v", err)
	}
	if got, _ := os.ReadFile(prevPath); string(got) != "old binary" {
		t.Fatalf("THE ROLLBACK BACKUP WAS DESTROYED by a re-entry: aii.previous holds %q", got)
	}
	if got := hits.Load(); got != installed {
		t.Errorf("the re-entry still fetched the release: %d requests, want the %d the install cost", got, installed)
	}

	// And the restart that retires the tombstone re-opens the path.
	WriteBootMarker(dir)
	c.state.SetAvailable(newVer)
	if err := c.applyTo(context.Background(), exePath); err != nil {
		t.Fatalf("after a healthy boot retired the update, apply must work again: %v", err)
	}
}

// A TRUNCATED TOMBSTONE IS NOT AN ABSENT SWAP. readPending maps corrupt
// bytes to "absent" — and an empty file is exactly what a power loss or
// a full disk leaves behind — so a guard keyed on the tombstone alone
// went quiet while aii.previous was still the only image known to boot,
// and the next hourly tick wrote the update over it. The boot path had
// the opposite reading of the same file: checkRollbackAt adopts a
// corrupt tombstone from the backup rather than ignoring it, so the two
// readers disagreed about the state that matters most.
func TestACorruptTombstoneStillProtectsTheBackup(t *testing.T) {
	for _, corrupt := range []string{"", "{ not json", "[]"} {
		t.Run("tombstone="+corrupt, func(t *testing.T) {
			dir := t.TempDir()
			exePath := filepath.Join(dir, "aii")
			writeFile(t, dir, "aii", "new binary")          // the swap already happened
			writeFile(t, dir, "aii.previous", "old binary") // and this is the only image known to boot
			writeFile(t, dir, ".update_pending", corrupt)

			const newVer = "9.9.9"
			archive := releaseArchive(t, []byte("newer binary"))
			signer := newTestReleaseSigner(t)
			sig := signer.signReleasePayload(t, releaseArchivePayload{
				ArchiveHash: sha256hex(archive),
				Version:     newVer,
				Platform:    packagefmt.HostPlatform(),
				Arch:        packagefmt.HostArch(),
				SourceRev:   "corrupttomb0",
			})
			// A WORKING release, so a guard that fails to fire does real
			// damage instead of erroring for some unrelated reason.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/archive":
					w.Write(archive)
				case "/sig":
					w.Write(sig)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			release := &githubRelease{
				TagName: "v" + newVer,
				Assets: []githubAsset{
					{Name: assetName(newVer), BrowserDownloadURL: srv.URL + "/archive"},
					{Name: assetName(newVer) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
				},
			}
			c := newTestChecker(signer.env, dir, release)
			c.state.SetAvailable(newVer)

			if err := c.applyTo(context.Background(), exePath); !errors.Is(err, ErrUpdatePending) {
				t.Fatalf("a swap staged behind a corrupt tombstone must still refuse, got %v", err)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "aii.previous")); string(got) != "old binary" {
				t.Fatalf("THE ROLLBACK BACKUP WAS DESTROYED: aii.previous holds %q — a failed boot would restore the binary that failed", got)
			}
		})
	}
}

// --- test helpers ---

func newTestChecker(root *sigenvelope.PublicKeyEnvelope, dataDir string, release *githubRelease) *Checker {
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return root },
		func() string { return "1.0.0" },
		func() bool { return true },
		nil,
		dataDir,
	)
	c.cachedRelease = release
	return c
}

// releaseArchive builds the archive shape this host's extractBinary
// expects, carrying the given binary bytes.
func releaseArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("aii.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(binary); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{Name: "aii", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
