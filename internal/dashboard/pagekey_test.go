package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const pageKeyTestToken = "a-token-of-honest-length-for-the-page-key-0123456789"

// keyServer is a serving dashboard that asks for its token, on plain
// loopback: what a browser on this machine reaches.
func keyServer(t *testing.T, h *WSHandler) (*Server, string) {
	t.Helper()
	if h == nil {
		h = &WSHandler{}
	}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, pageKeyTestToken)
	s.refusedPause = func(time.Duration) {}
	srv := httptest.NewServer(s.server.Handler)
	t.Cleanup(srv.Close)
	bound := strings.TrimPrefix(srv.URL, "http://")
	s.AllowHost(bound)
	return s, bound
}

// cookieName is the sign-in cookie's name for the dashboard at bound.
func cookieName(bound string) string {
	_, port, _ := net.SplitHostPort(bound)
	return "aii_token_" + port
}

// tlsCookieName is the sign-in cookie's name for a request that arrived over
// TLS at port: the __Host- cookie (dashboardCookieName).
func tlsCookieName(port string) string {
	return hostCookiePrefix + "aii_token_" + port
}

// signInAt signs in at bound with credential, as the sign-in form does, and
// answers the two halves its answer gave: the cookie's value and the page key.
func signInAt(t *testing.T, bound, credential string) (cookie, key string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/token", strings.NewReader(credential))
	req.Header.Set("Origin", "http://"+bound)
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("signing in: %d", res.StatusCode)
	}
	for _, c := range res.Cookies() {
		if c.Name == cookieName(bound) {
			cookie = c.Value
		}
	}
	return cookie, res.Header.Get(pageKeyHeader)
}

