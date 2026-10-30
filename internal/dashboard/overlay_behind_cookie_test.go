package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cookieFrameGet loads one frame file from bound as the browser does by itself:
// no Origin, no page key, the sign-in cookie when given.
func cookieFrameGet(t *testing.T, bound, address, cookie string) (int, http.Header, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+bound+address, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName(bound), Value: cookie})
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(body)
}

// shipped is the embedded bytes of one frame file.
func shipped(t *testing.T, name string) string {
	t.Helper()
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheOverlayIsServedOnlyWithTheSignInCookie(t *testing.T) {
	overlay := map[string]string{
		"theme.css":  "/* the identity's theme */",
		"custom.css": "body { color: rebeccapurple }",
		"mine.css":   ".only-the-overlay-has-this {}",
	}
	t.Run("token on", func(t *testing.T) {
		s, bound := keyServer(t, nil)
		dir := t.TempDir()
		for name, body := range overlay {
			writeOverlay(t, dir, name, body)
		}
		s.SetUIOverlay(dir)
		cookie, _ := signInAt(t, bound, pageKeyTestToken)
		if cookie == "" {
			t.Fatal("the sign-in gave no cookie, so this proves nothing")
		}
		for _, c := range []struct{ address, cookie, want, why string }{
			{"/theme.css", "", shipped(t, "theme.css"), "a replaced file without the cookie is the shipped one"},
			{"/theme.css", "not-the-verifier", shipped(t, "theme.css"), "a cookie that does not hold the token is no cookie"},
			{"/shipped/custom.css", "", shipped(t, "custom.css"), "/shipped/ applies the identity's stylesheets only with the cookie"},
			{"/theme.css", cookie, overlay["theme.css"], "with the cookie the overlay is served as before"},
			{"/mine.css", cookie, overlay["mine.css"], "with the cookie a file only the overlay has is served"},
			{"/shipped/custom.css", cookie, overlay["custom.css"], "with the cookie /shipped/ applies the identity's stylesheets (T72)"},
		} {
			code, header, body := cookieFrameGet(t, bound, c.address, c.cookie)
			if code != http.StatusOK || body != c.want {
				t.Fatalf("%s: %s answered %d %.80q, want %.80q", c.why, c.address, code, body, c.want)
			}
			if !strings.Contains(strings.Join(header.Values("Vary"), ","), "Cookie") {
				t.Fatalf("%s does not vary by cookie: a cache could hand one answer for the other", c.address)
			}
		}
		if code, _, body := cookieFrameGet(t, bound, "/mine.css", ""); code != http.StatusNotFound {
			t.Fatalf("A FILE ONLY THE OVERLAY HAS WAS SERVED WITHOUT THE COOKIE: %d %.80q", code, body)
		}
	})
	t.Run("token off", func(t *testing.T) {
		s := New("127.0.0.1", 0, &WSHandler{})
		srv := httptest.NewServer(s.server.Handler)
		t.Cleanup(srv.Close)
		bound := strings.TrimPrefix(srv.URL, "http://")
		s.AllowHost(bound)
		dir := t.TempDir()
		for name, body := range overlay {
			writeOverlay(t, dir, name, body)
		}
		s.SetUIOverlay(dir)
		for _, name := range []string{"theme.css", "mine.css"} {
			if code, _, body := cookieFrameGet(t, bound, "/"+name, ""); code != http.StatusOK || body != overlay[name] {
				t.Fatalf("with no token asked for, the overlay's %s is served as before: %d %.80q", name, code, body)
			}
		}
	})
}

// signInAnswer signs in at bound and answers the door's reply.
func signInAnswer(t *testing.T, bound string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/token", strings.NewReader(pageKeyTestToken))
	req.Header.Set("Origin", "http://"+bound)
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("signing in: %d", res.StatusCode)
	}
	return res
}

// A SIGN-IN SAYS WHEN THE OVERLAY SERVES THE PAGE. A page loaded before its
// browser signed in was given the shipped frame, so the sign-in's answer
// tells it to load once more where the overlay would give it anything, and
// only there: a dashboard whose overlay holds nothing the frame serves
// (the seeded README, a script never approved) does not make a page reload
// for nothing.
func TestASignInSaysWhenTheOverlayServesThePage(t *testing.T) {
	s, bound := keyServer(t, nil)
	dir := t.TempDir()
	writeOverlay(t, dir, "README.md", "the seeded notes")
	writeOverlay(t, dir, "custom.js", "console.log('never approved')")
	s.SetUIOverlay(dir)
	if got := signInAnswer(t, bound).Header.Get(overlayHeader); got != "" {
		t.Fatalf("an overlay that serves the page nothing asked for a reload: %q", got)
	}
	writeOverlay(t, dir, "custom.css", "body { color: rebeccapurple }")
	if got := signInAnswer(t, bound).Header.Get(overlayHeader); got != overlayReload {
		t.Fatalf("THE SIGN-IN DID NOT SAY THE OVERLAY SERVES THE PAGE: %s is %q, want %q; the page keeps the shipped frame it was loaded with", overlayHeader, got, overlayReload)
	}
}
