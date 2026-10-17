package dashboard

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

// setAsideRig is the dashboard's own handler behind a test server, with an
// overlay directory and, when token is set, the access token in force.
type setAsideRig struct {
	t     *testing.T
	s     *Server
	srv   *httptest.Server
	dir   string
	token bool
}

func newSetAsideRig(t *testing.T, token bool) *setAsideRig {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{})
	if token {
		s.SetAccessToken(true, "test-token")
	}
	dir := filepath.Join(t.TempDir(), "ui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s.SetUIOverlay(dir)
	srv := httptest.NewServer(s.server.Handler)
	t.Cleanup(srv.Close)
	s.AllowHost(strings.TrimPrefix(srv.URL, "http://"))
	return &setAsideRig{t: t, s: s, srv: srv, dir: dir, token: token}
}

// do sends one request as the dashboard's own page would — its Origin, its
// cookie — unless the caller says otherwise through edit.
func (g *setAsideRig) do(method, urlPath, body string, edit func(*http.Request)) (int, []byte) {
	g.t.Helper()
	req, err := http.NewRequest(method, g.srv.URL+urlPath, strings.NewReader(body))
	if err != nil {
		g.t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", g.srv.URL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/json")
	}
	if g.token {
		req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: g.s.accessHash()})
	}
	if edit != nil {
		edit(req)
	}
	resp, err := g.srv.Client().Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		g.t.Fatal(err)
	}
	return resp.StatusCode, got
}

