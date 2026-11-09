package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// builtInServer is a server holding one built-in section ("shown", the
// binary's own bytes) and one installed section ("installed", a directory on
// disk), under a token, at example.test.
func builtInServer(t *testing.T, safe bool) (*Server, *sections.Registry) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<!doctype html>installed", "disk-only.js": "export {};"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reg := sections.NewRegistry()
	if safe {
		reg.SetSafeSource(func() (string, bool) { return "test reason", true })
	}
	for _, sec := range []*sections.Section{
		{Decl: sections.Decl{ID: "shown", Title: "Shown", Slot: "panel", Entry: "index.html"}, BuiltIn: true,
			// Dir names the installed section's directory, which holds
			// disk-only.js: a built-in section reads its FS alone.
			Dir: dir,
			FS: fstest.MapFS{
				"index.html":     {Data: []byte("<!doctype html>shown")},
				"main.js":        {Data: []byte("export {};")},
				"notes.txt":      {Data: []byte("not servable")},
				"sub/styles.css": {Data: []byte("#x{}")},
			}},
		{Decl: sections.Decl{ID: "installed", Title: "Installed", Slot: "rail", Entry: "index.html"}, Dir: dir, PackageID: "id.example.installed"},
	} {
		if err := reg.Register(sec); err != nil {
			t.Fatal(err)
		}
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	s.AllowHost("example.test:8443")
	s.SetSections(reg)
	s.SetAccessToken(true, "built-in-token-0123456789abcdef0123456789abcdef")
	return s, reg
}

func getAt(s *Server, p string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test:8443"+p, nil))
	return w
}

// A BUILT-IN SECTION IS SERVED AS THE FRAME'S OWN BYTES ARE: with no
// sign-in asked (here, under a token, with no cookie), from the binary's
// own tree alone, with Access-Control-Allow-Origin: * so its sandboxed
// frame can load its modules, and under the section's own policy, whose one
// source is the built-in route's own path. The same fences as the installed
// route: a type it does not serve, a path that climbs, a file the tree does
// not hold.
func TestABuiltInSectionIsServedAsTheFramesBytesAre(t *testing.T) {
	s, _ := builtInServer(t, false)
	w := getAt(s, "/built-in-sections/shown/main.js")
	if w.Code != 200 || w.Body.String() != "export {};" {
		t.Fatalf("a built-in section's file under a token, with no cookie: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("a built-in section's file says Access-Control-Allow-Origin %q, want *", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("a built-in section's file allows credentials: %q", got)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' http://example.test:8443/built-in-sections/shown/ ") || !strings.Contains(csp, "connect-src 'none'") {
		t.Errorf("a built-in section's policy is not its own: %q", csp)
	}
	if w.Header().Get("X-Frame-Options") != "SAMEORIGIN" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("a built-in section's file lost its framing or sniffing header: %v", w.Header())
	}
	if w := getAt(s, "/built-in-sections/shown/sub/styles.css"); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Errorf("a built-in section's stylesheet in a folder: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, p := range []string{
		"/built-in-sections/shown/notes.txt",            // a type it does not serve
		"/built-in-sections/shown/..%2fshown%2fmain.js", // a climbing path, escaped past the mux
		"/built-in-sections/shown/disk-only.js",         // on disk beside it, not in its tree
		"/built-in-sections/installed/index.html",       // an installed section
		"/built-in-sections/nobody/index.html",          // no such section
	} {
		if w := getAt(s, p); w.Code != 404 {
			t.Errorf("%s answered %d, want 404", p, w.Code)
		}
	}
}

// THE INSTALLED ROUTE KEEPS ITS WALL, AND SERVES NO BUILT-IN SECTION. Under
// a token, an installed section's file without the cookie is refused, as on
// main; a built-in section's id asked of it is not found; and it says no
// Access-Control-Allow-Origin.
func TestTheInstalledRouteKeepsItsWallAndServesNoBuiltIn(t *testing.T) {
	s, _ := builtInServer(t, false)
	if w := getAt(s, "/sections/installed/index.html"); w.Code != 404 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an installed section's file with no cookie under a token: %d, Access-Control-Allow-Origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	s.SetAccessToken(false, "")
	if w := getAt(s, "/sections/installed/index.html"); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("an installed section's file with no token: %d, Access-Control-Allow-Origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w := getAt(s, "/sections/shown/index.html"); w.Code != 404 {
		t.Errorf("the installed route served a built-in section: %d", w.Code)
	}
}

