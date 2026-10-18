package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// approveOverlay approves the overlay files rels as they stand now, as the
// operator would after reading them: each path with the digest of its bytes.
// For the tests whose subject is something an approved script does.
func approveOverlay(t *testing.T, s *Server, dir string, rels ...string) {
	t.Helper()
	var list []ApprovedScript
	for _, rel := range rels {
		list = append(list, ApprovedScript{Path: rel, SHA256: digestOf(t, filepath.Join(dir, filepath.FromSlash(rel)))})
	}
	s.SetApprovedScripts(list)
}

func digestOf(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func frameGet(t *testing.T, s *Server, p string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test:8443"+p, nil))
	return w.Code, w.Body.String()
}

func TestAScriptIsServedOnlyOnceItsExactBytesAreApproved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.js", "document.title = 'v1';\n")
	writeOverlay(t, dir, "views/mine.js", "export const mine = 1;\n")
	writeOverlay(t, dir, "custom.css", "body { color: rgb(1, 2, 3); }\n")
	s := New("example.test", 8443, &WSHandler{})
	s.AllowHost("example.test:8443")
	s.SetUIOverlay(dir)
	shipped, err := staticFS.ReadFile("static/custom.js")
	if err != nil {
		t.Fatal(err)
	}

	if code, body := frameGet(t, s, "/custom.js"); code != http.StatusOK || body != string(shipped) {
		t.Fatalf("an unapproved custom.js was served (%d): %q", code, body)
	}
	if code, body := frameGet(t, s, "/views/mine.js"); code != http.StatusNotFound {
		t.Fatalf("an unapproved script of the identity's own was served (%d): %q", code, body)
	}
	if _, body := frameGet(t, s, "/custom.css"); !strings.Contains(body, "rgb(1, 2, 3)") {
		t.Fatalf("restyling was held: %q", body)
	}
	if !strings.Contains(strings.Join(overlayOutcomes(s), "\n"), "held: a script awaiting your approval") {
		t.Fatalf("the readback does not say the script is held: %q", overlayOutcomes(s))
	}

	approveOverlay(t, s, dir, "custom.js", "views/mine.js")
	if _, body := frameGet(t, s, "/custom.js"); body != "document.title = 'v1';\n" {
		t.Fatalf("an approved custom.js was not served: %q", body)
	}
	if code, _ := frameGet(t, s, "/views/mine.js"); code != http.StatusOK {
		t.Fatalf("an approved script of the identity's own was not served: %d", code)
	}

	// Changed: held again, whatever was approved before.
	writeOverlay(t, dir, "custom.js", "document.title = 'v2';\n")
	if _, body := frameGet(t, s, "/custom.js"); body != string(shipped) {
		t.Fatalf("a changed custom.js ran on its earlier approval: %q", body)
	}
	// Under /shipped/ no script of the identity's is served, approved or not.
	approveOverlay(t, s, dir, "custom.js")
	if _, body := frameGet(t, s, "/shipped/custom.js"); body != string(shipped) {
		t.Fatalf("/shipped/ served a script of the identity's: %q", body)
	}
}

func TestNoScriptTheDashboardServesBecomesAServiceWorker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "worker.js", "self.addEventListener('fetch', () => {});\n")
	s := New("example.test", 8443, &WSHandler{})
	s.AllowHost("example.test:8443")
	s.SetUIOverlay(dir)
	approveOverlay(t, s, dir, "worker.js")
	get := func(p string, asWorker bool) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.test:8443"+p, nil)
		if asWorker {
			r.Header.Set("Service-Worker", "script")
		}
		s.server.Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := get("/worker.js", false); code != http.StatusOK || !strings.Contains(body, "addEventListener") {
		t.Fatalf("the approved script is not served to the page: %d %q", code, body)
	}
	for _, p := range []string{"/worker.js", "/app.js", "/custom.js", "/"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.test:8443"+p, nil)
		r.Header.Set("Service-Worker", "script")
		s.server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.String() != workerEviction {
			t.Errorf("%s was answered as a service worker's script with something else: %d %q", p, w.Code, w.Body.String())
		}
		if ct, cc := w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"); !strings.HasPrefix(ct, "text/javascript") || cc != "no-store" {
			t.Errorf("%s: the worker's body is %q, %q; want a script that is not cached", p, ct, cc)
		}
	}
	if !strings.Contains(workerEviction, "self.registration.unregister()") || strings.Contains(workerEviction, "'fetch'") {
		t.Fatalf("the worker's body does not remove itself, or answers requests: %s", workerEviction)
	}
}

// overlayOutcomes is the readback's outcomes, as the panel shows them.
func overlayOutcomes(s *Server) []string {
	var out []string
	for _, e := range s.overlayMessage().Overlays {
		out = append(out, e.Path+" "+e.Outcome)
	}
	return out
}

