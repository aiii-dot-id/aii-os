package updates

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// recordingTransport answers every request itself, with an empty release,
// and keeps the URL each was sent to: nothing leaves the machine.
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.String())
	r.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.0.1","assets":[]}`)), Header: http.Header{}, Request: req}, nil
}

func checkerWithAPIURL(t *testing.T, apiURL *string) *Checker {
	t.Helper()
	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "0.1.0" },
		func() bool { return false },
		nil,
		nil,
		t.TempDir(),
		func() bool { return false }, func() bool { return false },
	)
	if apiURL != nil {
		c.SetAPIURL(func() string { return *apiURL })
	}
	return c
}

// WITH NOTHING SET, THE RELEASE API ASKED IS THE ONE THE CODE ALWAYS ASKED,
// byte for byte: no setter, an empty updates.api_url, and the default
// written out with a trailing slash all ask the same URL.
func TestTheDefaultReleaseURLIsTheOneAlwaysAsked(t *testing.T) {
	const want = "https://api.github.com/repos/aiii-dot-id/aii-os/releases/latest"
	empty, written := "", "https://api.github.com/"
	for name, set := range map[string]*string{"no setter": nil, "empty": &empty, "the default with a slash": &written} {
		c := checkerWithAPIURL(t, set)
		rec := &recordingTransport{}
		c.httpClient = &http.Client{Transport: rec}
		if _, err := c.fetchLatestRelease(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(rec.urls) != 1 || rec.urls[0] != want {
			t.Errorf("%s: asked %v, want exactly %q", name, rec.urls, want)
		}
	}
	if DefaultAPIURL != "https://api.github.com" {
		t.Errorf("DefaultAPIURL is %q: the default release API changed", DefaultAPIURL)
	}
}

// mirrorTransport reaches srv, a TLS server, as https://mirror.example:8443
// and refuses to dial anything else, so a request that went anywhere but
// the configured mirror fails rather than leaving the machine.
func mirrorTransport(t *testing.T, srv *httptest.Server) http.RoundTripper {
	t.Helper()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com" // the test certificate's name
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "mirror.example:8443" {
			return nil, errors.New("rig: dialled " + addr + ", not the mirror")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	return tr
}

// A CONFIGURED MIRROR IS THE ONE FETCHED, and what it lists is downloaded
// from it: the release metadata is asked of https://mirror.example:8443
// (an httptest TLS server reached under that name), an asset on the
// mirror is downloaded through the production fence, and an asset or a
// redirect the mirror's unsigned metadata points at GitHub is refused at
// the fence before anything is fetched. The fence is the release's own
// source, recorded when it was read: with updates.api_url set back to
// GitHub's afterwards, the mirror's GitHub asset is still refused and its
// own still admitted.
func TestAConfiguredMirrorIsTheOneFetched(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.Host+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/repos/aiii-dot-id/aii-os/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","assets":[{"name":"a.tar.gz","browser_download_url":"https://mirror.example:8443/dl/a.tar.gz"},{"name":"b.tar.gz","browser_download_url":"https://objects.githubusercontent.com/b.tar.gz"}]}`))
		case "/dl/a.tar.gz":
			_, _ = w.Write([]byte("archive"))
		case "/away":
			http.Redirect(w, r, "https://objects.githubusercontent.com/b.tar.gz", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	mirror := "https://mirror.example:8443"
	c := checkerWithAPIURL(t, &mirror)
	c.httpClient.Transport = mirrorTransport(t, srv) // NewChecker's client, its redirect fence kept

	release, err := c.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("the mirror was not the release API asked: %v", err)
	}
	if release.TagName != "v9.9.9" {
		t.Fatalf("read %q, not the mirror's release", release.TagName)
	}
	if release.source == nil || release.source.String() != mirror {
		t.Fatalf("the release does not record the source it was read from: %v", release.source)
	}
	mirror = "" // the operator goes back to GitHub's source after the check
	got, err := c.download(context.Background(), release.source, release.Assets[0].BrowserDownloadURL)
	if err != nil || string(got) != "archive" {
		t.Fatalf("an asset on the mirror was not downloaded through the fence: %q, %v", got, err)
	}
	if _, err := c.download(context.Background(), release.source, release.Assets[1].BrowserDownloadURL); err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("an asset on GitHub, listed by a mirror, passed the fence: %v", err)
	}
	if _, err := c.download(context.Background(), release.source, "https://mirror.example:8443/away"); err == nil || !strings.Contains(err.Error(), "release source") {
		t.Fatalf("a redirect from the mirror to GitHub was followed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"mirror.example:8443/repos/aiii-dot-id/aii-os/releases/latest", "mirror.example:8443/dl/a.tar.gz", "mirror.example:8443/away"}
	if strings.Join(asked, " ") != strings.Join(want, " ") {
		t.Errorf("the mirror was asked %v, want %v", asked, want)
	}
}

// AN API URL THAT IS NOT HTTPS, OR NOT A SERVER'S ADDRESS, IS REFUSED: by
// APIBase (which the loader holds the file to), and by the checker for a
// configuration built in code, which then asks nothing rather than falling
// back to GitHub. A path is kept, less its trailing slash, for a forge that
// serves the API under one.
func TestAnAPIURLThatIsNotHTTPSIsRefused(t *testing.T) {
	for _, bad := range []string{
		"http://mirror.example:8443",
		"mirror.example",
		"//mirror.example",
		"ftp://mirror.example",
		"https://",
		"https:mirror.example",
		"https://user:pw@mirror.example",
		"https://mirror.example/?x=1",
		"https://mirror.example/#releases",
		"https://mirror.example/%zz",
	} {
		if _, err := APIBase(bad); err == nil {
			t.Errorf("APIBase(%q) was accepted", bad)
		}
	}
	for raw, want := range map[string]string{
		"":                               "https://api.github.com",
		"https://mirror.example:8443":    "https://mirror.example:8443",
		"https://forge.example/api/v1/":  "https://forge.example/api/v1",
		"https://forge.example/api%20v1": "https://forge.example/api%20v1",
	} {
		base, err := APIBase(raw)
		if err != nil || base.String() != want {
			t.Errorf("APIBase(%q) = %v, %v; want %q", raw, base, err, want)
		}
	}

	plain := "http://mirror.example:8443"
	c := checkerWithAPIURL(t, &plain)
	rec := &recordingTransport{}
	c.httpClient = &http.Client{Transport: rec}
	if _, err := c.fetchLatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "updates.api_url") || !strings.Contains(err.Error(), "https") {
		t.Fatalf("a plaintext api_url was not refused by its key and its requirement: %v", err)
	}
	if len(rec.urls) != 0 {
		t.Fatalf("a refused api_url still asked %v", rec.urls)
	}
}

// AN ASSET URL OFF THE ADMITTED HOSTS IS REFUSED. Under a mirror only its
// own host and port, over https; under the default, GitHub's asset hosts
// as before the setting, and never the mirror's; with no recorded source,
// nothing.
func TestAnAssetURLOffTheAdmittedHostsIsRefused(t *testing.T) {
	m, err := APIBase("https://mirror.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"https://mirror.example:8443/dl/a.tar.gz", "https://MIRROR.example:8443/dl/a.tar.gz"} {
		if err := releaseHost(m, ok); err != nil {
			t.Errorf("under the mirror, %s was refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"https://mirror.example/dl/a.tar.gz",      // 443, not the mirror's port
		"https://mirror.example:8444/dl/a.tar.gz", // another port
		"http://mirror.example:8443/dl/a.tar.gz",  // plaintext
		"https://objects.githubusercontent.com/b", // GitHub, which the operator did not name
		"https://github.com/o/r/releases/download/v/a.tar.gz",
		"https://evil.example:8443/a.tar.gz",
		"https://mirror.example.evil:8443/a.tar.gz",
		"https://127.0.0.1:8443/a.tar.gz",
	} {
		if err := releaseHost(m, bad); err == nil {
			t.Errorf("under the mirror, %s was admitted", bad)
		}
	}

	d := defaultSource(t)
	for _, ok := range []string{
		"https://github.com/o/r/releases/download/v0.1.0/a.tar.gz",
		"https://objects.githubusercontent.com/github-production-release-asset/x",
		"https://api.github.com/repos/o/r/releases/latest",
	} {
		if err := releaseHost(d, ok); err != nil {
			t.Errorf("under the default, %s was refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"https://mirror.example:8443/a.tar.gz", "http://github.com/a", "https://evil.example/a"} {
		if err := releaseHost(d, bad); err == nil {
			t.Errorf("under the default, %s was admitted", bad)
		}
	}
	if err := releaseHost(nil, "https://github.com/o/r/releases/download/v/a.tar.gz"); err == nil {
		t.Error("with no recorded source the fence admitted a URL")
	}
	// And the production checker's fence is this one.
	if err := checkerWithAPIURL(t, nil).hostAllowlist(d, "https://mirror.example:8443/a.tar.gz"); err == nil {
		t.Error("NewChecker's fence admitted a host the default does not")
	}
}

// A RELEASE HELD FROM ONE SOURCE IS NOT APPLIED UNDER ANOTHER, either way:
// read from GitHub's source and updates.api_url then names a mirror, or
// read from a mirror and the setting then names GitHub's again, Apply and
// the package path refuse it, typed and saying so, and fetch nothing. Under
// the source it was read from it applies.
func TestAReleaseHeldFromAnotherSourceIsNotApplied(t *testing.T) {
	mirrorBase, err := APIBase("https://mirror.example:8443")
	if err != nil {
		t.Fatal(err)
	}
	for _, way := range []struct {
		name         string
		readFrom     *neturl.URL
		now, settled string
	}{
		{"github then a mirror", defaultSource(t), "https://mirror.example:8443", ""},
		{"a mirror then github", mirrorBase, "", "https://mirror.example:8443"},
	} {
		t.Run(way.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "aii")
			writeFile(t, dir, "aii", "old binary")
			writeFile(t, dir, markerFile, "ok")
			c, hits := applyOwnerFixture(t, dir)
			c.cachedRelease.source = way.readFrom
			setting := way.now
			c.SetAPIURL(func() string { return setting })
			if err := c.applyTo(t.Context(), exe); !errors.Is(err, ErrReleaseSourceChanged) {
				t.Fatalf("a release read from %s was applied under %q: %v", way.readFrom, way.now, err)
			}
			if hits.Load() != 0 {
				t.Fatalf("the refused release's assets were fetched (%d requests)", hits.Load())
			}
			assertApplyFile(t, exe, "old binary")
			setting = way.settled
			if err := c.applyTo(t.Context(), exe); err != nil {
				t.Fatalf("under the source it was read from, the release was not applied: %v", err)
			}
			assertApplyFile(t, exe, "new binary")
		})
	}

	rig := newPackageRig(t, []byte("the package's bytes"), nil)
	rig.c.SetAPIURL(func() string { return "https://mirror.example:8443" })
	if got := rig.prepared(); got.Path != "" || !strings.Contains(got.Refusal, ErrReleaseSourceChanged.Error()) || rig.fetches() != 0 {
		t.Fatalf("a package held from GitHub's source was offered under a mirror: %+v, %d fetches", got, rig.fetches())
	}
}

// A FAILED ANSWER NAMES THE SOURCE THAT GAVE IT: under a mirror, a release
// API that answers 500 is said as that server's answer, not GitHub's.
func TestAFailedAnswerNamesTheSourceThatGaveIt(t *testing.T) {
	mirror := "https://mirror.example:8443"
	c := checkerWithAPIURL(t, &mirror)
	c.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("down")), Header: http.Header{}, Request: req}, nil
	})}
	_, err := c.fetchLatestRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "release API https://mirror.example:8443 returned 500") {
		t.Fatalf("a mirror's failure was not said as the mirror's: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
