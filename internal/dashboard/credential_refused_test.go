package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/coder/websocket"
)

// The line a refused credential leaves: its category, and the peer's address
// without its port.
const refusedLine = "dashboard.refusal: access token refused from 203.0.113.7\n"

// pauses stands where the sleep of a refused credential would: a test reads
// what each refusal was made to wait and waits none of it.
type pauses struct {
	mu  sync.Mutex
	got []time.Duration
}

func (p *pauses) pause(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, d)
}

// take answers the pauses since the last take.
func (p *pauses) take() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	got := p.got
	p.got = nil
	return got
}

// walled is a serving dashboard that asks for its token, with one project file
// behind the wall and its refusals' pauses recorded.
type walled struct {
	*Server
	addr, port string
	waits      *pauses
	lines      *logsink.Capture
}

func newWalled(t *testing.T, token string) *walled {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "notes.md"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, &WSHandler{
		GetConfig:      func() *ConfigState { return &ConfigState{} },
		GetProjectRoot: func(id string) (string, bool) { return proj, id == "p1" },
	})
	g := &walled{Server: s, waits: &pauses{}}
	s.refusedPause = g.waits.pause
	s.SetAccessToken(true, token)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	g.addr = addr
	_, g.port, _ = net.SplitHostPort(addr)
	g.lines = logsink.CaptureForTest(t)
	return g
}

// ask puts one request to the dashboard's own handler, as a peer at
// 203.0.113.7 on the TLS bind, carrying exactly the Cookie header given (none
// when empty). The socket's route is asked from the dashboard's own origin
// unless another is named, as its page would ask.
func (g *walled) ask(t *testing.T, method, path, cookie string, origin ...string) *http.Response {
	t.Helper()
	return g.send(t, httptest.NewRequest(method, "https://"+g.addr+path, nil), cookie, origin...)
}

func (g *walled) send(t *testing.T, req *http.Request, cookie string, origin ...string) *http.Response {
	t.Helper()
	req.RemoteAddr = "203.0.113.7:4711"
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if len(origin) > 0 {
		req.Header.Set("Origin", origin[0])
	} else if req.URL.Path == "/ws" {
		req.Header.Set("Origin", "https://"+g.addr)
	}
	w := httptest.NewRecorder()
	g.server.Handler.ServeHTTP(w, req)
	return w.Result()
}

func (g *walled) current(value string) string { return "aii_token_" + g.port + "=" + value }

func verifierOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Every route that reads the access cookie, and what it answers a request it
// does not admit. Each tells a right cookie from a wrong one, so each can be
// asked whether a guess was right.
var cookieRoutes = []struct {
	method, path string
	refused      int
}{
	{http.MethodGet, "/auth/token", http.StatusUnauthorized},
	{http.MethodHead, "/auth/token", http.StatusUnauthorized},
	{http.MethodPost, "/auth/rotate", http.StatusUnauthorized},
	{http.MethodGet, "/ws", http.StatusForbidden},
	{http.MethodGet, "/p/p1/notes.md", http.StatusNotFound},
	{http.MethodGet, "/sections/hello/index.html", http.StatusNotFound},
	{http.MethodGet, "/interaction/detail", http.StatusUnauthorized},
	{http.MethodGet, "/interaction/thinking", http.StatusUnauthorized},
	{http.MethodGet, "/record", http.StatusUnauthorized},
	{http.MethodGet, "/record/payload", http.StatusUnauthorized},
	{http.MethodGet, "/sent", http.StatusUnauthorized},
	{http.MethodGet, "/sent/body", http.StatusUnauthorized},
	{http.MethodGet, "/overlay/replacements", http.StatusUnauthorized},
	{http.MethodPost, "/overlay/set-aside", http.StatusUnauthorized},
	{http.MethodPost, "/speech/say", http.StatusUnauthorized},
	{http.MethodGet, "/speech/say/some-reply", http.StatusUnauthorized},
	{http.MethodGet, "/speech/heard", http.StatusUnauthorized},
	{http.MethodPost, "/database/export", http.StatusUnauthorized},
	{http.MethodPost, "/restore/upload", http.StatusUnauthorized},
}

