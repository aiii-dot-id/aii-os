package updates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// An hourly tick and CheckNow share a Checker, but not check-now's inFlight
// flag. Pause the first Apply after verification, before it writes any pending
// evidence. The second must refuse without downloading or replacing anything.
// Without admission ownership it installs first, then the paused call replaces
// the only old backup with the new binary. No scheduler timing is involved.
func TestOverlappingApplyPreservesTheOriginalBackup(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		name := "installed"
		if cancelFirst {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "aii")
			writeFile(t, dir, "aii", "old binary")
			writeFile(t, dir, markerFile, "ok")
			c, hits := applyOwnerFixture(t, dir)
			tgt := targetForTest(t, exe)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var steps atomic.Int32
			c.afterVerified = func() {
				if steps.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
			}
			var firstErr error
			go func() {
				defer close(done)
				firstErr = c.applyTo(ctx, exe)
			}()
			t.Cleanup(func() {
				cancel()
				<-done // join before removing fixtures
			})
			select {
			case <-entered:
			case <-done:
				t.Fatalf("first apply never reached verification: %v", firstErr)
			case <-ctx.Done():
				t.Fatal("first apply never reached the step")
			}
			before := hits.Load()
			// A refusal must not release somebody else's admission either.
			for duplicate := 1; duplicate <= 2; duplicate++ {
				if err := c.applyTo(ctx, exe); !errors.Is(err, ErrAlreadyApplying) {
					t.Errorf("overlapping apply %d must refuse as already applying, got %v", duplicate, err)
				}
			}
			if got := hits.Load(); got != before {
				t.Errorf("duplicate downloaded assets: requests %d -> %d", before, got)
			}
			assertApplyFile(t, exe, "old binary")
			if exists(tgt.backup) || exists(tgt.record) {
				t.Error("duplicate touched rollback state before the admitted swap")
			}
			if cancelFirst {
				cancel()
			}
			close(release)
			<-done
			if cancelFirst {
				if !errors.Is(firstErr, context.Canceled) {
					t.Errorf("cancelled owner returned %v", firstErr)
				}
				assertApplyFile(t, exe, "old binary")
				// A cancelled attempt must not strand the checker or require a restart.
				if err := c.applyTo(t.Context(), exe); err != nil {
					t.Fatalf("retry after cancellation: %v", err)
				}
			} else if firstErr != nil {
				t.Fatalf("admitted apply: %v", firstErr)
			}
			assertApplyFile(t, exe, "new binary")
			assertApplyFile(t, tgt.backup, "old binary")
			assertRecord(t, tgt, ownerForTest(t, dir), 0, "old binary", "new binary")
			if err := c.applyTo(t.Context(), exe); !errors.Is(err, ErrUpdatePending) {
				t.Errorf("finished owner must yield to the durable pending guard: %v", err)
			}
		})
	}
}

func assertApplyFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = %q (%v), want %q", filepath.Base(path), got, err, want)
	}
}

func TestFailedApplyReleasesAdmissionForRetry(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	c, _ := applyOwnerFixture(t, dir)
	refusal := errors.New("test release download refused")
	allow := c.hostAllowlist
	c.hostAllowlist = func(string) error { return refusal }
	if err := c.applyTo(t.Context(), exe); !errors.Is(err, refusal) {
		t.Fatalf("expected the injected download refusal, got %v", err)
	}
	assertApplyFile(t, exe, "old binary")
	c.hostAllowlist = allow
	if err := c.applyTo(t.Context(), exe); err != nil {
		t.Fatalf("retry after a failed download: %v", err)
	}
	assertApplyFile(t, exe, "new binary")
	assertApplyFile(t, targetForTest(t, exe).backup, "old binary")
}

func TestTickDoesNotReportAnAdmittedApplyAsFailed(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return false })
	// Isolate tick's classification. Its nil signing root also prevents
	// installation if a mutation removes admission; never swap the test binary.
	c.applying.Store(true)
	c.tick(t.Context())
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" || snap.LastCheck.IsZero() || snap.LastError != "" || snap.NeedsRestart {
		t.Fatalf("a duplicate trigger is neither failed nor installed: %+v", snap)
	}
	// A real refusal must still surface once no competing attempt owns Apply.
	c.applying.Store(false)
	c.tick(t.Context())
	if snap := c.State().Snapshot("0.3.0"); snap.LastError == "" || snap.NeedsRestart {
		t.Fatalf("a real apply error must remain visible: %+v", snap)
	}
}

func applyOwnerFixture(t *testing.T, dir string) (*Checker, *atomic.Int64) {
	t.Helper()
	const ver = "9.9.9"
	archive := releaseArchive(t, []byte("new binary"))
	signer := newTestReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive), Version: ver,
		Platform: packagefmt.HostPlatform(), Arch: hostArchForTest(), SourceRev: "applyowner0",
	})
	hits := new(atomic.Int64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
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
	return c, hits
}