func (g *setAsideRig) listed() []OverlayReplacement {
	g.t.Helper()
	code, body := g.do(http.MethodGet, "/overlay/replacements", "", nil)
	if code != http.StatusOK {
		g.t.Fatalf("GET /overlay/replacements = %d %s", code, body)
	}
	var got struct {
		Files []OverlayReplacement `json:"files"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		g.t.Fatalf("GET /overlay/replacements: %v in %s", err, body)
	}
	return got.Files
}

func (g *setAsideRig) setAside(paths ...string) (int, []byte) {
	g.t.Helper()
	b, _ := json.Marshal(map[string]any{"paths": paths})
	return g.do(http.MethodPost, "/overlay/set-aside", string(b), nil)
}

func (g *setAsideRig) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(g.dir, filepath.FromSlash(rel)))
	return err == nil
}

// THE SERVER NAMES WHAT REPLACES THE SHIPPED FRAME (T72), and only that: the
// files the frame route would serve in place of a shipped one. The additive
// stubs, the identity's own new files, a non-frame file, a file over the
// ceiling, a directory and a link that leaves the directory replace nothing
// the page receives, and are not named.
func TestTheOverlayListsOnlyWhatReplacesTheShippedFrame(t *testing.T) {
	g := newSetAsideRig(t, false)
	writeOverlay(t, g.dir, "views/plugin-setting.js", "export const stale = 1;\n")
	writeOverlay(t, g.dir, "app.js", "export {};\n")
	writeOverlay(t, g.dir, "theme.css", "body{}\n")
	writeOverlay(t, g.dir, "custom.css", "body{}\n")
	writeOverlay(t, g.dir, "custom.js", "export {};\n")
	writeOverlay(t, g.dir, "views/mine.js", "export {};\n")
	writeOverlay(t, g.dir, "README.md", "# notes\n")
	writeOverlay(t, g.dir, "util.js", strings.Repeat("x", maxOverlayBytes+1))

	approveOverlay(t, g.s, g.dir, "views/plugin-setting.js", "app.js", "custom.js", "views/mine.js")
	if err := os.MkdirAll(filepath.Join(g.dir, "layout.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.js")
	if err := os.WriteFile(outside, []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(g.dir, "ws.js")); err != nil {
		t.Logf("symlinks unavailable on this host, the escaping link is not part of this run: %v", err)
	}
	when := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(g.dir, "views", "plugin-setting.js"), when, when); err != nil {
		t.Fatal(err)
	}

	got := g.listed()
	var names []string
	for _, f := range got {
		names = append(names, f.Path)
	}
	if strings.Join(names, " ") != "app.js theme.css views/plugin-setting.js" {
		t.Fatalf("listed %q; want app.js, theme.css and views/plugin-setting.js", names)
	}
	stale := got[2]
	if stale.Size != int64(len("export const stale = 1;\n")) || !stale.MTime.Equal(when) {
		t.Errorf("views/plugin-setting.js listed as %d bytes at %v; want %d at %v", stale.Size, stale.MTime, len("export const stale = 1;\n"), when)
	}

	// No overlay directory at all is an empty list, not a failure.
	if err := os.RemoveAll(g.dir); err != nil {
		t.Fatal(err)
	}
	if got := g.listed(); len(got) != 0 {
		t.Errorf("with no overlay directory, listed %v", got)
	}
}

// SETTING ASIDE RENAMES AND NEVER DELETES. The file is renamed to
// <name>.set-aside-<version>, which the frame never serves, so the shipped
// file is served again; asking again changes nothing; a second set-aside of
// a newer copy takes the next name and keeps the first.
func TestSetAsideRenamesTheReplacementAndKeepsItsBytes(t *testing.T) {
	g := newSetAsideRig(t, false)
	writeOverlay(t, g.dir, "views/plugin-setting.js", "export const stale = 1;\n")
	writeOverlay(t, g.dir, "custom.css", "body{color:red}\n")
	approveOverlay(t, g.s, g.dir, "views/plugin-setting.js")
	if code, body := g.do(http.MethodGet, "/views/plugin-setting.js", "", nil); code != 200 || string(body) != "export const stale = 1;\n" {
		t.Fatalf("control: the overlay's copy is not what the page receives (%d)", code)
	}

	want := "views/plugin-setting.js.set-aside-" + version.Authored()
	code, body := g.setAside("views/plugin-setting.js")
	if code != http.StatusOK || !strings.Contains(string(body), `"to":"`+want+`"`) {
		t.Fatalf("set aside = %d %s; want it renamed to %s", code, body, want)
	}
	if g.exists("views/plugin-setting.js") {
		t.Fatal("the replacement is still in place")
	}
	if b, err := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(want))); err != nil || string(b) != "export const stale = 1;\n" {
		t.Fatalf("the set-aside copy lost its bytes: %q %v", b, err)
	}
	shipped, _ := staticFS.ReadFile("static/views/plugin-setting.js")
	if code, body := g.do(http.MethodGet, "/views/plugin-setting.js", "", nil); code != 200 || !bytes.Equal(body, shipped) {
		t.Errorf("after setting aside, /views/plugin-setting.js is not the shipped file (%d)", code)
	}
	if code, _ := g.do(http.MethodGet, "/"+want, "", nil); code != http.StatusNotFound {
		t.Errorf("the set-aside copy is served (%d)", code)
	}
	if !g.exists("custom.css") {
		t.Error("setting aside took custom.css with it")
	}
	if got := g.listed(); len(got) != 0 {
		t.Errorf("still listed after setting aside: %v", got)
	}

	// Idempotent: nothing there now, nothing done, and the copy untouched.
	if code, body := g.setAside("views/plugin-setting.js"); code != http.StatusOK || strings.Contains(string(body), `"to"`) {
		t.Errorf("setting aside again = %d %s; want success that renames nothing", code, body)
	}
	// A newer copy set aside under the same version keeps the first.
	writeOverlay(t, g.dir, "views/plugin-setting.js", "export const newer = 1;\n")
	if code, body := g.setAside("views/plugin-setting.js"); code != http.StatusOK || !strings.Contains(string(body), want+`-2"`) {
		t.Fatalf("a second set-aside = %d %s; want %s-2", code, body, want)
	}
	if b, _ := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(want))); string(b) != "export const stale = 1;\n" {
		t.Errorf("the second set-aside overwrote the first: %q", b)
	}
}

