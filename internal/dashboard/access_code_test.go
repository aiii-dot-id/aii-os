package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// codeServer is a running loopback dashboard that asks for its token.
func codeServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, "fixture-token-0123456789")
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	return s, bound
}

// signIn posts what a new device offers to the sign-in door and returns the
// status and the cookie it was given.
func signIn(t *testing.T, base, bound, offered string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+base+"/auth/token", strings.NewReader(offered))
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, port, _ := net.SplitHostPort(bound)
	for _, c := range res.Cookies() {
		if c.Name == "aii_token_"+port {
			return res.StatusCode, c.Value
		}
	}
	return res.StatusCode, ""
}

// admitted reports whether a cookie signs a browser in.
func admitted(t *testing.T, bound, cookie string) bool {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+bound+"/auth/token", nil)
	_, port, _ := net.SplitHostPort(bound)
	req.Header.Set("Cookie", "aii_token_"+port+"="+cookie)
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode == http.StatusNoContent
}

func TestAnAccessCodeSignsADeviceInOnce(t *testing.T) {
	s, bound := codeServer(t)
	st, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	// Twelve characters of the alphabet, shown in fours, and nothing kept
	// about it on the server but its hash.
	plain := strings.ReplaceAll(st.Code, "-", "")
	if len(plain) != AccessCodeLength || len(st.Code) != AccessCodeLength+2 || strings.Trim(plain, accessCodeAlphabet) != "" {
		t.Fatalf("a generated code is twelve characters of the alphabet, shown in fours: %q", st.Code)
	}
	if st.Seconds != DefaultAccessCodeSeconds || st.Own {
		t.Fatalf("the code's life as told: %+v", st)
	}
	status, cookie := signIn(t, bound, bound, st.Code)
	if status != http.StatusNoContent || cookie == "" || !admitted(t, bound, cookie) {
		t.Fatalf("the code did not sign the device in: %d, cookie %q", status, cookie)
	}
	if status, again := signIn(t, bound, bound, st.Code); status != http.StatusUnauthorized || again != "" {
		t.Fatalf("the code worked twice: %d", status)
	}
	if !admitted(t, bound, cookie) {
		t.Fatal("a refused second use signed the first device out")
	}
	// The token itself still signs in at the same door.
	if status, _ := signIn(t, bound, bound, "fixture-token-0123456789"); status != http.StatusNoContent {
		t.Fatalf("the token at its own door: %d", status)
	}
}

// IT IS TYPED AS PEOPLE TYPE: any case, with or without its hyphens or
// spaces, and with the letters the alphabet leaves out read as the digits
// they are mistaken for. A wrong code is refused and leaves the right one
// good: no count of tries is kept.
func TestAnAccessCodeIsReadAsItIsTyped(t *testing.T) {
	s, bound := codeServer(t)
	for _, typed := range []func(string) string{
		strings.ToLower,
		func(c string) string { return strings.ReplaceAll(c, "-", " ") },
		func(c string) string { return " " + strings.ReplaceAll(c, "-", "") + " " },
		func(c string) string { return strings.NewReplacer("0", "o", "1", "l").Replace(strings.ToLower(c)) },
	} {
		st, err := s.issueAccessCode(s.auth.Load(), "")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if status, _ := signIn(t, bound, bound, "0000-0000-0000"); status != http.StatusUnauthorized {
				t.Fatalf("a wrong code: %d", status)
			}
		}
		if status, cookie := signIn(t, bound, bound, typed(st.Code)); status != http.StatusNoContent || cookie == "" {
			t.Fatalf("the code typed as %q was refused after three wrong tries: %d", typed(st.Code), status)
		}
	}
}

func TestTheOperatorsOwnAccessCode(t *testing.T) {
	s, bound := codeServer(t)
	_, err := s.issueAccessCode(s.auth.Load(), "too short")
	var short *AccessCodeShort
	if !errors.As(err, &short) || short.Line != AccessCodeLength || short.Length != len("TOOSHORT") {
		t.Fatalf("an own code under twelve characters: %v", err)
	}
	if s.accessCode.Load() != nil {
		t.Fatal("a refused own code was kept")
	}
	st, err := s.issueAccessCode(s.auth.Load(), "My Own Sign-In Phrase")
	if err != nil || !st.Own || st.Code != "My Own Sign-In Phrase" {
		t.Fatalf("an own code of fifteen characters: %+v %v", st, err)
	}
	if status, _ := signIn(t, bound, bound, "my own sign-in phrase"); status != http.StatusNoContent {
		t.Fatalf("the operator's own code, typed in lower case: %d", status)
	}
	if status, _ := signIn(t, bound, bound, "My Own Sign-In Phrase"); status != http.StatusUnauthorized {
		t.Fatal("the operator's own code worked twice")
	}
}

