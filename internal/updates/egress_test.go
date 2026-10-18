package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The release download must refuse any URL that is not an https GitHub
// asset host — the metadata that supplies it is unsigned, so its
// browser_download_url is not trustworthy on its own.
func TestTheDownloadRefusesOffGitHubAndPlaintextURLs(t *testing.T) {
	for _, tc := range []struct {
		url, why string
	}{
		{"http://github.com/x/y/releases/download/v/a.tar.gz", "https"},
		{"https://evil.example/a.tar.gz", "off GitHub"},
		{"https://objects.githubusercontent.example/a", "off GitHub"},
		{"https://127.0.0.1/a", "off GitHub"},
		{"https://raw.githubusercontent.com.evil.example/a", "off GitHub"},
	} {
		if err := gitHubReleaseHost(tc.url); err == nil {
			t.Fatalf("%s was accepted, want refusal (%s)", tc.url, tc.why)
		}
	}
	// And download() itself must apply the guard BEFORE any network:
	// an off-GitHub https URL is refused with the egress error, not a
	// transport error. This is what makes removing the guard CALL (not
	// just the function) a detectable regression.
	c := newTestChecker(nil, t.TempDir(), nil)
	c.hostAllowlist = gitHubReleaseHost // the production fence, for this assertion
	if _, err := c.download(context.Background(), "https://evil.example/a.tar.gz"); err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("download() did not fence an off-GitHub URL at the egress guard: %v", err)
	}

	for _, ok := range []string{
		"https://github.com/o/r/releases/download/v0.1.0/aii-os_0.1.0_linux_amd64.tar.gz",
		"https://objects.githubusercontent.com/github-production-release-asset/...",
		"https://release-assets.githubusercontent.com/x",
		"https://api.github.com/repos/o/r/releases/latest",
	} {
		if err := gitHubReleaseHost(ok); err != nil {
			t.Fatalf("%s was refused, want accepted: %v", ok, err)
		}
	}
}

func TestTheDownloadClientRefusesARedirectOffGitHub(t *testing.T) {
	priv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("SSRF"))
	}))
	defer priv.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, priv.URL, http.StatusFound) // 302 to loopback
	}))
	defer redir.Close()

	c := newTestChecker(nil, t.TempDir(), nil)
	c.hostAllowlist = gitHubReleaseHost // the production fence, for this assertion
	// Bypass the first-hop allowlist to isolate the redirect check: call
	// the client directly the way download() would, then assert the hop
	// was refused.
	req, _ := http.NewRequestWithContext(context.Background(), "GET", redir.URL, nil)
	_, err := c.httpClient.Do(req)
	if err == nil {
		t.Fatal("a redirect to a loopback address was followed")
	}
	// Both guard messages carry "release URL"; the loopback target is
	// refused for being non-https (httptest is plaintext), which is a
	// refusal all the same. The host-on-https-loopback case is unit
	// -tested in TestTheDownloadRefusesOffGitHubAndPlaintextURLs.
	if !strings.Contains(err.Error(), "release URL") {
		t.Fatalf("redirect refused for the wrong reason: %v", err)
	}
}
