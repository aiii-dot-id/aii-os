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

// .
// .
// .
// .
// .
// .
// .
// .
func TestASecondApplyKeepsTheRollbackBackup(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	const newVer = "9.9.9"
	archive := releaseArchive(t, []byte("new binary"))
	signer := newTestReleaseSigner(t)
	// .
	// .
	// .
	// .
	signer.provisionTrustDir(t, dir)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive),
		Version:     newVer,
		Platform:    packagefmt.HostPlatform(),
		Arch:        hostArchForTest(),
		SourceRev:   "reapplytest0",
	})
	// .
	// .
	// .
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

	// .
	// .
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

	// .
	// .
	// .
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

	// .
	WriteBootMarker(dir)
	c.state.SetAvailable(newVer)
	if err := c.applyTo(context.Background(), exePath); err != nil {
		t.Fatalf("after a healthy boot retired the update, apply must work again: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestACorruptTombstoneStillProtectsTheBackup(t *testing.T) {
	for _, corrupt := range []string{"", "{ not json", "[]"} {
		t.Run("tombstone="+corrupt, func(t *testing.T) {
			dir := t.TempDir()
			exePath := filepath.Join(dir, "aii")
			writeFile(t, dir, "aii", "new binary")
			writeFile(t, dir, "aii.previous", "old binary")
			writeFile(t, dir, ".update_pending", corrupt)

			const newVer = "9.9.9"
			archive := releaseArchive(t, []byte("newer binary"))
			signer := newTestReleaseSigner(t)
			sig := signer.signReleasePayload(t, releaseArchivePayload{
				ArchiveHash: sha256hex(archive),
				Version:     newVer,
				Platform:    packagefmt.HostPlatform(),
				Arch:        hostArchForTest(),
				SourceRev:   "corrupttomb0",
			})
			// .
			// .
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

// .

func newTestChecker(root *sigenvelope.PublicKeyEnvelope, dataDir string, release *githubRelease) *Checker {
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return root },
		func() string { return "1.0.0" },
		func() bool { return true },
		nil,
		nil,
		dataDir,
	)
	c.cachedRelease = release
	// .
	// .
	// .
	c.hostAllowlist = func(string) error { return nil }
	return c
}

// .
// .
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

// .
// .
// .
// .
// .
func hostArchForTest() string { return runtime.GOARCH }
