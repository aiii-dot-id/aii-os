package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// Check, fetchLatestRelease and canStageBeside were all at 0%: the
// entire orchestration of the self-updater was untested, which is the
// code-level shape of journey 9 being unproven. Apply's download-verify-
// swap still needs a real signed release and is deliberately not
// simulated here — a fake swap would prove nothing about the one step
// that matters.

// redirectTo makes every request the checker sends land on srv instead
// of api.github.com, so the real fetch path runs against a fixture.
func redirectTo(srv *httptest.Server) *http.Client {
	return &http.Client{Transport: rewriteHost{base: srv.URL}}
}

type rewriteHost struct{ base string }

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.base + req.URL.Path)
	if err != nil {
		return nil, err
	}
	req = req.Clone(req.Context())
	req.URL = u
	req.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func checkerAgainst(t *testing.T, srv *httptest.Server, version string) *Checker {
	t.Helper()
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return version },
		func() bool { return true },
		nil, // repo unset — resolveRepo falls back to DefaultRepo
		nil,
		t.TempDir(),
		func() bool { return false }, func() bool { return false },
	)
	c.httpClient = redirectTo(srv)
	return c
}

func releaseServer(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases/latest") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A NEWER RELEASE IS OFFERED, and the state carries it for the
// dashboard to show.
func TestCheckOffersANewerRelease(t *testing.T) {
	srv := releaseServer(t, http.StatusOK, map[string]any{"tag_name": "v0.4.0"})
	c := checkerAgainst(t, srv, "0.3.0")

	got := c.Check(context.Background())
	if got != "0.4.0" {
		t.Fatalf("Check returned %q, want 0.4.0", got)
	}
	if av := c.State().Available(); av != "0.4.0" {
		t.Fatalf("state carries %q — the dashboard would show nothing", av)
	}
}

// CURRENT OR AHEAD IS NOT AN UPDATE, and a stale "available" from an
// earlier check must be cleared rather than left standing.
func TestCheckClearsAStaleOfferWhenCurrent(t *testing.T) {
	srv := releaseServer(t, http.StatusOK, map[string]any{"tag_name": "v0.3.0"})
	c := checkerAgainst(t, srv, "0.3.0")
	c.State().setAvailable("0.9.9") // left over from a previous check

	if got := c.Check(context.Background()); got != "" {
		t.Fatalf("Check offered %q while running the same version", got)
	}
	if av := c.State().Available(); av != "" {
		t.Fatalf("a stale offer survived: %q — the dashboard would keep advertising an update that is not one", av)
	}
}

// THE 404 A LIVE IDENTITY SAW 35 TIMES. A repository with no releases yet must
// record an error rather than a phantom update.
func TestCheckRecordsTheErrorWhenThereIsNoRelease(t *testing.T) {
	srv := releaseServer(t, http.StatusNotFound, map[string]any{"message": "Not Found"})
	c := checkerAgainst(t, srv, "0.3.0")

	if got := c.Check(context.Background()); got != "" {
		t.Fatalf("Check offered %q from a 404", got)
	}
	if e := c.State().Snapshot("0.3.0").LastError; !strings.Contains(e, "404") {
		t.Fatalf("last error = %q — an operator cannot tell why nothing happened", e)
	}
}

// A MALFORMED VERSION IS NOT A COMPARISON. Refusing loudly beats a
// hand-rolled parse that confidently downgrades.
func TestCheckRefusesAnUncomparableRelease(t *testing.T) {
	srv := releaseServer(t, http.StatusOK, map[string]any{"tag_name": "not-a-version"})
	c := checkerAgainst(t, srv, "0.3.0")

	if got := c.Check(context.Background()); got != "" {
		t.Fatalf("Check offered %q from an unparseable tag", got)
	}
	if e := c.State().Snapshot("0.3.0").LastError; !strings.Contains(e, "invalid semantic version") {
		t.Fatalf("last error = %q — it must name the real problem", e)
	}
}

// A WRITABLE DIRECTORY CAN BE STAGED BESIDE; a read-only one cannot,
// and says so in terms an operator can act on. This is what keeps a
// package-managed install from downloading a release every hour and
// failing at the last step.
func TestCanStageBesideProbesTheRealDirectory(t *testing.T) {
	if ranUnprivileged(t) {
		return // to root a read-only directory is still writable: it ran as an unprivileged user
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, why := canStageBesideAt(exe); !ok {
		t.Fatalf("a writable directory refused staging: %s", why)
	}

	if runtime.GOOS == "windows" {
		// A directory's writability on Windows is a DACL, not a mode
		// bit: os.Mkdir(0o500) and os.Chmod both move only the
		// read-only ATTRIBUTE, which does not stop file creation. The
		// precondition this test needs therefore never exists, and the
		// probe correctly reports the directory as writable — it
		// attempts a real write rather than trusting a mode. Expressing
		// this properly needs a deny-write ACE, which risks leaving an
		// undeletable TempDir behind.
		t.Skip("a mode bit cannot make a directory unwritable on Windows")
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	ok, why := canStageBesideAt(filepath.Join(locked, "aii"))
	if ok {
		t.Fatal("a directory this user cannot write reported that it could be staged into")
	}
	if !strings.Contains(why, "package manager") {
		t.Fatalf("the refusal does not tell the operator what to do instead: %q", why)
	}
}