// IT DIES when its time has passed, when another is made, when the token is
// rotated, and when the page that was given it says so — and a late close
// from one page does not erase the code another page holds.
func TestAnAccessCodeDies(t *testing.T) {
	s, bound := codeServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	issue := func() AccessCodeState {
		t.Helper()
		st, err := s.issueAccessCode(s.auth.Load(), "")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	refused := func(why string, st AccessCodeState) {
		t.Helper()
		if status, _ := signIn(t, bound, bound, st.Code); status != http.StatusUnauthorized {
			t.Fatalf("%s: the code still signed a device in (%d)", why, status)
		}
	}
	// Its time.
	s.SetAccessCodeSeconds(45)
	st := issue()
	if st.Seconds != 45 {
		t.Fatalf("the configured life is not the one told: %d", st.Seconds)
	}
	now = now.Add(46 * time.Second)
	refused("past its time", st)
	// The bounds on that time.
	for asked, want := range map[int]int{0: DefaultAccessCodeSeconds, 5: MinAccessCodeSeconds, 9999: MaxAccessCodeSeconds} {
		s.SetAccessCodeSeconds(asked)
		if got := issue().Seconds; got != want {
			t.Fatalf("a life of %d seconds was taken as %d, want %d", asked, got, want)
		}
	}
	// Another one made.
	first, second := issue(), issue()
	refused("replaced by a newer code", first)
	// A late close from the page that held the first does not touch the second.
	s.cancelAccessCode(first.ID)
	if status, _ := signIn(t, bound, bound, second.Code); status != http.StatusNoContent {
		t.Fatalf("a close naming an older code erased the newer one: %d", status)
	}
	// The page that holds it closes.
	st = issue()
	s.cancelAccessCode(st.ID)
	refused("cancelled by its own page", st)
	// The token rotated.
	st = issue()
	s.SetAccessToken(true, "another-token-0123456789")
	refused("after a rotation", st)
}

// A CODE IS MADE ONLY FOR THE POLICY ITS ASKER WAS ADMITTED UNDER. A request
// still in flight on a session a rotation is closing makes nothing, and a
// dashboard that asks for no token has no sign-in to grant.
func TestAnAccessCodeIsIssuedOnlyUnderThePolicyInForce(t *testing.T) {
	s, _ := codeServer(t)
	old := s.auth.Load()
	s.SetAccessToken(true, "another-token-0123456789")
	if _, err := s.issueAccessCode(old, ""); !errors.Is(err, ErrAccessCodeStale) {
		t.Fatalf("a session admitted under the old token was given a code for the new one: %v", err)
	}
	if s.accessCode.Load() != nil {
		t.Fatal("a refused request left a code outstanding")
	}
	s.SetAccessToken(false, "")
	if _, err := s.issueAccessCode(s.auth.Load(), ""); !errors.Is(err, ErrAccessCodeNeedsToken) {
		t.Fatalf("a code from a dashboard that asks for no token: %v", err)
	}
}

func TestARotationWhileACodeIsSpentGrantsNothingUnderTheNewToken(t *testing.T) {
	s, bound := codeServer(t)
	st, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	s.accessCodeSpent = func() { s.SetAccessToken(true, "another-token-0123456789") }
	status, cookie := signIn(t, bound, bound, st.Code)
	if status != http.StatusNoContent || cookie == "" {
		t.Fatalf("the fixture did not reach the cookie: %d", status)
	}
	if cookie == s.accessHash() || admitted(t, bound, cookie) {
		t.Fatal("a code issued under the old token signed a device in under the new one")
	}
}

func TestAnAccessCodeOfferedOverAConnectionThatIsNotPrivateIsEnded(t *testing.T) {
	s, bound := codeServer(t)
	front := notPrivateFront(t, s, bound)
	offer := func(base, code string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, "http://"+base+"/auth/token", strings.NewReader(code))
		res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		said, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return res.StatusCode, res.Header.Get("Set-Cookie"), strings.TrimSpace(string(said))
	}
	st, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	// A wrong guess in the clear ends nothing.
	if status, cookie, said := offer(front, "ZZZZ-ZZZZ-ZZZZ"); status != http.StatusUnauthorized || cookie != "" || said != "dashboard access token refused" {
		t.Fatalf("a wrong guess over a connection that is not private: %d %q %q", status, cookie, said)
	}
	if s.accessCode.Load() == nil {
		t.Fatal("a wrong guess over a connection that is not private ended the code: anyone could end one")
	}
	// The code itself, in the clear: nothing granted, the reason said, the code gone.
	if status, cookie, said := offer(front, st.Code); status != http.StatusForbidden || cookie != "" || said != ErrAccessCodeSeen.Error() {
		t.Fatalf("a code offered over a connection that is not private: %d, cookie %q, told %q; want a refusal that says it was seen and is ended", status, cookie, said)
	}
	if s.accessCode.Load() != nil {
		t.Fatal("a code that crossed the network in the clear is still outstanding")
	}
	if status, cookie, _ := offer(bound, st.Code); status != http.StatusUnauthorized || cookie != "" {
		t.Fatalf("a code that was seen in the clear still signed a device in over a private connection: %d", status)
	}
	// Another is made, and works where it should.
	next, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := signIn(t, bound, bound, next.Code); status != http.StatusNoContent {
		t.Fatalf("the next code, over a private connection: %d", status)
	}
}

