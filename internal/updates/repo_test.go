package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestValidRepoShape(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"aiii-dot-id/aii-os", true},
		{"owner/name", true},
		{"o.w-n_er/n.a-m_e", true},
		{"", false},
		{"noslash", false},
		{"/leading", false},
		{"trailing/", false},
		{"three/part/path", false},
		{"has space/name", false},
		{"owner/name?query=1", false},
		{"https://github.com/owner/name", false},
	} {
		if got := validRepo(tt.in); got != tt.want {
			t.Errorf("validRepo(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestResolveRepoFallsBackWithoutFailingClosed(t *testing.T) {
	mk := func(f func() string) *Checker { return &Checker{repo: f} }
	for _, tt := range []struct {
		name string
		c    *Checker
		want string
	}{
		{"nil accessor", &Checker{}, DefaultRepo},
		{"unset", mk(func() string { return "" }), DefaultRepo},
		{"operator's fork", mk(func() string { return "someone/their-fork" }), "someone/their-fork"},
		// .
		// .
		{"malformed", mk(func() string { return "not a repo" }), DefaultRepo},
		{"a URL, not owner/name", mk(func() string { return "https://github.com/o/n" }), DefaultRepo},
	} {
		if got := tt.c.resolveRepo(); got != tt.want {
			t.Errorf("%s: resolveRepo() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// .
// .
// .
func TestConfiguredRepoReachesTheReleaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","assets":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return nil },
		func() string { return "0.1.0" },
		func() bool { return false },
		func() string { return "someone/their-fork" },
		nil,
		t.TempDir(),
	)
	c.httpClient = redirectTo(srv)

	if _, err := c.fetchLatestRelease(context.Background()); err != nil {
		t.Fatalf("fetch against the configured repo failed: %v", err)
	}
	if want := "/repos/someone/their-fork/releases/latest"; !strings.Contains(gotPath, want) {
		t.Errorf("checker asked for %q, want it to contain %q — updates.repo never reached the URL", gotPath, want)
	}
	if strings.Contains(gotPath, DefaultRepo) {
		t.Errorf("checker asked for the default %q despite an operator override", DefaultRepo)
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
// .
// .
// .
// .
// .
// .
// .
// .
func TestNoReleaseNamesTheSituationNotTheTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found","status":"404"}`))
	}))
	t.Cleanup(srv.Close)

	c := checkerAgainst(t, srv, "0.1.0")
	if got := c.Check(context.Background()); got != "" {
		t.Errorf("Check() offered %q against a source with no releases", got)
	}

	snap := c.state.Snapshot("0.1.0")
	if snap.LastError == "" {
		t.Fatal("nothing recorded — an operator cannot tell why no update appeared")
	}
	if !strings.Contains(snap.LastError, "no release published") {
		t.Errorf("recorded %q — it must say the source has published nothing, not merely relay a status code", snap.LastError)
	}
	if !strings.Contains(snap.LastError, DefaultRepo) {
		t.Errorf("recorded %q — it must name WHICH source, since updates.repo makes that configurable", snap.LastError)
	}
	if strings.Contains(snap.LastError, "{") {
		t.Errorf("recorded %q — raw API JSON is the transport talking, not an explanation", snap.LastError)
	}
	if snap.LastCheck.IsZero() {
		t.Error("LastCheck is zero — the check did happen, and its timestamp is what shows the checker is still alive")
	}
	if snap.AvailableVersion != "" {
		t.Errorf("AvailableVersion = %q, want empty — no phantom update", snap.AvailableVersion)
	}
}

// .
// .
func TestRealAPIFailureStillReportsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream is on fire"}`))
	}))
	t.Cleanup(srv.Close)

	c := checkerAgainst(t, srv, "0.1.0")
	_ = c.Check(context.Background())
	if snap := c.state.Snapshot("0.1.0"); snap.LastError == "" {
		t.Error("a 500 from the releases API left LastError empty — real failures must still surface")
	}
}
