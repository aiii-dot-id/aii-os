package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func verifier(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func notPrivateFront(t *testing.T, s *Server, bound string) string {
	t.Helper()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "192.0.2.7:40000"
		r.Host = bound
		s.server.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return strings.TrimPrefix(front.URL, "http://")
}

func postRotate(t *testing.T, base, bound, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+base+"/auth/rotate", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://"+bound)
	if cookie != "" {
		_, port, _ := net.SplitHostPort(bound)
		req.Header.Set("Cookie", "aii_token_"+port+"="+cookie)
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func authStatus(t *testing.T, bound, cookie string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+bound+"/auth/token", nil)
	_, port, _ := net.SplitHostPort(bound)
	req.Header.Set("Cookie", "aii_token_"+port+"="+cookie)
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func dialPlain(bound, cookie string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": []string{"http://" + bound}}
	if cookie != "" {
		_, port, _ := net.SplitHostPort(bound)
		h.Set("Cookie", "aii_token_"+port+"="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+bound+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: h})
	return conn, err
}

// A ROTATION FROM SETTINGS KEEPS THE BROWSER THAT ASKED AND SIGNS EVERY
// OTHER ONE OUT. The answer to the rotating browser carries its new cookie;
// the old cookie opens nothing from then on; a session admitted under the
// old token is cut off; and the new cookie's session can reveal the token in
// force, as the eye does today.
func TestARotationFromSettingsKeepsTheBrowserThatAskedAndSignsEveryOtherOut(t *testing.T) {
	var s *Server
	var mu sync.Mutex
	inForce := "old-token"
	h := &WSHandler{
		DashboardToken: func() string { mu.Lock(); defer mu.Unlock(); return inForce },
		RotateDashboardToken: func() error {
			mu.Lock()
			inForce = "rotated-token"
			mu.Unlock()
			s.SetAccessToken(true, "rotated-token")
			return nil
		},
	}
	s = New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "old-token")
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	old := verifier("old-token")

	other, err := dialPlain(bound, old)
	if err != nil {
		t.Fatalf("the token in force was refused: %v", err)
	}
	defer other.CloseNow()
	waitForConns(t, s, 1)

	res := postRotate(t, bound, bound, old)
	if res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("rotation over a private connection: %d %s", res.StatusCode, body)
	}
	var fresh *http.Cookie
	for _, c := range res.Cookies() {
		if strings.HasPrefix(c.Name, "aii_token_") {
			fresh = c
		}
	}
	if fresh == nil || !fresh.HttpOnly || fresh.Value != verifier("rotated-token") {
		t.Fatalf("the rotating browser was not handed the new verifier as its HttpOnly cookie: %+v", fresh)
	}
	if got := authStatus(t, bound, old); got != http.StatusUnauthorized {
		t.Fatalf("the old cookie still signs in after the rotation: %d", got)
	}
	if got := authStatus(t, bound, fresh.Value); got != http.StatusNoContent {
		t.Fatalf("the rotating browser's new cookie is refused: %d", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := other.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("a session admitted under the old token stayed open through the rotation")
			}
			break
		}
	}
	if c, err := dialPlain(bound, old); err == nil {
		c.CloseNow()
		t.Fatal("the old cookie still opens a session")
	}
	mine, err := dialPlain(bound, fresh.Value)
	if err != nil {
		t.Fatalf("the rotating browser could not reconnect on its new cookie: %v", err)
	}
	defer mine.CloseNow()
	_ = mine.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"dashboard_token","request_id":"eye"}`))
	for {
		_, raw, err := mine.Read(ctx)
		if err != nil {
			t.Fatalf("the eye was not answered after the rotation: %v", err)
		}
		var m ServerMessage
		if json.Unmarshal(raw, &m) == nil && m.RequestID == "eye" {
			if m.DashboardToken != "rotated-token" {
				t.Fatalf("the eye showed %q after the rotation", m.DashboardToken)
			}
			break
		}
	}
}

// OFF A PRIVATE CONNECTION NOTHING ROTATES, AND NOTHING IS ASKED FIRST. The
// refusal comes before the handler — which is where the token is minted and
// config.json written — and names what to change; the policy in force is the
// one there was. A request without the token in force, and one from another
// site, are refused the same way, before anything.
func TestARotationOffAPrivateConnectionChangesNothing(t *testing.T) {
	var calls atomic.Int32
	s := New("127.0.0.1", 0, &WSHandler{RotateDashboardToken: func() error { calls.Add(1); return nil }})
	s.SetAccessToken(true, "old-token")
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	old := verifier("old-token")
	front := notPrivateFront(t, s, bound)

	res := postRotate(t, front, bound, old)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "not private") || !strings.Contains(string(body), "HTTPS") {
		t.Fatalf("a rotation off a private connection: %d %s", res.StatusCode, body)
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("a refused rotation set a cookie")
	}
	for _, refused := range []struct {
		name, cookie, origin string
		want                 int
	}{
		{"no token", "", "http://" + bound, http.StatusUnauthorized},
		{"another site", old, "http://evil.example", http.StatusForbidden},
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/rotate", nil)
		req.Header.Set("Origin", refused.origin)
		if refused.cookie != "" {
			_, port, _ := net.SplitHostPort(bound)
			req.Header.Set("Cookie", "aii_token_"+port+"="+refused.cookie)
		}
		r, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != refused.want {
			t.Fatalf("%s: %d, want %d", refused.name, r.StatusCode, refused.want)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("the rotation reached the handler %d time(s) off a private connection or without the token", calls.Load())
	}
	if s.accessHash() != old || authStatus(t, bound, old) != http.StatusNoContent {
		t.Fatal("a refused rotation changed the token in force")
	}

	// An open dashboard has no token to rotate, and says so.
	open := New("127.0.0.1", 0, &WSHandler{RotateDashboardToken: func() error { calls.Add(1); return nil }})
	openBound, err := open.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Shutdown(context.Background())
	if r := postRotate(t, openBound, openBound, ""); r.StatusCode != http.StatusConflict || calls.Load() != 0 {
		t.Fatalf("an open dashboard answered a rotation: %d, handler calls %d", r.StatusCode, calls.Load())
	}
}

// THE ROUTE IS CHOSEN OVER A PRIVATE CONNECTION ONLY. Off one, a config_set
// naming either route key is refused before the door sees it, by its request
// id, and the view that connection receives says it is not private; any other
// key still reaches the door there. Over a private connection the route keys
// reach the door as sent.
func TestTheRouteIsChosenOverAPrivateConnectionOnly(t *testing.T) {
	var mu sync.Mutex
	var seen []map[string]any
	h := &WSHandler{
		SetConfig: func(ch map[string]any) (*ConfigState, error) {
			mu.Lock()
			seen = append(seen, ch)
			mu.Unlock()
			return &ConfigState{}, nil
		},
		GetConfig: func() *ConfigState { return &ConfigState{} },
	}
	s := New("127.0.0.1", 0, h)
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	front := notPrivateFront(t, s, bound)

	ask := func(conn *websocket.Conn, id string, v map[string]any) ServerMessage {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v["request_id"] = id
		data, _ := json.Marshal(v)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var m ServerMessage
			// The configuration query is answered without its id: the
			// frame is the page's standing view, not a reply.
			if json.Unmarshal(raw, &m) == nil && (m.RequestID == id || (id == "view" && m.Type == "config")) {
				return m
			}
		}
	}
	dial := func(base string) *websocket.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws://"+base+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + bound}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return conn
	}
	relay := map[string]any{"certificate.route_mode": "relay", "certificate.relay_endpoint": "relay.example.test"}

	far := dial(front)
	if m := ask(far, "view", map[string]any{"type": "query", "query": "config"}); m.Config == nil || m.Config.SecretsOK {
		t.Fatalf("the view off a private connection does not say so: %+v", m.Config)
	}
	// The direct addresses are the route too: what the name points at.
	for i, ch := range []map[string]any{relay, {"certificate.route_mode": "direct"}, {"certificate.relay_endpoint": "relay.example.test"},
		{"certificate.direct_addresses": []any{"10.8.0.2"}}, {"certificate.direct_addresses": nil}} {
		m := ask(far, "route"+string(rune('a'+i)), map[string]any{"type": "config_set", "config": ch})
		if m.Type != "error" || !strings.Contains(m.Message, "not private") {
			t.Fatalf("a route change off a private connection: %+v", m)
		}
	}
	if m := ask(far, "other", map[string]any{"type": "config_set", "config": map[string]any{"updates.automatic": true}}); m.Type != "config" {
		t.Fatalf("a key that is not the route was refused off a private connection: %+v", m)
	}
	mu.Lock()
	if len(seen) != 1 || seen[0]["updates.automatic"] != true {
		t.Fatalf("the door saw a route change from a connection that is not private: %v", seen)
	}
	mu.Unlock()

	near := dial(bound)
	if m := ask(near, "mine", map[string]any{"type": "config_set", "config": relay}); m.Type != "config" || m.Config == nil || !m.Config.SecretsOK {
		t.Fatalf("a route change over a private connection: %+v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[1]["certificate.route_mode"] != "relay" || seen[1]["certificate.relay_endpoint"] != "relay.example.test" {
		t.Fatalf("the door did not receive the route as sent: %v", seen)
	}
}

func postAct(t *testing.T, base, bound, path, origin, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if cookie != "" {
		_, port, _ := net.SplitHostPort(bound)
		req.Header.Set("Cookie", "aii_token_"+port+"="+cookie)
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// ASKING FOR A TOKEN FROM SETTINGS KEEPS THE BROWSER THAT ASKED AND CUTS OFF
// EVERY OTHER SESSION. On a dashboard that asked for none, the answer carries
// the asking browser's cookie for the token now in force; a session admitted
// while nothing was asked is cut off, and a browser without the cookie is
// asked for the token from then on; the eye shows the new token. Asked twice,
// there is nothing to turn on.
func TestAskingForATokenKeepsTheBrowserThatAskedAndCutsEveryOtherSession(t *testing.T) {
	var s *Server
	var calls atomic.Int32
	var mu sync.Mutex
	inForce := ""
	h := &WSHandler{
		DashboardToken: func() string { mu.Lock(); defer mu.Unlock(); return inForce },
		RequireDashboardToken: func() error {
			calls.Add(1)
			mu.Lock()
			inForce = "asked-token"
			mu.Unlock()
			s.SetAccessToken(true, "asked-token")
			return nil
		},
	}
	s = New("127.0.0.1", 0, h)
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	other, err := dialPlain(bound, "")
	if err != nil {
		t.Fatalf("an open dashboard refused a session: %v", err)
	}
	defer other.CloseNow()
	waitForConns(t, s, 1)

	res := postAct(t, bound, bound, "/auth/require", "http://"+bound, "")
	if res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("asking for a token over a private connection: %d %s", res.StatusCode, body)
	}
	var fresh *http.Cookie
	for _, c := range res.Cookies() {
		if strings.HasPrefix(c.Name, "aii_token_") {
			fresh = c
		}
	}
	if fresh == nil || !fresh.HttpOnly || fresh.Value != verifier("asked-token") {
		t.Fatalf("the browser that asked was not handed the token's verifier as its HttpOnly cookie: %+v", fresh)
	}
	if !s.AccessTokenRequired() {
		t.Fatal("the server is not asking for the token")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := other.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("a session admitted while no token was asked for stayed open")
			}
			break
		}
	}
	if got := authStatus(t, bound, "not-a-cookie"); got != http.StatusUnauthorized {
		t.Fatalf("a browser without the token is not asked for it: %d", got)
	}
	if c, err := dialPlain(bound, ""); err == nil {
		c.CloseNow()
		t.Fatal("a session without the token still opens")
	}
	mine, err := dialPlain(bound, fresh.Value)
	if err != nil {
		t.Fatalf("the browser that asked could not reconnect on its cookie: %v", err)
	}
	defer mine.CloseNow()
	_ = mine.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"dashboard_token","request_id":"eye"}`))
	for {
		_, raw, err := mine.Read(ctx)
		if err != nil {
			t.Fatalf("the eye was not answered: %v", err)
		}
		var m ServerMessage
		if json.Unmarshal(raw, &m) == nil && m.RequestID == "eye" {
			if m.DashboardToken != "asked-token" {
				t.Fatalf("the eye showed %q", m.DashboardToken)
			}
			break
		}
	}
	again := postAct(t, bound, bound, "/auth/require", "http://"+bound, fresh.Value)
	body, _ := io.ReadAll(again.Body)
	if again.StatusCode != http.StatusConflict || !strings.Contains(string(body), "already asks") || calls.Load() != 1 || len(again.Cookies()) != 0 {
		t.Fatalf("asked again: %d %s, handler calls %d", again.StatusCode, body, calls.Load())
	}
}

