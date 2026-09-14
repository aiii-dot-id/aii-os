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

// .
// .
// .
// .
// .
// .

// .
// .
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
		nil,
		nil,
		t.TempDir(),
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

// .
// .
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

// .
// .
func TestCheckClearsAStaleOfferWhenCurrent(t *testing.T) {
	srv := releaseServer(t, http.StatusOK, map[string]any{"tag_name": "v0.3.0"})
	c := checkerAgainst(t, srv, "0.3.0")
	c.State().SetAvailable("0.9.9")

	if got := c.Check(context.Background()); got != "" {
		t.Fatalf("Check offered %q while running the same version", got)
	}
	if av := c.State().Available(); av != "" {
		t.Fatalf("a stale offer survived: %q — the dashboard would keep advertising an update that is not one", av)
	}
}

// .
// .
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

// .
// .
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

// .
// .
// .
// .
func TestCanStageBesideProbesTheRealDirectory(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	if err := os.WriteFile(exe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, why := canStageBesideAt(exe); !ok {
		t.Fatalf("a writable directory refused staging: %s", why)
	}

	if runtime.GOOS == "windows" {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		t.Skip("a mode bit cannot make a directory unwritable on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory is still writable, so the refusal cannot be observed")
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