func TestAGuessOfferedAsTheCookieIsSlowedAndLoggedOnEveryRoute(t *testing.T) {
	g := newWalled(t, "the-right-token")
	guesses := []struct{ form, offered, cookie string }{
		{"hashed, as the server's own cookie", "guess-one", g.current(verifierOf("guess-one"))},
		{"raw, as the older cookie", "guess-two", g.current("guess-two")},
		{"raw, under the oldest name", "guess-three", legacyDashboardCookieName + "=guess-three"},
	}
	for _, route := range cookieRoutes {
		for _, guess := range guesses {
			g.lines.Reset()
			res := g.ask(t, route.method, route.path, guess.cookie)
			name := route.method + " " + route.path + ", " + guess.form
			if res.StatusCode != route.refused {
				t.Errorf("%s: answered %d, want %d", name, res.StatusCode, route.refused)
			}
			if got := g.waits.take(); len(got) != 1 || got[0] != refusedLoginDelay {
				t.Errorf("%s: paused %v, want one pause of %s — a guess answered at wire speed", name, got, refusedLoginDelay)
			}
			logged := g.lines.String()
			if strings.Count(logged, refusedLine) != 1 {
				t.Errorf("%s: the refusal is not written down once, with the address: %q", name, logged)
			}
			if strings.Contains(logged, guess.offered) || strings.Contains(logged, verifierOf(guess.offered)) {
				t.Errorf("%s: the offered value was written down: %q", name, logged)
			}
			if len(res.Cookies()) != 0 {
				t.Errorf("%s: a refusal wrote a cookie: %v", name, res.Cookies())
			}
		}
	}
}

// A request with no cookie has offered nothing. It is refused as it always
// was: at once, and without a line that says a token was refused.
func TestARequestWithNoCookieIsRefusedAtOnceAndNotCalledAGuess(t *testing.T) {
	g := newWalled(t, "the-right-token")
	for _, route := range cookieRoutes {
		res := g.ask(t, route.method, route.path, "")
		name := route.method + " " + route.path
		if res.StatusCode != route.refused {
			t.Errorf("%s: answered %d, want %d", name, res.StatusCode, route.refused)
		}
		if got := g.waits.take(); len(got) != 0 {
			t.Errorf("%s: a request with no cookie was made to wait %v", name, got)
		}
	}
	if g.lines.Contains("access token refused") {
		t.Errorf("a request with no cookie was written down as a refused token: %q", g.lines.String())
	}
}

// The right cookie is never slowed and never written down, and no route
// answers it as it answers a wrong one — which is what makes each of them a
// place a guess could be checked.
func TestTheRightCookieIsNeitherSlowedNorLogged(t *testing.T) {
	g := newWalled(t, "the-right-token")
	right := g.current(g.accessHash())
	for _, route := range cookieRoutes {
		name := route.method + " " + route.path
		if route.path == "/ws" {
			conn, err := dialWSToken(g.addr, g.accessHash())
			if err != nil {
				t.Fatalf("%s: the right cookie was refused: %v", name, err)
			}
			conn.Close(websocket.StatusNormalClosure, "")
			continue
		}
		res := g.ask(t, route.method, route.path, right)
		// The section is not registered here, so its route answers 404 to
		// everyone; a registered one is TestSectionFilesRequireTheAccessToken's.
		if res.StatusCode == route.refused && !strings.HasPrefix(route.path, "/sections/") {
			t.Errorf("%s: the right cookie was answered %d, as a wrong one is", name, res.StatusCode)
		}
	}
	if got := g.waits.take(); len(got) != 0 {
		t.Errorf("the right cookie was made to wait %v", got)
	}
	if g.lines.Contains("access token refused") {
		t.Errorf("the right cookie was written down as refused: %q", g.lines.String())
	}
}