// THE FILE IS SHOWN AS TEXT, NEVER RUN, with the digest of exactly the bytes
// shown; the list says which scripts are approved.
func TestTheScriptsAreListedAndShownAsTextWithTheirDigest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.js", "alert(1);\n")
	writeOverlay(t, dir, "views/mine.js", "export const mine = 1;\n")
	s := New("example.test", 8443, &WSHandler{})
	s.AllowHost("example.test:8443")
	s.SetUIOverlay(dir)
	approveOverlay(t, s, dir, "views/mine.js")

	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test:8443/overlay/scripts", nil))
	var got struct {
		Scripts    []OverlayScript `json:"scripts"`
		CanApprove bool            `json:"can_approve"`
		WhyNot     string          `json:"why_not"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	if len(got.Scripts) != 2 || got.Scripts[0].Path != "custom.js" || got.Scripts[0].Approved || !got.Scripts[1].Approved {
		t.Fatalf("the list is wrong: %+v", got.Scripts)
	}
	if got.CanApprove || got.WhyNot != approvalNeedsToken {
		t.Fatalf("a dashboard that asks for no token offered approval: %+v", got)
	}

	w = httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test:8443/overlay/scripts/file?path=custom.js", nil))
	if w.Body.String() != "alert(1);\n" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("the file is not shown as inert text: %d %q %v", w.Code, w.Body, w.Header())
	}
	if w.Header().Get("X-Script-SHA256") != got.Scripts[0].SHA256 {
		t.Fatalf("the digest shown is not the bytes': %q vs %q", w.Header().Get("X-Script-SHA256"), got.Scripts[0].SHA256)
	}
	for _, bad := range []string{"../config.json", "custom.css", "/etc/passwd", "nope.js"} {
		w = httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test:8443/overlay/scripts/file?path="+bad, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%q was shown: %d %q", bad, w.Code, w.Body)
		}
	}
}

// AN APPROVAL IS TAKEN ONLY FROM A BROWSER SIGNED IN WITH THE TOKEN IN
// FORCE, from this site, as JSON, for the bytes as they stand.
func TestAnApprovalIsTakenOnlyFromTheOperatorsSignedInBrowser(t *testing.T) {
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.js", "document.title = 'v1';\n")
	var approved []ApprovedScript
	s := New("example.test", 8443, &WSHandler{ApproveOverlayScript: func(a ApprovedScript) error {
		approved = append(approved, a)
		return nil
	}})
	s.AllowHost("example.test:8443")
	s.SetUIOverlay(dir)
	digest := digestOf(t, filepath.Join(dir, "custom.js"))
	post := func(cookie *http.Cookie, origin, ctype, body string) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "http://example.test:8443/overlay/scripts/approve", strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.Header.Set("Content-Type", ctype)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		s.server.Handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	good := `{"path":"custom.js","sha256":"` + digest + `"}`
	const site = "http://example.test:8443"

	// No token asked for: the identity's shell is as good as a browser here,
	// so nothing is taken.
	if code, body := post(nil, site, "application/json", good); code != http.StatusForbidden || !strings.Contains(body, "ask for its access token") {
		t.Fatalf("an approval was taken from a dashboard that asks for no token: %d %q", code, body)
	}

	s.SetAccessToken(true, token)
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, site+"/auth/token", strings.NewReader(token)))
	cookie := findCookie(w.Result().Cookies(), "aii_token_8443")
	if cookie == nil {
		t.Fatal("signing in set no cookie")
	}
	for _, tc := range []struct {
		name       string
		cookie     *http.Cookie
		origin     string
		ctype      string
		body       string
		wantStatus int
	}{
		{"not signed in", nil, site, "application/json", good, http.StatusUnauthorized},
		{"another site", cookie, "http://elsewhere.test", "application/json", good, http.StatusForbidden},
		{"no Origin", cookie, "", "application/json", good, http.StatusForbidden},
		{"a plain form", cookie, site, "application/x-www-form-urlencoded", "path=custom.js&sha256=" + digest, http.StatusUnsupportedMediaType},
		// A form with enctype="text/plain" can send a body that parses as
		// JSON; only the media type tells it from the page's own request.
		{"a text/plain form shaped as JSON", cookie, site, "text/plain", good, http.StatusUnsupportedMediaType},
		{"a digest it was not shown", cookie, site, "application/json", `{"path":"custom.js","sha256":"` + strings.Repeat("0", 64) + `"}`, http.StatusConflict},
		{"not a script of the folder", cookie, site, "application/json", `{"path":"../config.json","sha256":"` + digest + `"}`, http.StatusNotFound},
	} {
		if code, body := post(tc.cookie, tc.origin, tc.ctype, tc.body); code != tc.wantStatus {
			t.Errorf("%s: %d %q, want %d", tc.name, code, body, tc.wantStatus)
		}
	}
	if len(approved) != 0 {
		t.Fatalf("a refused request reached the approval: %+v", approved)
	}
	if code, body := post(cookie, site, "application/json; charset=utf-8", good); code != http.StatusOK {
		t.Fatalf("the operator's approval was refused: %d %q", code, body)
	}
	if len(approved) != 1 || approved[0].Path != "custom.js" || approved[0].SHA256 != digest {
		t.Fatalf("the approval handed to the host is not the one given: %+v", approved)
	}
}

// AN UNAPPROVED COPY OF A SHIPPED SCRIPT REPLACES NOTHING. The page's way out
// (recovery.js, GET /overlay/replacements) names the overlay's files that
// replace shipped frame files and may stop the page; a script the operator
// has not approved is not served, so it is not named until he approves it.
func TestAnUnapprovedReplacementIsNotListed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "views/plugin-setting.js", "export const stale = 1;\n")
	writeOverlay(t, dir, "theme.css", "body{}\n")
	s := New("example.test", 8443, &WSHandler{})
	s.SetUIOverlay(dir)
	paths := func() []string {
		list, err := s.overlayReplacements()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range list {
			out = append(out, r.Path)
		}
		return out
	}
	if got := strings.Join(paths(), ","); got != "theme.css" {
		t.Fatalf("listed %q; an unapproved script replaces nothing, a stylesheet does", got)
	}
	approveOverlay(t, s, dir, "views/plugin-setting.js")
	if got := strings.Join(paths(), ","); got != "theme.css,views/plugin-setting.js" {
		t.Fatalf("listed %q once approved; want theme.css,views/plugin-setting.js", got)
	}
}