func TestAnAccessCodeIsMadeOverAPrivateConnectionOnly(t *testing.T) {
	s, bound := codeServer(t)
	front := notPrivateFront(t, s, bound)
	_, port, _ := net.SplitHostPort(bound)
	ask := func(base string) ServerMessage {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws://"+base+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: http.Header{
			"Origin": []string{"http://" + bound},
			"Cookie": []string{"aii_token_" + port + "=" + verifier("fixture-token-0123456789")},
		}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"access_code","request_id":"code"}`)); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("the request for a code was not answered: %v", err)
			}
			var m ServerMessage
			if json.Unmarshal(raw, &m) == nil && m.RequestID == "code" {
				return m
			}
		}
	}
	if m := ask(front); m.Type != "error" || m.Message != ErrAccessCodeNotPrivate.Error() || m.AccessCode != nil {
		t.Fatalf("a code was asked for over a connection that is not private, and the answer was %q %q (code made: %v), want the refusal that says why", m.Type, m.Message, m.AccessCode != nil)
	}
	if s.accessCode.Load() != nil {
		t.Fatal("the refused request left a code outstanding")
	}
	if m := ask(bound); m.Type != "access_code" || m.AccessCode == nil || m.AccessCode.Code == "" {
		t.Fatalf("the machine's own page was not given a code: %+v", m)
	}
}

// THE PICTURE CARRIES THE LINK AND NOTHING ELSE: an SVG whose geometry is the
// code, with no text of the link in its markup.
func TestTheQRPictureIsMarkupWithTheLinkOnlyInItsGeometry(t *testing.T) {
	link := "https://abcdefghijklmnopqrstuvwxyz.aiios.id/auth/link#0123456789AB"
	svg := qrSVG(link)
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") || !strings.Contains(svg, "<path ") {
		t.Fatalf("not an SVG picture: %.80q", svg)
	}
	if strings.Contains(svg, "aiios") || strings.Contains(svg, "0123456789AB") || strings.Contains(svg, "<script") {
		t.Fatal("the link's text is in the picture's markup")
	}
	if qrSVG(strings.Repeat("x", 8000)) != "" {
		t.Fatal("a text too long for a code produced a picture")
	}
}

// THE PAGE AN ACCESS CODE'S LINK OPENS IS THE COMPILED ONE, served to a
// browser that is not signed in: it reads a sign-in secret out of the
// address bar, so it is never a file the identity's overlay can replace.
// It sends the code in a request body to the sign-in door and nowhere else.
func TestTheAccessCodesPageIsTheCompiledOne(t *testing.T) {
	_, bound := codeServer(t)
	for path, want := range map[string][]byte{"/auth/link": accessCodeLinkHTML, "/auth/link.js": accessCodeLinkJS} {
		res, err := http.Get("http://" + bound + path)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, len(want)+1)
		n, _ := res.Body.Read(body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || string(body[:n]) != string(want) {
			t.Fatalf("%s to a browser that is not signed in: %d, %d bytes, want the %d compiled ones", path, res.StatusCode, n, len(want))
		}
		if res.Header.Get("Content-Security-Policy") != uiCSP || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%s is not served under the page's policy, uncached and without a referrer: %v", path, res.Header)
		}
	}
	js := string(accessCodeLinkJS)
	if !strings.Contains(js, "history.replaceState") || !strings.Contains(js, "'/auth/token'") || strings.Contains(js, "?") && strings.Contains(js, "location.search") {
		t.Fatal("the link's script does not take the code out of the address bar and offer it in a request body at the sign-in door")
	}
}

// THE LINK CARRIES THE CODE WHOLE, WHATEVER IS IN IT. A person's own code
// may hold a character a link reads as its own. It went into the fragment
// as typed: "50% off" made a link the page's script could not decode, and
// a "#" was sent on as part of the code only by a browser's leniency.
func TestTheLinkCarriesAPersonsOwnCodeWhole(t *testing.T) {
	for _, own := range []string{"0123456789AB", "50% off #1 for me", "caf\u00e9 au lait, s'il vous pla\u00eet", "a/b?c=d&e+f g"} {
		link := accessCodeLink("https://abc.id.example.test/", own)
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("%q made a link that does not parse: %q: %v", own, link, err)
		}
		if u.Path != "/auth/link" || u.RawQuery != "" || u.Fragment != normalizeAccessCode(own) {
			t.Fatalf("%q: the link %q reads as path %q, query %q, code %q; want the code %q", own, link, u.Path, u.RawQuery, u.Fragment, normalizeAccessCode(own))
		}
		if raw := strings.SplitN(link, "#", 2)[1]; strings.ContainsAny(raw, " #") {
			t.Fatalf("%q went into the link unescaped: %q", own, link)
		}
	}
	// The page's script decodes it, and says so when it cannot.
	if js := string(accessCodeLinkJS); !strings.Contains(js, "decodeURIComponent") || !strings.Contains(js, "catch") {
		t.Fatal("the link's script does not decode the fragment, or lets a fragment that does not decode stop it")
	}
}

// THE SIGN-IN FIELD SHOWS A GENERATED CODE AND MASKS ANYTHING ELSE, and the
// page decides which by the code's own length and alphabet. The page holds
// its own copy of both, so this holds the copy to the original.
func TestTheSignInPagesCodeShapeIsTheHosts(t *testing.T) {
	raw, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("const ACCESS_CODE_LENGTH = %d, ACCESS_CODE_ALPHABET = '%s';", AccessCodeLength, accessCodeAlphabet)
	if !strings.Contains(string(raw), want) {
		t.Fatalf("ws.js does not read a code as the host makes one; want the line %q", want)
	}
}

func TestTheLauncherOnThisMachineIsGivenOneSignInLink(t *testing.T) {
	const token = "fixture-token-0123456789"
	s := New("127.0.0.1", 0, &WSHandler{AccessCodeSeconds: func() int { return 45 }})
	s.SetAccessToken(true, token)
	s.refusedPause = func(time.Duration) {}
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	ask := func(offered string, header map[string]string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, "http://"+bound+"/auth/launcher", strings.NewReader(offered))
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	prefix := "http://" + bound + "/auth/link#"
	codeOf := func(body []byte) (string, LauncherLink) {
		t.Helper()
		var got LauncherLink
		if err := json.Unmarshal(body, &got); err != nil || !strings.HasPrefix(got.Link, prefix) {
			t.Fatalf("the answer is not a sign-in link to this address: %q", body)
		}
		code, err := url.PathUnescape(strings.TrimPrefix(got.Link, prefix))
		if err != nil {
			t.Fatal(err)
		}
		return code, got
	}

	for _, page := range []map[string]string{{"Origin": "http://" + bound}, {"Sec-Fetch-Site": "same-origin"}} {
		if status, body := ask(token, page); status != http.StatusForbidden || !strings.Contains(string(body), "never for a page") {
			t.Fatalf("a page was given a sign-in link (%v): %d %q", page, status, body)
		}
	}
	if status, _ := ask("not-the-token", nil); status != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %d", status)
	}
	stranger := httptest.NewRequest(http.MethodPost, "http://"+bound+"/auth/launcher", strings.NewReader(token))
	stranger.RemoteAddr = "192.0.2.7:4242"
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, stranger)
	if w.Code != http.StatusForbidden {
		t.Fatalf("a peer that is not this machine: %d %q", w.Code, w.Body.String())
	}
	if s.accessCode.Load() != nil {
		t.Fatal("a refused request made a code")
	}

	status, body := ask(token, nil)
	if status != http.StatusOK {
		t.Fatalf("the launcher's request: %d %q", status, body)
	}
	code, got := codeOf(body)
	if got.Seconds != 45 || got.ExpiresAt == "" {
		t.Fatalf("the link does not live as the setting says: %+v", got)
	}
	status, cookie := signIn(t, bound, bound, code)
	if status != http.StatusNoContent || cookie == "" || !admitted(t, bound, cookie) {
		t.Fatalf("the link did not sign the browser in: %d, cookie %q", status, cookie)
	}
	if status, _ := signIn(t, bound, bound, code); status != http.StatusUnauthorized {
		t.Fatalf("the link worked twice: %d", status)
	}

	status, body = ask(token, nil)
	if status != http.StatusOK {
		t.Fatalf("a second launch: %d %q", status, body)
	}
	unspent, _ := codeOf(body)
	s.SetAccessToken(true, "fixture-token-rotated-9876543210")
	if status, _ := signIn(t, bound, bound, unspent); status != http.StatusUnauthorized {
		t.Fatalf("a link made under the old token signed a browser in after the rotation: %d", status)
	}

	s.SetAccessToken(false, "")
	outstanding := s.accessCode.Load()
	if status, body := ask("anything", nil); status != http.StatusNoContent || len(body) != 0 || s.accessCode.Load() != outstanding {
		t.Fatalf("a dashboard that asks for no token made a link: %d %q", status, body)
	}
}

func TestADeviceSignedInByAnAccessCodeApprovesScriptsAndExportsTheKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	writeOverlay(t, dir, "custom.js", "document.title = 'v1';\n")
	var approved []ApprovedScript
	rig := &backupsRig{}
	s := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks(), ApproveOverlayScript: func(a ApprovedScript) error {
		approved = append(approved, a)
		return nil
	}})
	s.SetUIOverlay(dir)
	s.SetAccessToken(true, "fixture-token-0123456789")
	bound, err := s.Start(t.TempDir()) // served over TLS, as the escrow's socket is in these tests
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	st, err := s.issueAccessCode(s.auth.Load(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(bound)
	res, err := testClient.Post("https://"+bound+"/auth/token", "text/plain", strings.NewReader(st.Code))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	signed := findCookie(res.Cookies(), "aii_token_"+port)
	if res.StatusCode != http.StatusNoContent || signed == nil {
		t.Fatalf("the code did not sign the device in: %d", res.StatusCode)
	}
	cookie := signed.Value

	digest := digestOf(t, filepath.Join(dir, "custom.js"))
	req, _ := http.NewRequest(http.MethodPost, "https://"+bound+"/overlay/scripts/approve", strings.NewReader(`{"path":"custom.js","sha256":"`+digest+`"}`))
	req.Header.Set("Origin", "https://"+bound)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "aii_token_"+port+"="+cookie)
	res, err = testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(approved) != 1 || approved[0].SHA256 != digest {
		t.Fatalf("the device signed in by a code could not approve the script: %d %q %+v", res.StatusCode, body, approved)
	}

	conn, err := dialWSToken(bound, cookie)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	const pass = "correct horse battery staple"
	if r := askBackups(t, conn, "c", ContinuityRequest{Action: "escrow_create", Passphrase: pass, Again: pass}); r.Error != "" || r.FileB64 == "" {
		t.Fatalf("the device signed in by a code could not export the keys: %+v", r)
	}
}