// ask sends one request to bound as a page there would: this site's Origin,
// the cookie when given, and the page key in its header when given.
func ask(t *testing.T, bound, method, address, cookie, key string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+bound+address, strings.NewReader("{}"))
	req.Header.Set("Origin", "http://"+bound)
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName(bound), Value: cookie})
	}
	if key != "" {
		req.Header.Set(pageKeyHeader, key)
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// dialHello opens bound's socket with the cookie and sends first what a page
// sends: the hello with key, or first when it is given ("" sends nothing).
func dialHello(t *testing.T, bound, cookie, key string, first ...string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{"Origin": []string{"http://" + bound}}
	if cookie != "" {
		h.Set("Cookie", cookieName(bound)+"="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+bound+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("the handshake was refused: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	msg := `{"type":"hello","page_key":"` + key + `"}`
	if len(first) > 0 {
		msg = first[0]
	}
	if msg == "" {
		return conn
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("the first message: %v", err)
	}
	return conn
}

// firstFrame reads the socket's next frame, or how it was closed.
func firstFrame(conn *websocket.Conn) (frame string, closed websocket.StatusCode, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return "", ce.Code, ce.Reason
		}
		return "", -1, err.Error()
	}
	return string(data), 0, ""
}

// THE COOKIE ALONE OPENS NO PRIVATE ROUTE. For every route of the table that
// asks for the page key, a request carrying a valid cookie and this site's
// Origin, and no key, is refused before its handler, and says the page key
// is what it lacked; the same request with the key passes the wall. The
// fault this closes: a program on another port of this machine, given the
// cookie by one link opened in the operator's browser, was the operator.
func TestTheCookieAloneOpensNoPrivateRoute(t *testing.T) {
	s, bound := keyServer(t, nil)
	cookie, key := signInAt(t, bound, pageKeyTestToken)
	if cookie == "" || !pageKeyValid(s.auth.Load(), key) {
		t.Fatalf("the sign-in did not answer both halves: cookie %q, key %q", cookie, key)
	}
	asked := 0
	for _, rt := range s.routes() {
		if rt.asks != reachPage && rt.asks != reachBrowser {
			continue
		}
		asked++
		method, address, _ := strings.Cut(rt.pattern, " ")
		address = strings.NewReplacer("{id}", "an-id", "{path...}", "a-file").Replace(address)
		refused := ask(t, bound, method, address, cookie, "")
		if refused.StatusCode != http.StatusUnauthorized || refused.Header.Get(missingHeader) != string(missingPageKey) {
			t.Errorf("%s with the cookie alone: %d, %s %q; want 401, the page key named", rt.pattern, refused.StatusCode, missingHeader, refused.Header.Get(missingHeader))
		}
		if passed := ask(t, bound, method, address, cookie, key); passed.StatusCode == http.StatusUnauthorized {
			t.Errorf("%s with both halves was refused at the wall", rt.pattern)
		}
	}
	if asked < 20 {
		t.Fatalf("only %d routes ask for the page key; the table lost some", asked)
	}
}

// THE PAGE KEY ALONE OPENS NOTHING EITHER. Without the cookie a key is
// refused on every route that asks for both, and on the socket's handshake.
func TestThePageKeyAloneOpensNothing(t *testing.T) {
	s, bound := keyServer(t, nil)
	_, key := signInAt(t, bound, pageKeyTestToken)
	for _, rt := range s.routes() {
		if rt.asks != reachPage && rt.asks != reachBrowser {
			continue
		}
		method, address, _ := strings.Cut(rt.pattern, " ")
		address = strings.NewReplacer("{id}", "an-id", "{path...}", "a-file").Replace(address)
		if res := ask(t, bound, method, address, "", key); res.StatusCode != http.StatusUnauthorized || res.Header.Get(missingHeader) != string(missingCookie) {
			t.Errorf("%s with the key alone: %d, %s %q", rt.pattern, res.StatusCode, missingHeader, res.Header.Get(missingHeader))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if conn, _, err := websocket.Dial(ctx, "ws://"+bound+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + bound}}}); err == nil {
		conn.CloseNow()
		t.Fatal("a socket with no cookie was accepted")
	}
}

// THE SOCKET ASKS FOR THE PAGE KEY IN ITS FIRST MESSAGE. With a valid
// cookie, a first message that is a hello with no key, a wrong key, or a key
// made under a token rotated since is answered by a close with 4401 before
// anything else is sent; a wrong key is slowed and written down as a guess, a
// missing one is not. A page from before the page key, whose first message is
// a query, is told to reload and closed. With both halves the session begins.
func TestTheSocketAsksForThePageKeyInItsFirstMessage(t *testing.T) {
	s, bound := keyServer(t, nil)
	var slowed int
	s.refusedPause = func(time.Duration) { slowed++ }
	cookie, key := signInAt(t, bound, pageKeyTestToken)

	other := New("127.0.0.1", 0, nil)
	other.SetAccessToken(true, "another-token-of-honest-length-0123456789")
	for _, c := range []struct {
		name, key string
		slows     int
	}{
		{"no key", "", 0},
		{"a wrong key", "pk1.AAAA", 1},
		{"a key under another token", mintPageKey(other.auth.Load()), 1},
	} {
		before := slowed
		frame, code, reason := firstFrame(dialHello(t, bound, cookie, c.key))
		if frame != "" || code != closeSignIn || reason != refusedSocket {
			t.Errorf("%s: frame %q, close %d %q; want only a close with %d", c.name, frame, code, reason, closeSignIn)
		}
		if slowed-before != c.slows {
			t.Errorf("%s: slowed %d times, want %d", c.name, slowed-before, c.slows)
		}
	}

	// A page from before the page key sends a query first; one that asked
	// for a channel says nothing until the identity speaks, and is answered
	// the same when a stranger's body's time has passed.
	s.strangerBodyWait = 300 * time.Millisecond
	for _, first := range []string{`{"type":"query","query":"providers","request_id":"1"}`, ""} {
		earlier := dialHello(t, bound, cookie, "", first)
		frame, _, _ := firstFrame(earlier)
		var told ServerMessage
		if json.Unmarshal([]byte(frame), &told) != nil || told.Type != "overlay_changed" || len(told.Paths) != 1 || told.Paths[0] != "/ws.js" || told.Token == 0 {
			t.Fatalf("a page from before the page key (first message %q) was not told to reload: %q", first, frame)
		}
		if more, code, _ := firstFrame(earlier); more != "" || code != closeSignIn {
			t.Fatalf("after the reload frame (first message %q): %q, close %d; want the close", first, more, code)
		}
	}

	// Both halves: the session begins, and answers.
	if got := answered(t, dialHello(t, bound, cookie, key)); got != "probe" {
		t.Fatalf("both halves were not answered as a session: %q", got)
	}
	if !s.PageOpen() {
		t.Fatal("the signed-in socket was not registered")
	}
}

// EVERY SIGN-IN GIVES A PAGE KEY: the token at the door, an access code (a
// launcher's link spends one there too), and Settings' rotation and
// requirement, each answering a key the socket and a route then take. A
// browser holding one of the older cookie forms is given no key and is
// asked to sign in: those forms no longer admit, and the unscoped one is
// cleared.
func TestEverySignInGivesAPageKey(t *testing.T) {
	works := func(t *testing.T, s *Server, bound, cookie, key string) {
		t.Helper()
		if !pageKeyValid(s.auth.Load(), key) {
			t.Fatalf("no valid page key was answered: %q", key)
		}
		if res := ask(t, bound, http.MethodGet, "/auth/token", cookie, key); res.StatusCode != http.StatusNoContent {
			t.Fatalf("the status refused both halves: %d", res.StatusCode)
		}
		if got := answered(t, dialHello(t, bound, cookie, key)); got != "probe" {
			t.Fatalf("the socket did not answer both halves as a session: %q", got)
		}
	}
	t.Run("the token", func(t *testing.T) {
		s, bound := keyServer(t, nil)
		cookie, key := signInAt(t, bound, pageKeyTestToken)
		works(t, s, bound, cookie, key)
	})
	t.Run("an access code", func(t *testing.T) {
		s, bound := keyServer(t, nil)
		st, err := s.issueAccessCode(s.auth.Load(), "", false)
		if err != nil {
			t.Fatal(err)
		}
		cookie, key := signInAt(t, bound, st.Code)
		works(t, s, bound, cookie, key)
	})
	for _, require := range []bool{false, true} {
		name := "a rotation"
		if require {
			name = "turning the token on"
		}
		t.Run(name, func(t *testing.T) {
			var s *Server
			h := &WSHandler{
				RotateDashboardToken:  func() error { s.SetAccessToken(true, "the-token-after-the-act-0123456789"); return nil },
				RequireDashboardToken: func() error { s.SetAccessToken(true, "the-token-after-the-act-0123456789"); return nil },
			}
			s2, bound := keyServer(t, h)
			s = s2
			cookie, key := signInAt(t, bound, pageKeyTestToken)
			path := "/auth/rotate"
			if require {
				s.SetAccessToken(false, "")
				cookie, key, path = "", "", "/auth/require"
			}
			res := ask(t, bound, http.MethodPost, path, cookie, key)
			if res.StatusCode != http.StatusNoContent {
				t.Fatalf("%s: %d", path, res.StatusCode)
			}
			var fresh string
			for _, c := range res.Cookies() {
				if c.Name == cookieName(bound) {
					fresh = c.Value
				}
			}
			works(t, s, bound, fresh, res.Header.Get(pageKeyHeader))
			if key != "" && pageKeyValid(s.auth.Load(), key) {
				t.Fatal("the key from before the rotation still opens")
			}
		})
	}
	t.Run("the older cookie forms", func(t *testing.T) {
		_, bound := keyServer(t, nil)
		for _, name := range []string{cookieName(bound), legacyDashboardCookieName} {
			req, _ := http.NewRequest(http.MethodGet, "http://"+bound+"/auth/token", nil)
			req.AddCookie(&http.Cookie{Name: name, Value: pageKeyTestToken})
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized || res.Header.Get(pageKeyHeader) != "" || len(res.Cookies()) != 0 {
				t.Errorf("%s holding the raw token: %d, key %q, cookies %v; want 401, no key and no cookie", name, res.StatusCode, res.Header.Get(pageKeyHeader), res.Cookies())
			}
		}
		// The sign-in that follows clears the unscoped one.
		req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/token", strings.NewReader(pageKeyTestToken))
		req.AddCookie(&http.Cookie{Name: legacyDashboardCookieName, Value: pageKeyTestToken})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		cleared := false
		for _, c := range res.Cookies() {
			cleared = cleared || (c.Name == legacyDashboardCookieName && c.MaxAge < 0)
		}
		if res.StatusCode != http.StatusNoContent || !cleared {
			t.Errorf("the sign-in after an unscoped cookie: %d, cleared %v", res.StatusCode, cleared)
		}
	})
}

// THE PAGE KEY OUTLIVES A RESTART AND NOT A ROTATION. Nothing is kept per
// sign-in: a second server built from the same token takes a key the first
// made, and one built after a rotation takes none.
func TestThePageKeyOutlivesARestartAndNotARotation(t *testing.T) {
	first := New("127.0.0.1", 0, nil)
	first.SetAccessToken(true, pageKeyTestToken)
	key := mintPageKey(first.auth.Load())
	again := New("127.0.0.1", 0, nil)
	again.SetAccessToken(true, pageKeyTestToken)
	if !pageKeyValid(again.auth.Load(), key) {
		t.Fatal("a restart signed the page out")
	}
	rotated := New("127.0.0.1", 0, nil)
	rotated.SetAccessToken(true, pageKeyTestToken+"-rotated")
	if pageKeyValid(rotated.auth.Load(), key) {
		t.Fatal("a rotation kept the page key")
	}
	if mintPageKey(first.auth.Load()) == key {
		t.Fatal("two sign-ins were given one key")
	}
}

// THE COOKIE CANNOT STAND IN FOR THE PAGE SECRET. HMAC hashes a key longer
// than its 64-byte block, and that hash is the cookie's value: had the
// secret been HMAC(token, label), a token of 100 bytes, or the 4096 the door
// accepts, would let whoever holds the cookie make keys. With the secret
// HKDF derives, a key made from the cookie's value is refused.
func TestTheCookieCannotStandInForThePageSecret(t *testing.T) {
	for _, n := range []int{100, AccessTokenMaxBytes} {
		token := strings.Repeat("k", n)
		s := New("127.0.0.1", 0, nil)
		s.SetAccessToken(true, token)
		p := s.auth.Load()
		cookie := sha256.Sum256([]byte(token)) // what the cookie's hex value names
		mac := hmac.New(sha256.New, cookie[:])
		mac.Write([]byte(pageKeyInfo))
		forged := &accessPolicy{required: true, hash: p.hash, secret: mac.Sum(nil)}
		if hmac.Equal(forged.secret, p.secret) {
			t.Fatalf("a %d-byte token: the page secret is an HMAC keyed by the cookie's value", n)
		}
		if pageKeyValid(p, mintPageKey(forged)) {
			t.Fatalf("a %d-byte token: a key made from the cookie's value was taken", n)
		}
	}
}

// ticketServer is a dashboard that serves one picture of the world and one
// of a project, and a database export, behind the token.
func ticketServer(t *testing.T) (*Server, string) {
	t.Helper()
	world := t.TempDir()
	if err := os.WriteFile(filepath.Join(world, "proof.png"), []byte("\x89PNG\r\n\x1a\nimage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(world, "other.png"), []byte("\x89PNG\r\n\x1a\nother"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &WSHandler{
		AdmitFile:      func(p string) (string, string, bool) { return world, p, p == filepath.Base(p) },
		GetProjectRoot: func(id string) (string, bool) { return world, id == "proj" },
	}
	return keyServer(t, h)
}

// ticketFor asks bound for a ticket for one request, with both halves.
func ticketAt(t *testing.T, bound, cookie, key, method, address string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/ticket", strings.NewReader(`{"method":"`+method+`","address":"`+address+`"}`))
	req.Header.Set("Origin", "http://"+bound)
	req.AddCookie(&http.Cookie{Name: cookieName(bound), Value: cookie})
	req.Header.Set(pageKeyHeader, key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got struct{ Ticket string }
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&got) != nil || got.Ticket == "" {
		t.Fatalf("no ticket for %s %s: %d", method, address, res.StatusCode)
	}
	return address + map[bool]string{true: "&", false: "?"}[strings.Contains(address, "?")] + "ticket=" + url.QueryEscape(got.Ticket)
}

// A TICKET OPENS ITS ONE REQUEST, ONCE. For a picture of the world, one of a
// project and the database export, a ticket admits the request it was minted
// for with the cookie beside it, and is refused when used again; one for one
// file opens no other; one past its life, one minted before a rotation and
// one without the cookie open nothing; and at the bound the oldest is
// dropped.
func TestATicketOpensItsRequestOnce(t *testing.T) {
	s, bound := ticketServer(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	cookie, key := signInAt(t, bound, pageKeyTestToken)
	for _, c := range []struct{ method, address string }{
		{http.MethodGet, "/w?p=proof.png"},
		{http.MethodGet, "/p/proj/proof.png"},
		{http.MethodPost, "/database/export"},
	} {
		ticketed := ticketAt(t, bound, cookie, key, c.method, c.address)
		if res := ask(t, bound, c.method, ticketed, cookie, ""); res.StatusCode == http.StatusUnauthorized {
			t.Errorf("%s: the ticket did not open its request", c.address)
		}
		if res := ask(t, bound, c.method, ticketed, cookie, ""); res.StatusCode != http.StatusUnauthorized || res.Header.Get(missingHeader) != string(missingPageKey) {
			t.Errorf("%s: a spent ticket opened it again (%d)", c.address, res.StatusCode)
		}
		if res := ask(t, bound, c.method, ticketAt(t, bound, cookie, key, c.method, c.address), "", ""); res.StatusCode != http.StatusUnauthorized || res.Header.Get(missingHeader) != string(missingCookie) {
			t.Errorf("%s: a ticket without the cookie opened it (%d)", c.address, res.StatusCode)
		}
	}
	// One file's ticket opens no other.
	other := strings.Replace(ticketAt(t, bound, cookie, key, http.MethodGet, "/w?p=proof.png"), "proof.png", "other.png", 1)
	if res := ask(t, bound, http.MethodGet, other, cookie, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a ticket for one file opened another (%d)", res.StatusCode)
	}
	// Past its life.
	late := ticketAt(t, bound, cookie, key, http.MethodGet, "/w?p=proof.png")
	clock = clock.Add(DefaultPageTicketSeconds * time.Second)
	if res := ask(t, bound, http.MethodGet, late, cookie, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a ticket past its life opened its file (%d)", res.StatusCode)
	}
	// Minted before a rotation (the cookie rotated with it).
	before := ticketAt(t, bound, cookie, key, http.MethodGet, "/w?p=proof.png")
	s.SetAccessToken(true, pageKeyTestToken+"-rotated")
	fresh, freshKey := signInAt(t, bound, pageKeyTestToken+"-rotated")
	if res := ask(t, bound, http.MethodGet, before, fresh, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a ticket minted before a rotation opened its file (%d)", res.StatusCode)
	}
	// At the bound the oldest is dropped.
	first := ticketAt(t, bound, fresh, freshKey, http.MethodGet, "/w?p=proof.png")
	for i := 0; i < DefaultPageTickets; i++ {
		clock = clock.Add(time.Millisecond)
		ticketAt(t, bound, fresh, freshKey, http.MethodGet, "/w?p=other.png")
	}
	if res := ask(t, bound, http.MethodGet, first, fresh, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("the oldest ticket was kept past the bound (%d)", res.StatusCode)
	}
}

// A TICKET IS MINTED ONLY FOR BOTH HALVES, AND ONLY FOR A ROUTE THAT TAKES
// ONE: the cookie alone gets none, and an address a page reaches with its key
// (the record, the socket's door, the sign-in) is refused, so a ticket
// widens nothing.
func TestATicketIsMintedOnlyForBothHalvesAndOnlyWhereOneIsTaken(t *testing.T) {
	_, bound := ticketServer(t)
	cookie, key := signInAt(t, bound, pageKeyTestToken)
	req := func(cookie, key, method, address string) *http.Response {
		r, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/ticket", strings.NewReader(`{"method":"`+method+`","address":"`+address+`"}`))
		r.Header.Set("Origin", "http://"+bound)
		r.AddCookie(&http.Cookie{Name: cookieName(bound), Value: cookie})
		if key != "" {
			r.Header.Set(pageKeyHeader, key)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := req(cookie, "", http.MethodGet, "/w?p=proof.png"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the cookie alone was given a ticket: %d", res.StatusCode)
	}
	for _, c := range []struct{ method, address string }{
		{http.MethodGet, "/record"},
		{http.MethodPost, "/upload"},
		{http.MethodGet, "/ws"},
		{http.MethodPost, "/auth/rotate"},
		{http.MethodGet, "/w/../record"},
		{http.MethodPost, "/w?p=proof.png"},
		{http.MethodGet, "http://elsewhere.example/w?p=proof.png"},
	} {
		if res := req(cookie, key, c.method, c.address); res.StatusCode != http.StatusBadRequest {
			t.Errorf("a ticket was minted for %s %s: %d", c.method, c.address, res.StatusCode)
		}
	}
}

// EVERY ROUTE DECLARES WHAT IT ASKS. The mux is built from the route table
// alone, each pattern with its reach, so a route added later says what it
// asks or the tree is red: no file of the package registers a route
// anywhere else, and every pattern in the table has a reach this file knows.
func TestEveryRouteDeclaresItsReach(t *testing.T) {
	s := New("127.0.0.1", 0, nil)
	known := map[reach]bool{reachPage: true, reachBrowser: true, reachMinted: true, reachDoor: true, reachOutside: true, reachFrame: true, reachSection: true, reachSocket: true, reachHook: true}
	for _, rt := range s.routes() {
		if !known[rt.asks] || rt.serve == nil {
			t.Errorf("%s declares no reach this test knows (%d)", rt.pattern, rt.asks)
		}
		if s.reaches[rt.pattern] != rt.asks {
			t.Errorf("%s is not registered with its reach", rt.pattern)
		}
	}
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(body), ".HandleFunc(") + strings.Count(string(body), ".Handle("); n != map[bool]int{true: 1, false: 0}[f == "server.go"] {
			t.Errorf("%s registers %d route(s) outside the route table's one loop", f, n)
		}
	}
}

func TestTheLinkPageKeepsThePageKeyWhereThePageReadsIt(t *testing.T) {
	page, err := staticFS.ReadFile("static/pagekey.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"static/pagekey.js": page, "linkpage/link.js": accessCodeLinkJS} {
		for _, want := range []string{"'aii.signin'", "'" + pageKeyHeader + "'"} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s does not name %s", name, want)
			}
		}
	}
}