// The typed login is what it was: a wrong token is refused 401 after one
// pause and one line, the right one is answered with its cookie and neither.
// (TestARefusedLoginIsSlowedAndLogged holds the same on the real clock.)
func TestTheTypedLoginRefusesThroughTheSameOwner(t *testing.T) {
	g := newWalled(t, "the-right-token")
	post := func(token string) *http.Response {
		return g.send(t, httptest.NewRequest(http.MethodPost, "https://"+g.addr+"/auth/token", strings.NewReader(token)), "")
	}
	if res := post("a-wrong-guess"); res.StatusCode != http.StatusUnauthorized || len(res.Cookies()) != 0 {
		t.Fatalf("a wrong token: %d, cookies %v", res.StatusCode, res.Cookies())
	}
	if got := g.waits.take(); len(got) != 1 || got[0] != refusedLoginDelay {
		t.Fatalf("a wrong token paused %v, want one pause of %s", got, refusedLoginDelay)
	}
	if logged := g.lines.String(); strings.Count(logged, refusedLine) != 1 || strings.Contains(logged, "a-wrong-guess") {
		t.Fatalf("a wrong token's line: %q", logged)
	}
	g.lines.Reset()
	res := post("the-right-token")
	if c := findCookie(res.Cookies(), "aii_token_"+g.port); res.StatusCode != http.StatusNoContent || c == nil || c.Value != g.accessHash() {
		t.Fatalf("the right token: %d, cookies %v", res.StatusCode, res.Cookies())
	}
	if got := g.waits.take(); len(got) != 0 || g.lines.Contains("access token refused") {
		t.Fatalf("the right token paused %v and logged %q", got, g.lines.String())
	}
}

