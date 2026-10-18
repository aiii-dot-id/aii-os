package updates

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// blockingRelease serves the latest-release answer only after release
// is closed, and counts asset downloads.
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

// checkNowChecker is a checker as boot builds it, with the given SAFE and
// mobile answers, and armed as Run arms it once the version gate passes.
func checkNowChecker(t *testing.T, srv *httptest.Server, automatic bool, isSafe, isMobile func() bool) *Checker {
	t.Helper()
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "0.3.0" },
		func() bool { return automatic },
		nil, nil, t.TempDir(),
		isSafe, isMobile,
	)
	c.httpClient = redirectTo(srv)
	c.armed.Store(true)
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

// 1. One check runs and its result reaches the state the dashboard reads.
func TestCheckNowRunsOneCheckAndReportsIt(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })

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

// 3. Repeated requests cannot create overlapping runs: the second is
// refused with an explicit answer while the first is in flight, and a
// third is accepted once it finished.
func TestCheckNowRefusesAnOverlappingRun(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })

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

// 5. With automatic off — the default — an available update is reported
// and NOTHING is downloaded or installed.
func TestCheckNowDoesNotApplyWhenAutomaticIsOff(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, downloads := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })

	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	snap := c.State().Snapshot("0.3.0")
	if snap.AvailableVersion != "0.4.0" {
		t.Fatalf("the update must be reported: %+v", snap)
	}
	// An attempted Apply is visible two ways: a download, or — when Apply
	// refuses before downloading (no platform root here) — the error it
	// records. Inform-only records neither.
	if snap.InstalledVersion != "" || snap.NeedsRestart || snap.LastError != "" || atomic.LoadInt32(downloads) != 0 {
		t.Fatalf("automatic is off: nothing may be downloaded, installed or attempted (downloads=%d, %+v)", atomic.LoadInt32(downloads), snap)
	}
}

// 6. Mobile is inform-only whatever the setting says.
func TestCheckNowIsInformOnlyOnMobile(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, downloads := blockingRelease(t, release)
	c := checkNowChecker(t, srv, true, func() bool { return false }, func() bool { return true })

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

// Not armed (a build with no release version) and SAFE are answers, not
// runs: each is refused by name, and no goroutine starts.
func TestCheckNowRefusesWhenOffOrInSafe(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return true }, func() bool { return false })
	c.armed.Store(false) // as before Run passes the version gate
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrCheckingOff) {
		t.Fatalf("an unarmed checker must refuse as off, got %v", err)
	}
	c.armed.Store(true)
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrSafeMode) {
		t.Fatalf("SAFE must refuse by name, got %v", err)
	}
	if c.State().Snapshot("0.3.0").Checking {
		t.Fatal("a refusal must not leave the state saying checking")
	}
}

// The run is bounded by the caller's lifecycle: cancelling it ends the
// run, done fires, and the state records a failure rather than hanging.
func TestCheckNowEndsWithTheLifecycle(t *testing.T) {
	release := make(chan struct{}) // never released: the request would hang
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })

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

func TestCheckNowIsHandedToItsOwnerAndRefusedWhenStopping(t *testing.T) {
	release := make(chan struct{})
	close(release)
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })

	// The owner runs it, and the check is the work it was handed — not a
	// goroutine started beside it.
	var owned int
	var ranInsideOwner bool
	done := make(chan struct{})
	c.SetRunner(func(work func()) bool {
		owned++
		inside := false
		func() { inside = true; work() }()
		ranInsideOwner = inside
		return true
	})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if owned != 1 || !ranInsideOwner {
		t.Fatalf("the check was not run by its owner: handed over %d times, ran inside=%v", owned, ranInsideOwner)
	}
	if snap := c.State().Snapshot("0.3.0"); snap.AvailableVersion != "0.4.0" || snap.Checking {
		t.Fatalf("the owned check did not do the work: %+v", snap)
	}

	// An owner that is stopping: nothing begins, the caller is told, and the
	// state does not sit saying "checking" for a check nobody is running.
	c.SetRunner(func(func()) bool { return false })
	err := c.CheckNow(context.Background(), func() { t.Error("a refused check reported itself done") })
	if !errors.Is(err, ErrStopping) {
		t.Fatalf("a check asked for while stopping: %v", err)
	}
	if snap := c.State().Snapshot("0.3.0"); snap.Checking {
		t.Error("a check that never began left the state saying checking")
	}
	// And the next one, with an owner again, still runs: nothing is latched.
	c.SetRunner(func(work func()) bool { work(); return true })
	again := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(again) }); err != nil {
		t.Fatalf("after a refusal the next check must run: %v", err)
	}
	waitDone(t, again)
}

func TestApplyDoesNotSwapAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "aii")
	writeFile(t, dir, "aii", "old binary")
	writeFile(t, dir, ".boot_completed", "ok")

	const newVer = "9.9.9"
	archive := releaseArchive(t, []byte("new binary"))
	signer := newTestReleaseSigner(t)
	signer.provisionTrustDir(t, dir)
	sig := signer.signReleasePayload(t, releaseArchivePayload{
		ArchiveHash: sha256hex(archive),
		Version:     newVer,
		Platform:    packagefmt.HostPlatform(),
		Arch:        hostArchForTest(),
		SourceRev:   "cancelapply0",
	})
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
	c := newTestChecker(signer.env, dir, &githubRelease{
		TagName: "v" + newVer,
		Assets: []githubAsset{
			{Name: assetName(newVer), BrowserDownloadURL: srv.URL + "/archive"},
			{Name: assetName(newVer) + ".platform.sig", BrowserDownloadURL: srv.URL + "/sig"},
		},
	})
	c.state.setAvailable(newVer)

	// THE REASON TO STOP ARRIVES WHILE THE RELEASE IS BEING VERIFIED, which
	// is the case this is about. Cancelling earlier only proves that a
	// download uses its context — which it does, and which is not this rule:
	// the first shape of this test cancelled before the first request and
	// passed with the guard removed. Nothing between the last fetch and the
	// swap looks at the context, so the seam is the only way in.
	ctx, cancel := context.WithCancel(context.Background())
	c.afterVerified = cancel
	err := c.applyTo(ctx, exePath)
	if err == nil {
		t.Fatal("A BINARY WAS SWAPPED AFTER THE REASON TO STOP ARRIVED")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the refusal does not name the cancellation: %v", err)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "old binary" {
		t.Errorf("the running program was replaced by a cancelled apply: %q", got)
	}

	// The same rig, verified and NOT cancelled, DOES install — so the case above is the
	// cancellation and not a rig that could never have got there.
	if err := c.applyTo(context.Background(), exePath); err != nil {
		t.Fatalf("the same apply without a cancellation must install: %v", err)
	}
	if got, _ := os.ReadFile(exePath); string(got) != "new binary" {
		t.Fatalf("after the apply the binary is %q", got)
	}
}

// ONE IN-FLIGHT FLAG, ADMITTED ONCE (T45). The flag the dashboard renders
// is the flag that admits: of many simultaneous requests exactly one
// starts a run, every other is told it is already checking, and while
// that run is in flight the state says so.
func TestCheckNowAdmitsExactlyOneOfManyAtOnce(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })
	const callers = 32
	errs := make(chan error, callers)
	done := make(chan struct{})
	start := make(chan struct{})
	for range callers {
		go func() {
			<-start
			errs <- c.CheckNow(context.Background(), func() { close(done) })
		}()
	}
	close(start)
	admitted := 0
	for range callers {
		switch err := <-errs; {
		case err == nil:
			admitted++
		case !errors.Is(err, ErrAlreadyChecking):
			t.Fatalf("a concurrent request was refused for another reason: %v", err)
		}
	}
	if admitted != 1 {
		t.Fatalf("%d runs admitted at once, want exactly 1", admitted)
	}
	if !c.State().Snapshot("0.3.0").Checking {
		t.Fatal("the admitted run is not visible as checking")
	}
	close(release)
	waitDone(t, done)
	if c.State().Snapshot("0.3.0").Checking {
		t.Fatal("the state still says checking after the run ended")
	}
}

// A CHECK-NOW CANNOT BEGIN BEFORE RUN ARMS THE CHECKER. Boot builds the
// checker before the dashboard and starts Run only after the boot-health
// marker is written; the marker retires this identity's update state, and
// nothing may begin a replacement meanwhile (boot.go). So a checker with a
// release version still refuses as off until Run passes the version gate.
func TestCheckNowRefusesUntilRunArms(t *testing.T) {
	release := make(chan struct{})
	srv, _ := blockingRelease(t, release)
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "0.3.0" },
		func() bool { return false },
		nil, nil, t.TempDir(),
		func() bool { return false }, func() bool { return false },
	)
	c.httpClient = redirectTo(srv)
	if c.Armed() {
		t.Fatal("a checker is armed before Run")
	}
	if err := c.CheckNow(context.Background(), nil); !errors.Is(err, ErrCheckingOff) {
		t.Fatalf("a check-now before Run must refuse as off, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() { defer close(ran); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-ran })
	deadline := time.Now().Add(10 * time.Second)
	for !c.Armed() {
		if time.Now().After(deadline) {
			t.Fatal("Run never armed a checker with a release version")
		}
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan struct{})
	if err := c.CheckNow(context.Background(), func() { close(done) }); err != nil {
		t.Fatalf("once Run armed it, a check-now must start, got %v", err)
	}
	close(release)
	waitDone(t, done)
}