// SET-ASIDE TOUCHES ONLY WHAT REPLACES THE SHIPPED FRAME, inside the
// overlay. A path outside the directory, an absolute one, the additive
// stubs, the identity's own new file and a non-frame file are refused, and a
// request that names any of them renames nothing at all. A link that leaves
// the directory replaces nothing and is left where it is, as is its target.
func TestSetAsideRefusesWhatDoesNotReplaceTheShippedFrame(t *testing.T) {
	g := newSetAsideRig(t, false)
	writeOverlay(t, g.dir, "app.js", "export {};\n")
	writeOverlay(t, g.dir, "custom.js", "export {};\n")
	writeOverlay(t, g.dir, "views/mine.js", "export {};\n")
	writeOverlay(t, g.dir, "README.md", "# notes\n")
	sibling := filepath.Join(filepath.Dir(g.dir), "app.js")
	if err := os.WriteFile(sibling, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../app.js", "/app.js", sibling, "views/../../app.js", "custom.js", "custom.css", "views/mine.js", "README.md", "", "."} {
		code, body := g.setAside("app.js", p)
		if code != http.StatusBadRequest {
			t.Errorf("set aside %q = %d %s; want 400", p, code, body)
		}
	}
	for _, kept := range []string{"app.js", "custom.js", "views/mine.js", "README.md"} {
		if !g.exists(kept) {
			t.Errorf("a refused request moved %s", kept)
		}
	}
	if b, err := os.ReadFile(sibling); err != nil || string(b) != "outside" {
		t.Errorf("the file outside the overlay was touched: %q %v", b, err)
	}
	if code, body := g.do(http.MethodPost, "/overlay/set-aside", `{"paths":[]}`, nil); code != http.StatusBadRequest {
		t.Errorf("an empty list = %d %s; want 400", code, body)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.js")
	if err := os.WriteFile(outside, []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(g.dir, "ws.js")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if code, body := g.setAside("ws.js"); code != http.StatusOK || strings.Contains(string(body), `"to"`) {
		t.Errorf("an escaping link = %d %s; want nothing set aside", code, body)
	}
	if _, err := os.Lstat(filepath.Join(g.dir, "ws.js")); err != nil {
		t.Errorf("the escaping link was moved: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the link's target was moved: %v", err)
	}
}

// THE WALLS OF A WRITE. The list is behind the access token where one is in
// force; the set-aside is behind the token and refuses every request that
// does not come from this dashboard's own page — no Origin, another site's,
// or fetch metadata that says cross-site — renaming nothing.
func TestSetAsideIsWalledAsEveryDashboardWrite(t *testing.T) {
	for _, token := range []bool{true, false} {
		g := newSetAsideRig(t, token)
		writeOverlay(t, g.dir, "app.js", "export {};\n")
		body := `{"paths":["app.js"]}`
		refusals := []struct {
			name string
			edit func(*http.Request)
			want int
		}{
			{"no Origin", func(r *http.Request) { r.Header.Del("Origin") }, http.StatusForbidden},
			{"another site's Origin", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
			{"a null Origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
			{"cross-site fetch metadata", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		}
		if token {
			refusals = append(refusals, struct {
				name string
				edit func(*http.Request)
				want int
			}{"no token", func(r *http.Request) { r.Header.Del("Cookie") }, http.StatusUnauthorized})
			if code, _ := g.do(http.MethodGet, "/overlay/replacements", "", func(r *http.Request) { r.Header.Del("Cookie") }); code != http.StatusUnauthorized {
				t.Errorf("the list without the token = %d; want 401", code)
			}
		}
		for _, c := range refusals {
			if code, got := g.do(http.MethodPost, "/overlay/set-aside", body, c.edit); code != c.want {
				t.Errorf("token=%v, %s: %d %s; want %d", token, c.name, code, got, c.want)
			}
			if !g.exists("app.js") {
				t.Fatalf("token=%v, %s: the file was set aside", token, c.name)
			}
		}
		// A navigation cannot set anything aside: GET is not the route.
		if code, _ := g.do(http.MethodGet, "/overlay/set-aside", "", nil); code == http.StatusOK || !g.exists("app.js") {
			t.Errorf("token=%v: GET /overlay/set-aside = %d, file there: %v", token, code, g.exists("app.js"))
		}
		// The control: the page's own request is taken.
		if code, got := g.do(http.MethodPost, "/overlay/set-aside", body, nil); code != http.StatusOK || g.exists("app.js") {
			t.Errorf("token=%v: the page's own request = %d %s, file still there: %v", token, code, got, g.exists("app.js"))
		}
	}
}