// WHAT AN HONEST TAB PAYS. A tab left open holds whatever cookie it was
// given, and its page asks again on its own (ws.js: the socket about every
// ten seconds while it is in view, the status check when a socket closes).
// Each case is one such tab, and what one of its requests costs.
func TestAStaleTabPaysOnlyForACookieThatIsWrong(t *testing.T) {
	const old = "the-token-before"
	quiet := func(t *testing.T, g *walled, what string) {
		t.Helper()
		if got := g.waits.take(); len(got) != 0 {
			t.Errorf("%s: made to wait %v", what, got)
		}
		if g.lines.Contains("access token refused") {
			t.Errorf("%s: written down as a refused token: %q", what, g.lines.String())
		}
	}

	// Another browser's tab after Rotate access token: its cookie is the old
	// token's, which is now wrong. Every request it makes pays, like a guess —
	// there is nothing in the request that tells the two apart — and the
	// refusal leaves its cookie alone: a deletion goes by name, and would take
	// the new cookie of a tab in the same browser that had just rotated.
	t.Run("after a rotation", func(t *testing.T) {
		g := newWalled(t, old)
		stale := g.current(g.accessHash())
		g.SetAccessToken(true, "the-token-after")
		for _, path := range []string{"/ws", "/auth/token", "/ws", "/ws"} {
			g.lines.Reset()
			res := g.ask(t, http.MethodGet, path, stale)
			if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusSwitchingProtocols {
				t.Fatalf("GET %s: the old cookie was admitted: %d", path, res.StatusCode)
			}
			if got := g.waits.take(); len(got) != 1 || got[0] != refusedLoginDelay {
				t.Errorf("GET %s: paused %v, want one pause of %s", path, got, refusedLoginDelay)
			}
			if logged := g.lines.String(); strings.Count(logged, refusedLine) != 1 || strings.Contains(logged, "failed auth") {
				t.Errorf("GET %s: want the one line that says a token was refused: %q", path, logged)
			}
			if len(res.Cookies()) != 0 {
				t.Errorf("GET %s: the refusal touched the browser's cookie: %v", path, res.Cookies())
			}
		}
	})

	// A tab open while Ask for an access token turned the requirement on has
	// no cookie. It is refused and asks for the token; it has guessed nothing.
	t.Run("after a token is asked for", func(t *testing.T) {
		g := newWalled(t, old)
		g.SetAccessToken(false, "")
		g.SetAccessToken(true, "the-token-now-asked-for")
		for _, path := range []string{"/ws", "/auth/token", "/ws"} {
			if res := g.ask(t, http.MethodGet, path, ""); res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("GET %s with no cookie: %d", path, res.StatusCode)
			}
		}
		quiet(t, g, "a tab with no cookie")
	})

	// A browser last signed in by a release that kept the raw token in the
	// cookie holds the right token in an older form. The socket does not take
	// that form and refuses it — as before, at once, and without calling it a
	// refused token — the status check that follows replaces it, and the
	// socket then opens.
	for _, name := range []string{"aii_token_%s", legacyDashboardCookieName} {
		t.Run("holding the right token in an older cookie, "+name, func(t *testing.T) {
			g := newWalled(t, old)
			older := strings.Replace(name, "%s", g.port, 1) + "=" + old
			if res := g.ask(t, http.MethodGet, "/ws", older); res.StatusCode != http.StatusForbidden {
				t.Fatalf("the socket took an older cookie form: %d", res.StatusCode)
			}
			if !g.lines.Contains("failed auth") {
				t.Errorf("the socket's refusal left no line: %q", g.lines.String())
			}
			res := g.ask(t, http.MethodGet, "/auth/token", older)
			upgraded := findCookie(res.Cookies(), "aii_token_"+g.port)
			if res.StatusCode != http.StatusNoContent || upgraded == nil || upgraded.Value != g.accessHash() {
				t.Fatalf("the status check did not replace the older cookie: %d %v", res.StatusCode, res.Cookies())
			}
			conn, err := dialWSToken(g.addr, upgraded.Value)
			if err != nil {
				t.Fatalf("the replaced cookie was refused: %v", err)
			}
			conn.Close(websocket.StatusNormalClosure, "")
			quiet(t, g, "a browser holding the right token in an older cookie")
		})
	}

	// The cookie's name carries the port. A dashboard that came back on
	// another port is offered the old port's cookie under a name it does not
	// read: no cookie, to it.
	t.Run("after a restart on another port", func(t *testing.T) {
		g := newWalled(t, old)
		elsewhere := "aii_token_1=" + g.accessHash()
		for _, path := range []string{"/ws", "/auth/token"} {
			if res := g.ask(t, http.MethodGet, path, elsewhere); res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("GET %s with another port's cookie: %d", path, res.StatusCode)
			}
		}
		quiet(t, g, "a tab holding another port's cookie")
	})

	// The socket reads the cookie only once the Origin has passed. A page on
	// another site is refused for its Origin, whatever cookie it carries, and
	// the line says so: an Origin refusal is never blamed on the token.
	t.Run("on a page of another site", func(t *testing.T) {
		g := newWalled(t, old)
		if res := g.ask(t, http.MethodGet, "/ws", g.current("a-wrong-cookie"), "https://elsewhere.example"); res.StatusCode != http.StatusForbidden {
			t.Fatalf("a foreign Origin: %d", res.StatusCode)
		}
		if !g.lines.Contains(`origin "https://elsewhere.example"`) {
			t.Errorf("the refusal does not name the Origin it refused: %q", g.lines.String())
		}
		quiet(t, g, "a socket refused for its Origin")
	})

	t.Run("while no token is in force", func(t *testing.T) {
		g := newWalled(t, "")
		for _, path := range []string{"/ws", "/auth/token", "/p/p1/notes.md"} {
			if res := g.ask(t, http.MethodGet, path, g.current(verifierOf(old))); res.StatusCode < 400 {
				t.Fatalf("GET %s was admitted with no token in force: %d", path, res.StatusCode)
			}
		}
		quiet(t, g, "a cookie offered while no token is in force")
	})
}
