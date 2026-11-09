package dashboard

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A HOST THE GATE DOES NOT SERVE IS SAID (step 17: an operator locked out
// over a tunnel was given no reason). The gate still refuses
// a Host it does not serve, every route the sign-in among them, with 403:
// it is the wall against DNS rebinding. But a browser is given a page of
// its own in AII OS's words that says what to change and where, needing
// nothing more from the refused host; any other caller gets the words as
// text; and both carry the typed refusal, host.not_served, so a program
// decides by a value. A Host the gate serves is not refused.
func TestAHostTheGateDoesNotServeIsSaidNotABareStatus(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.AllowHost("127.0.0.1:8180")
	gate := s.hostGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	ask := func(method, path, host, accept string) *http.Response {
		r := httptest.NewRequest(method, path, nil)
		r.Host = host
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		return w.Result()
	}
	body := func(res *http.Response) string { b, _ := io.ReadAll(res.Body); return string(b) }

	page := ask(http.MethodGet, "/", "10.8.0.2:8180", "text/html,application/xhtml+xml")
	if page.StatusCode != http.StatusForbidden || page.Header.Get(refusalHeader) != hostNotServed {
		t.Fatalf("a refused Host is answered %d with refusal %q; want 403 and %q", page.StatusCode, page.Header.Get(refusalHeader), hostNotServed)
	}
	if got := body(page); !strings.Contains(got, "AII OS doesn't answer at this address. To allow it, tick it in Settings → Access, under \"The route and addresses\".") ||
		!strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") || strings.Contains(got, "forbidden host") {
		t.Fatalf("A BROWSER REFUSED AT A HOST IS NOT TOLD WHY IN AII OS'S WORDS: %s %q", page.Header.Get("Content-Type"), got)
	}
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("the refusal page asks the refused host for more: CSP %q", csp)
	}
	// It is drawn in its own look, which its policy admits by hash, and it
	// names nothing to fetch.
	got := hostRefusalPage
	style := got[strings.Index(got, "<style>")+len("<style>") : strings.Index(got, "</style>")]
	sum := sha256.Sum256([]byte(style))
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(style, "--accent") || !strings.Contains(csp, "style-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Fatalf("THE REFUSAL PAGE'S LOOK IS NOT ITS OWN, OR ITS POLICY DOES NOT ADMIT IT: CSP %q", csp)
	}
	if strings.Contains(got, "src=") || strings.Contains(got, "href=") || strings.Contains(got, "url(") || strings.Contains(got, "@import") {
		t.Fatal("the refusal page names something to fetch from the refused host")
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/auth/token"}, {http.MethodGet, "/record"}, {http.MethodGet, "/ws"}} {
		res := ask(c.method, c.path, "10.8.0.2:8180", "")
		if res.StatusCode != http.StatusForbidden || res.Header.Get(refusalHeader) != hostNotServed || !strings.Contains(body(res), "The route and addresses") {
			t.Fatalf("%s %s at a refused Host: %d, refusal %q; want 403, %q and the words", c.method, c.path, res.StatusCode, res.Header.Get(refusalHeader), hostNotServed)
		}
	}
	if ok := ask(http.MethodGet, "/", "127.0.0.1:8180", "text/html"); ok.StatusCode != http.StatusNoContent || ok.Header.Get(refusalHeader) != "" {
		t.Fatalf("a Host the gate serves was refused: %d %q", ok.StatusCode, ok.Header.Get(refusalHeader))
	}
}
