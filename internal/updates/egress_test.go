package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// .
// .
// .
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
	// .
	// .
	// .
	// .
	c := newTestChecker(nil, t.TempDir(), nil)
	c.hostAllowlist = gitHubReleaseHost
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

// .
// .
// .
func TestTheDownloadClientRefusesARedirectOffGitHub(t *testing.T) {
	priv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("SSRF"))
	}))
	defer priv.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, priv.URL, http.StatusFound)
	}))
	defer redir.Close()

	c := newTestChecker(nil, t.TempDir(), nil)
	c.hostAllowlist = gitHubReleaseHost
	// .
	// .
	// .
	req, _ := http.NewRequestWithContext(context.Background(), "GET", redir.URL, nil)
	_, err := c.httpClient.Do(req)
	if err == nil {
		t.Fatal("a redirect to a loopback address was followed")
	}
	// .
	// .
	// .
	// .
	if !strings.Contains(err.Error(), "release URL") {
		t.Fatalf("redirect refused for the wrong reason: %v", err)
	}
}