// THE SECTION API IS SENT ACROSS ORIGINS, as shipped code: a section's
// frame is an opaque origin and loads it as a module.
func TestTheSectionAPIIsSentAcrossOrigins(t *testing.T) {
	s, _ := builtInServer(t, false)
	w := getAt(s, "/section-api.js")
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("/section-api.js: %d, Access-Control-Allow-Origin %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	if w := getAt(s, "/app.js"); w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("the frame's other files are sent across origins too: /app.js says %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

// UNDER SAFE THE BUILT-IN SECTIONS STAY: the frame is told of them alone,
// with SAFE's reason, and an installed section is not listed.
func TestUnderSAFEOnlyBuiltInSectionsAreListed(t *testing.T) {
	s, _ := builtInServer(t, true)
	states, reason := s.sectionStates()
	if reason != "test reason" || len(states) != 1 || states[0].ID != "shown" || !states[0].BuiltIn {
		t.Fatalf("under SAFE the frame was told %+v (reason %q); want the built-in section alone, with the reason", states, reason)
	}
	s2, _ := builtInServer(t, false)
	states, reason = s2.sectionStates()
	if reason != "" || len(states) != 2 {
		t.Fatalf("outside SAFE the frame was told %+v (reason %q); want both sections", states, reason)
	}
}

// AN INSTALLED SECTION CANNOT TAKE A BUILT-IN SECTION'S ID, by registering
// beside it or by replacing it.
func TestAnInstalledSectionCannotTakeABuiltInsID(t *testing.T) {
	_, reg := builtInServer(t, false)
	builtIn, _ := reg.Get("shown")
	impostor := &sections.Section{Decl: sections.Decl{ID: "shown", Title: "Impostor", Slot: "panel", Entry: "index.html"}, Dir: t.TempDir()}
	if err := reg.Register(impostor); err == nil {
		t.Fatal("an installed section was registered under a built-in section's id")
	}
	if err := reg.Replace(builtIn, impostor); err == nil {
		t.Fatal("an installed section replaced a built-in section")
	}
	if got, _ := reg.Get("shown"); got != builtIn {
		t.Fatalf("the built-in section's id now answers %+v", got)
	}
}

// A MINTED ADDRESS READS THE OVERLAY, AND NOTHING ELSE DOES. Only the frame the
// signed-in socket hears carries it. With it: the section's own files first, then
// the identity's overlay of a file the section names, by the page's rules (a
// script only as approved), then the embedded bytes. Without it: the embedded
// bytes alone. It is bound to one section and to the sign-in: a made-up one, one
// section's on another, and one from before a rotation are refused.
func TestAMintedAddressReadsTheOverlayAndNothingElseDoes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "views/x.js", "overlay view")
	writeOverlay(t, dir, "unapproved.js", "overlay unapproved")
	writeOverlay(t, dir, "custom.css", "overlay css")
	writeOverlay(t, dir, "ws.js", "overlay socket")
	writeOverlay(t, dir, "not-named.css", "overlay not named")
	own := fstest.MapFS{"index.html": {Data: []byte("own page")}, "ws.js": {Data: []byte("own socket")}}
	frame := fstest.MapFS{"views/x.js": {Data: []byte("shipped view")}, "unapproved.js": {Data: []byte("shipped unapproved")}, "custom.css": {Data: []byte("shipped css")}, "not-named.css": {Data: []byte("shipped not named")}}
	reg := sections.NewRegistry()
	for _, id := range []string{"one", "two"} {
		if err := reg.Register(&sections.Section{
			Decl: sections.Decl{ID: id, Title: id, Slot: "view", Entry: "index.html"}, BuiltIn: true, Views: []string{id},
			FS: builtInTree{own: own, frame: frame, shared: []string{"views/x.js", "unapproved.js", "custom.css"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	s.AllowHost("example.test:8443")
	s.SetSections(reg)
	s.SetUIOverlay(dir)
	// The socket's copy is approved, so only the section's own files coming first keeps it out.
	s.SetApprovedScripts([]ApprovedScript{
		{Path: "views/x.js", SHA256: digestOf(t, filepath.Join(dir, "views/x.js"))},
		{Path: "ws.js", SHA256: digestOf(t, filepath.Join(dir, "ws.js"))},
	})
	s.SetAccessToken(true, "minted-token-0123456789abcdef0123456789abcdef")
	address := func(id string) string {
		t.Helper()
		states, _ := s.sectionStates()
		for _, st := range states {
			if st.ID == id {
				return st.Address
			}
		}
		t.Fatalf("no address for %s", id)
		return ""
	}
	one := address("one")
	if !strings.HasPrefix(one, "/built-in-sections/one/~") || one == address("two") {
		t.Fatalf("the addresses: %q, %q", one, address("two"))
	}
	for p, want := range map[string]string{
		one + "views/x.js":                  "overlay view",
		one + "custom.css":                  "overlay css",
		one + "unapproved.js":               "shipped unapproved", // a script of the overlay runs only as approved
		one + "ws.js":                       "own socket",         // the section's own stay first
		one + "index.html":                  "own page",
		"/built-in-sections/one/views/x.js": "shipped view",
		"/built-in-sections/one/custom.css": "shipped css",
	} {
		w := getAt(s, p)
		if w.Code != 200 || w.Body.String() != want {
			t.Errorf("%s: %d %q, want %q", p, w.Code, w.Body.String(), want)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s says no cross-origin header", p)
		}
	}
	w := getAt(s, one+"custom.css")
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), one) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("the minted address's headers: %v", w.Header())
	}
	for _, p := range []string{
		one + "not-named.css",
		"/built-in-sections/one/~" + strings.Repeat("0", 64) + "/custom.css",
		strings.Replace(one, "/one/", "/two/", 1) + "custom.css",
	} {
		if w := getAt(s, p); w.Code != 404 {
			t.Errorf("%s answered %d: %q", p, w.Code, w.Body.String())
		}
	}
	s.SetAccessToken(true, "rotated-token-0123456789abcdef0123456789abcdef")
	if w := getAt(s, one+"custom.css"); w.Code != 404 {
		t.Errorf("an address from before a rotation answered %d", w.Code)
	}
	if w := getAt(s, address("one")+"custom.css"); w.Code != 200 || w.Body.String() != "overlay css" {
		t.Errorf("the address after a rotation: %d %q", w.Code, w.Body.String())
	}
	if got := pathToLog(one + "custom.css"); strings.Contains(got, strings.TrimPrefix(strings.Split(one, "/")[3], "~")) || !strings.HasSuffix(got, "custom.css") {
		t.Errorf("a log line would say the address: %q", got)
	}
}

// EVERY TOPIC THE FRAME ASKS FOR IS ASKED BY A QUERY THE SERVER ANSWERS.
func TestEveryTopicTheFrameAsksForIsAQueryTheServerAnswers(t *testing.T) {
	bridge, err := staticFS.ReadFile("static/bridge.js")
	if err != nil {
		t.Fatal(err)
	}
	asked := regexp.MustCompile(`const ASKED = \{([^}]*)\}`).FindSubmatch(bridge)
	if asked == nil {
		t.Fatal("rig: bridge.js has no ASKED")
	}
	server, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	doors := regexp.MustCompile(`: '([^']+)'`).FindAllSubmatch(asked[1], -1)
	if len(doors) == 0 {
		t.Fatal("rig: ASKED names no query")
	}
	for _, m := range doors {
		if !strings.Contains(string(server), `case "`+string(m[1])+`":`) {
			t.Errorf("the frame asks by %q, and the server has no such query", m[1])
		}
	}
}