// OFF A PRIVATE CONNECTION, FROM ANOTHER SITE, OR WITHOUT SAYING WHICH SITE,
// NOTHING IS TURNED ON. Every refusal comes before the handler — where the
// token is minted and config.json written — names what is missing, sets no
// cookie, and leaves the dashboard asking for nothing; with no handler wired
// the act is not available and says so.
func TestAskingForATokenOffAPrivateConnectionChangesNothing(t *testing.T) {
	var calls atomic.Int32
	s := New("127.0.0.1", 0, &WSHandler{RequireDashboardToken: func() error { calls.Add(1); return nil }})
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	front := notPrivateFront(t, s, bound)
	self := "http://" + bound

	for _, refused := range []struct {
		name, base, origin, site string
		want                     int
		says                     string
	}{
		{"a connection that is not private", front, self, "", http.StatusForbidden, "not private"},
		{"another site", bound, "http://evil.example", "", http.StatusForbidden, "another site"},
		{"no Origin", bound, "", "", http.StatusForbidden, "another site"},
		{"a cross-site fetch", bound, self, "cross-site", http.StatusForbidden, "another site"},
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://"+refused.base+"/auth/require", nil)
		if refused.origin != "" {
			req.Header.Set("Origin", refused.origin)
		}
		if refused.site != "" {
			req.Header.Set("Sec-Fetch-Site", refused.site)
		}
		r, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != refused.want || !strings.Contains(string(body), refused.says) || len(r.Cookies()) != 0 {
			t.Fatalf("%s: %d %s", refused.name, r.StatusCode, body)
		}
		if refused.name == "a connection that is not private" && !strings.Contains(string(body), "HTTPS") {
			t.Fatalf("the refusal does not say what to change: %s", body)
		}
	}
	if calls.Load() != 0 || s.accessRequired() {
		t.Fatalf("a refused request turned the token on: handler calls %d, required %v", calls.Load(), s.accessRequired())
	}

	bare := New("127.0.0.1", 0, &WSHandler{})
	bareBound, err := bare.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Shutdown(context.Background())
	if r := postAct(t, bareBound, bareBound, "/auth/require", "http://"+bareBound, ""); r.StatusCode != http.StatusNotFound || bare.accessRequired() {
		t.Fatalf("with no handler the act answered %d, required %v", r.StatusCode, bare.accessRequired())
	}
}
