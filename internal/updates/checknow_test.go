package updates

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
// .
// .

// .
// .
func blockingRelease(t *testing.T, release chan struct{}) (*httptest.Server, *int32) {
	t.Helper()
	var downloads int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/latest") {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"tag_name":"v0.4.0","assets":[{"name":"aii-os_0.4.0_linux_amd64.tar.gz","browser_download_url":"http://` + r.Host + `/asset"}]}`))
			return
		}
		atomic.AddInt32(&downloads, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &downloads
}

func checkNowChecker(t *testing.T, srv *httptest.Server, automatic bool) *Checker {
	t.Helper()
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "0.3.0" },
		func() bool { return automatic },
		nil, nil, t.TempDir(),
	)
	c.httpClient = redirectTo(srv)
	return c
}

func waitDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the check-now run never reported done")
	}
}

// .
func TestCheckNowRunsOneCheckAndReportsIt(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false)
	c.arm(func() bool { return false }, func() bool { return false })

	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" || snap.Checking || snap.LastCheck.IsZero() {
		t.Fatalf("after the run the state must carry the result and no longer say checking: %+v", snap)
	}
}

// .
// .
// .
func TestCheckNowRefusesAnOverlappingRun(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false)
	c.arm(func() bool { return false }, func() bool { return false })

	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	if !c.State().Snapshot("0.3.0").Checking {
		t.Fatal("a run in flight must be visible as checking")
	}
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrAlreadyChecking) {
		t.Fatalf("a second request during a run must be refused as already checking, got %v", err)
	}
	close(release)
	waitDone(t, done)
	done2 := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done2) }); err != nil {
		t.Fatalf("once the run finished a new one must be accepted, got %v", err)
	}
	waitDone(t, done2)
}

// .
// .
func TestCheckNowDoesNotApplyWhenAutomaticIsOff(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, downloads := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false)
	c.arm(func() bool { return false }, func() bool { return false })

	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" {
		t.Fatalf("the update must be reported: %+v", snap)
	}
	// .
	// .
	// .
	if snap.InstalledVersion != "" || snap.NeedsRestart || snap.LastError != "" || atomic.LoadInt32(downloads) != 0 {
		t.Fatalf("automatic is off: nothing may be downloaded, installed or attempted (downloads=%d, %+v)", atomic.LoadInt32(downloads), snap)
	}
}

// .
func TestCheckNowIsInformOnlyOnMobile(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, downloads := blockingRelease(t, release)
	c := checkNowChecker(t, srv, true)
	c.arm(func() bool { return false }, func() bool { return true })

	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	snap := c.State().Snapshot("0.3.0")
	if n := atomic.LoadInt32(downloads); n != 0 || snap.InstalledVersion != "" || snap.LastError != "" {
		t.Fatalf("mobile must only report — no download, no install, no attempted apply: downloads=%d %+v", n, snap)
	}
}

// .
// .
func TestCheckNowRefusesWhenOffOrInSafe(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false)
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrCheckingOff) {
		t.Fatalf("an unarmed checker must refuse as off, got %v", err)
	}
	c.arm(func() bool { return true }, func() bool { return false })
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrSafeMode) {
		t.Fatalf("SAFE must refuse by name, got %v", err)
	}
	if c.State().Snapshot("0.3.0").Checking {
		t.Fatal("a refusal must not leave the state saying checking")
	}
}

// .
// .
func TestCheckNowEndsWithTheLifecycle(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false)
	c.arm(func() bool { return false }, func() bool { return false })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	if err := c.CheckNow(ctx, func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitDone(t, done)
	snap := c.State().Snapshot("0.3.0")
	if snap.Checking || snap.LastError == "" {
		t.Fatalf("a cancelled run must finish, clear checking, and name its failure: %+v", snap)
	}
}
