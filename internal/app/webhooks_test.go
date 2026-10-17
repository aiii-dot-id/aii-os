package app

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/googleoidc"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// installWebhookPlugin adds an active plugin with a webhook declaration
// whose operation is a fake tool answering out; the arrival lands on
// the adapter's channel when it is one.
// webhookArgRecorder replaces the installed operation with one that
// keeps every argument the host handed it, as JSON — what a test needs
// when the question is what was HANDED OVER, not what was done with it.
func webhookArgRecorder(t *testing.T, a *App, pluginID, operation string) *[]string {
	t.Helper()
	name := pluginhost.ToolNameFor(pluginID, operation)
	a.toolReg.Deregister(name)
	given := &[]string{}
	if err := a.toolReg.RegisterDynamic(&fakeOp{name: name, out: `{"arrival":{"id":"m1","from":"+1","body":"hi"}}`, allArgs: given}, pluginID); err != nil {
		t.Fatal(err)
	}
	return given
}

func installWebhookPlugin(t *testing.T, a *App, pluginID string, decl pluginhost.WebhookDecl, out string, channel string) *[]string {
	t.Helper()
	calls := &[]string{}
	op := &fakeOp{name: pluginhost.ToolNameFor(pluginID, decl.Operation), out: out, calls: calls, record: true}
	if err := a.toolReg.RegisterDynamic(op, pluginID); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: pluginID, Webhooks: []pluginhost.WebhookDecl{decl}}
	if channel != "" {
		desc := "pl_" + pluginID + "_describe"
		if err := a.toolReg.RegisterHostOp(&fakeOp{name: desc, out: `{"channel":"` + channel + `"}`}, pluginID); err != nil {
			t.Fatal(err)
		}
		ap.Channel = &pluginhost.Channel{PluginID: pluginID, Describe: desc, Send: "pl_" + pluginID + "_send", Receive: "pl_" + pluginID + "_receive"}
	}
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, ap)
	a.pluginMu.Unlock()
	return calls
}

func grantSecret(t *testing.T, a *App, pluginID, setting, handle, env, value string) {
	t.Helper()
	t.Setenv(env, value)
	a.cfgMu.Lock()
	if a.cfg.Plugins.Settings == nil {
		a.cfg.Plugins.Settings = map[string]map[string]any{}
	}
	a.cfg.Plugins.Settings[pluginID] = map[string]any{setting: handle}
	if a.cfg.Plugins.Grants == nil {
		a.cfg.Plugins.Grants = map[string]broker.Grant{}
	}
	a.cfg.Plugins.Grants[pluginID] = broker.Grant{CredentialHandles: []string{handle}}
	if a.cfg.Plugins.AuthProfiles == nil {
		a.cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{}
	}
	a.cfg.Plugins.AuthProfiles[handle] = broker.AuthProfile{SecretEnv: env, Host: "api.example.test", Port: 443}
	a.cfgMu.Unlock()
}

func post(a *App, pluginID, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://identity.example.test/hooks/"+pluginID+"/"+path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.handleWebhook(rec, r, pluginID, path)
	return rec
}

// A forged webhook never reaches the operation; a valid one runs it and
// its arrival lands in the inbox once, from a stranger who does not
// wake the identity; the operation's response answers the caller.
func TestAForgedWebhookNeverReachesTheOperation(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.sms"
	decl := pluginhost.WebhookDecl{Path: "sms", Operation: "sms.inbound", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeHMACSHA256Hex, Header: "X-Signature", Prefix: "sha256=", SecretSetting: "auth_token"}, MaxBodyBytes: 64}
	calls := installWebhookPlugin(t, a, id, decl, `{"arrival":{"id":"SM1","from":"+15550001","body":"hello there"},"response":{"status":200,"content_type":"text/xml","body":"<Response/>"}}`, "sms")
	grantSecret(t, a, id, "auth_token", "twilio", "AII_TEST_WEBHOOK_SECRET", "s3cr3t")
	sign := func(body string) string {
		m := hmac.New(sha256.New, []byte("s3cr3t"))
		m.Write([]byte(body))
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	body := `{"from":"+15550001"}`

	if rec := post(a, id, "sms", body, map[string]string{"X-Signature": "sha256=deadbeef"}); rec.Code != http.StatusUnauthorized || len(*calls) != 0 {
		t.Fatalf("a forged signature: %d, calls %v", rec.Code, *calls)
	}
	if rec := post(a, id, "sms", body, nil); rec.Code != http.StatusUnauthorized || len(*calls) != 0 {
		t.Fatalf("no signature: %d", rec.Code)
	}
	if rec := post(a, id, "sms", body+"x", map[string]string{"X-Signature": sign(body)}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a signature over a different body: %d", rec.Code)
	}
	if rec := post(a, id, "sms", strings.Repeat("x", 65), map[string]string{"X-Signature": sign(strings.Repeat("x", 65))}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body over the declared ceiling: %d", rec.Code)
	}
	if rec := post(a, id, "nothing", body, map[string]string{"X-Signature": sign(body)}); rec.Code != http.StatusNotFound {
		t.Fatalf("an undeclared path: %d", rec.Code)
	}
	if rec := post(a, "org.example.absent", "sms", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown plugin: %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/hooks/"+id+"/sms", nil)
	rec := httptest.NewRecorder()
	a.handleWebhook(rec, r, id, "sms")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 0 || len(*calls) != 0 {
		t.Fatal("nothing ran and nothing landed before a valid signature")
	}

	rec = post(a, id, "sms", body, map[string]string{"X-Signature": sign(body), "Content-Type": "application/json"})
	if rec.Code != http.StatusOK || rec.Body.String() != "<Response/>" || rec.Header().Get("Content-Type") != "text/xml" {
		t.Fatalf("a valid webhook runs the operation and answers with its response: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if len(*calls) != 1 {
		t.Fatalf("the operation ran once: %v", *calls)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 1 || unseen[0].Channel != "sms" || unseen[0].Address != "+15550001" || unseen[0].Body != "hello there" {
		t.Fatalf("the arrival landed on the adapter's channel: %+v", unseen)
	}
	// A replay is not a second message.
	rec = post(a, id, "sms", body, map[string]string{"X-Signature": sign(body)})
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: %d", rec.Code)
	}
	if unseen, _ = a.store.InboundSince(0); len(unseen) != 1 {
		t.Fatalf("a replayed arrival is not a second message: %d", len(unseen))
	}
}

// The other schemes verify as their senders sign: Twilio over the URL
// and the sorted form, base64 HMAC, and a plain token; a plugin that is
// not a channel adapter lands its arrival on a channel named for it.
func TestWebhookSchemesVerifyAsTheirSendersSign(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.hooks"
	twilio := pluginhost.WebhookDecl{Path: "tw", Operation: "tw.in", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeTwilio, Header: "X-Twilio-Signature", SecretSetting: "auth_token"}}
	b64 := pluginhost.WebhookDecl{Path: "b64", Operation: "b64.in", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeHMACSHA256Base64, Header: "X-Hub", SecretSetting: "auth_token"}}
	token := pluginhost.WebhookDecl{Path: "tg", Operation: "tg.in", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-Telegram-Bot-Api-Secret-Token", SecretSetting: "auth_token"}}
	calls := installWebhookPlugin(t, a, id, twilio, `{"arrival":{"id":"m1","from":"+1555","body":"tw"}}`, "")
	for _, d := range []pluginhost.WebhookDecl{b64, token} {
		op := &fakeOp{name: pluginhost.ToolNameFor(id, d.Operation), out: `{}`, calls: calls, record: true}
		if err := a.toolReg.RegisterDynamic(op, id); err != nil {
			t.Fatal(err)
		}
	}
	a.pluginMu.Lock()
	a.plugins[len(a.plugins)-1].Webhooks = append(a.plugins[len(a.plugins)-1].Webhooks, b64, token)
	a.pluginMu.Unlock()
	grantSecret(t, a, id, "auth_token", "tw", "AII_TEST_WEBHOOK_SECRET2", "abc123")

	form := "Body=hi+there&From=%2B1555&MessageSid=SM9"
	url := "https://identity.example.test/hooks/" + id + "/tw"
	signed := url + "Body" + "hi there" + "From" + "+1555" + "MessageSid" + "SM9"
	m := hmac.New(sha1.New, []byte("abc123"))
	m.Write([]byte(signed))
	sig := base64.StdEncoding.EncodeToString(m.Sum(nil))
	rec := post(a, id, "tw", form, map[string]string{"X-Twilio-Signature": sig, "Content-Type": "application/x-www-form-urlencoded"})
	if rec.Code != http.StatusOK {
		t.Fatalf("twilio: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(a, id, "tw", form+"&x=1", map[string]string{"X-Twilio-Signature": sig, "Content-Type": "application/x-www-form-urlencoded"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("twilio with a changed form: %d", rec.Code)
	}
	unseen, _ := a.store.InboundSince(0)
	if len(unseen) != 1 || unseen[0].Channel != "hook:"+id {
		t.Fatalf("a non-adapter's arrival lands on a channel named for it: %+v", unseen)
	}

	mb := hmac.New(sha256.New, []byte("abc123"))
	mb.Write([]byte("{}"))
	if rec := post(a, id, "b64", "{}", map[string]string{"X-Hub": base64.StdEncoding.EncodeToString(mb.Sum(nil))}); rec.Code != http.StatusOK {
		t.Fatalf("base64 hmac: %d", rec.Code)
	}
	if rec := post(a, id, "tg", "{}", map[string]string{"X-Telegram-Bot-Api-Secret-Token": "abc123"}); rec.Code != http.StatusOK {
		t.Fatalf("token: %d", rec.Code)
	}
	if rec := post(a, id, "tg", "{}", map[string]string{"X-Telegram-Bot-Api-Secret-Token": "ABC123"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a token compares exactly: %d", rec.Code)
	}
	// Base64 is case-sensitive: with its letters' case flipped a signature is
	// another value, and it is refused. Only hex compares regardless of case.
	if rec := post(a, id, "b64", "{}", map[string]string{"X-Hub": flipCase(base64.StdEncoding.EncodeToString(mb.Sum(nil)))}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a base64 signature with its case flipped: %d", rec.Code)
	}
	if rec := post(a, id, "tw", form, map[string]string{"X-Twilio-Signature": flipCase(sig), "Content-Type": "application/x-www-form-urlencoded"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a twilio signature with its case flipped: %d", rec.Code)
	}
	if len(*calls) != 3 {
		t.Fatalf("three verified requests ran their operations: %v", *calls)
	}
	// No handle in the grant, no verification: refused before anything runs.
	a.cfgMu.Lock()
	a.cfg.Plugins.Grants[id] = broker.Grant{}
	a.cfgMu.Unlock()
	if rec := post(a, id, "tg", "{}", map[string]string{"X-Telegram-Bot-Api-Secret-Token": "abc123"}); rec.Code != http.StatusUnauthorized || len(*calls) != 3 {
		t.Fatalf("an unadmitted handle verifies nothing: %d", rec.Code)
	}
	_ = context.Background()
}

func TestAWebhookNeverHandsItsCredentialToThePlugin(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.tg"
	const secret = "a-reusable-bot-token"
	decl := pluginhost.WebhookDecl{Path: "tg", Operation: "tg.inbound",
		Signature:    &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: "X-Telegram-Bot-Api-Secret-Token", SecretSetting: "auth_token"},
		MaxBodyBytes: 256}
	installWebhookPlugin(t, a, id, decl, `{"arrival":{"id":"m1","from":"+1","body":"hi"}}`, "tg")
	grantSecret(t, a, id, "auth_token", "tg", "AII_TEST_TG_SECRET", secret)
	given := webhookArgRecorder(t, a, id, decl.Operation)

	if rec := post(a, id, "tg", `{"x":1}`, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token is refused: %d", rec.Code)
	}
	rec := post(a, id, "tg", `{"x":1}`, map[string]string{"X-Telegram-Bot-Api-Secret-Token": secret, "Content-Type": "application/json"})
	if rec.Code != http.StatusOK {
		t.Fatalf("a correct token runs the operation: %d", rec.Code)
	}
	// Every argument the host handed the operation, verbatim.
	if len(*given) != 1 {
		t.Fatalf("the operation was called once: %v", *given)
	}
	seen := (*given)[0]
	if strings.Contains(seen, secret) {
		t.Fatalf("THE PLUGIN WAS HANDED THE CREDENTIAL: %s", seen)
	}
	if strings.Contains(seen, "X-Telegram-Bot-Api-Secret-Token") {
		t.Fatalf("the authenticating header is not forwarded at all: %s", seen)
	}
	if !strings.Contains(seen, "application/json") {
		t.Fatalf("the ordinary headers still are: %s", seen)
	}
	if strings.Contains(seen, "_host_webhook_verified") {
		t.Fatalf("nothing host-authored is added to caller arguments — they are validated against the plugin's schema BEFORE the reserved namespace is stripped, so an extra key fails a strict schema outright: %s", seen)
	}
}

// AND THE HEADER IS EXCLUDED EVEN WHEN ITS NAME IS AN ORDINARY ONE.
// Dropping the signature header from the forwarded list was not enough:
// a declaration may name Content-Type, User-Agent or X-Request-Id as
// the header carrying its token — all are accepted declaration values —
// so the secret went straight back out under an allowed name. Header
// names are case-insensitive, so the comparison is too.
func TestAWebhookExcludesItsCredentialEvenUnderAnOrdinaryHeaderName(t *testing.T) {
	for _, hdr := range []string{"X-Request-Id", "x-request-id", "Content-Type", "User-Agent"} {
		a := liveApp(t)
		const id = "org.example.overlap"
		const secret = "secret-under-an-ordinary-name"
		decl := pluginhost.WebhookDecl{Path: "h", Operation: "h.inbound",
			Signature:    &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeToken, Header: hdr, SecretSetting: "auth_token"},
			MaxBodyBytes: 256}
		installWebhookPlugin(t, a, id, decl, `{"arrival":{"id":"m","from":"+1","body":"b"}}`, "h")
		grantSecret(t, a, id, "auth_token", "ov", "AII_TEST_OVERLAP_SECRET", secret)
		given := webhookArgRecorder(t, a, id, decl.Operation)
		if rec := post(a, id, "h", `{"x":1}`, map[string]string{hdr: secret}); rec.Code != http.StatusOK {
			t.Fatalf("%s: a correct token runs the operation: %d", hdr, rec.Code)
		}
		if len(*given) != 1 {
			t.Fatalf("%s: the operation was called once: %v", hdr, *given)
		}
		if strings.Contains((*given)[0], secret) {
			t.Fatalf("%s: THE PLUGIN WAS HANDED THE CREDENTIAL: %s", hdr, (*given)[0])
		}
	}
}

// toServer sends every request the client makes, whatever its URL, to one test
// server: Google's key endpoint, for a verifier that asks the real URL.
type toServer struct {
	base string
	next http.RoundTripper
}

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := neturl.Parse(t.base)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return t.next.RoundTrip(r)
}

// pushToken signs a Pub/Sub push's ID token as Google does, under key.
func pushToken(t *testing.T, key *rsa.PrivateKey, aud, email string, at time.Time) string {
	t.Helper()
	seg := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	signed := seg(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." +
		seg(map[string]any{"iss": "https://accounts.google.com", "aud": aud, "email": email, "email_verified": true, "iat": at.Unix(), "exp": at.Add(time.Hour).Unix()})
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// A Pub/Sub push is verified by its Google-signed token before the plugin's
// operation runs: a forged one, one for another URL or account, one when the
// host has no origin or the operator no account set — none reaches the
// operation and none is a message; a real one is, once.
func TestAGooglePushIsVerifiedBeforeItsOperationRuns(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := []byte{1, 0, 1}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kid": "k1", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	}))
	defer keys.Close()

	a := liveApp(t)
	a.googleOIDC = googleoidc.New(&http.Client{Transport: toServer{keys.URL, keys.Client().Transport}})
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	a.dashboard.SetOrigin("https://identity.example.test")
	const id, account = "com.example.mail", "push@project.iam.gserviceaccount.com"
	decl := pluginhost.WebhookDecl{Path: "notify", Operation: "mail.notify", Signature: &pluginhost.WebhookSignature{Scheme: pluginhost.SchemeGoogleOIDC, Header: "Authorization", Prefix: "Bearer ", EmailSetting: "push_account"}}
	calls := installWebhookPlugin(t, a, id, decl, `{"arrival":{"id":"m1","from":"mira@example.com","body":"hello"}}`, "mail")
	a.cfgMu.Lock()
	a.cfg.Plugins.Settings = map[string]map[string]any{id: {"push_account": account}}
	a.cfgMu.Unlock()
	url := "https://identity.example.test/hooks/" + id + "/notify"
	now := time.Now()
	bearer := func(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, h := range map[string]map[string]string{
		"no token":              nil,
		"not a bearer":          {"Authorization": "Basic abc"},
		"an empty bearer":       {"Authorization": "Bearer "},
		"garbage":               bearer("a.b.c"),
		"signed by another key": bearer(pushToken(t, other, url, account, now)),
		"for another URL":       bearer(pushToken(t, key, "https://identity.example.test/hooks/com.example.other/notify", account, now)),
		"for another account":   bearer(pushToken(t, key, url, "eve@evil.test", now)),
		"expired":               bearer(pushToken(t, key, url, account, now.Add(-3*time.Hour))),
	} {
		if rec := post(a, id, "notify", `{"message":{}}`, h); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if unseen, _ := a.store.InboundSince(0); len(*calls) != 0 || len(unseen) != 0 {
		t.Fatalf("nothing ran and nothing landed before a valid token: calls %v, %d arrivals", *calls, len(unseen))
	}

	good := bearer(pushToken(t, key, url, account, now))
	if rec := post(a, id, "notify", `{"message":{}}`, good); rec.Code != http.StatusOK || len(*calls) != 1 {
		t.Fatalf("a real push: %d, calls %v", rec.Code, *calls)
	}
	if unseen, _ := a.store.InboundSince(0); len(unseen) != 1 || unseen[0].Channel != "mail" {
		t.Fatalf("the arrival landed on the adapter's channel: %+v", unseen)
	}

	// The token does not reach the plugin: it is the sender's proof to the host.
	given := webhookArgRecorder(t, a, id, "mail.notify")
	if rec := post(a, id, "notify", `{"message":{}}`, bearer(pushToken(t, key, url, account, now))); rec.Code != http.StatusOK || len(*given) != 1 || strings.Contains((*given)[0], "Bearer") || strings.Contains((*given)[0], "Authorization") {
		t.Errorf("the plugin was handed %v (%d)", *given, rec.Code)
	}

	// Without an origin there is no URL Google could have signed for; without
	// the operator's account there is no one to accept — each refused for its
	// own cause, which the operator reads in the log, and never by luck.
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, url, nil)
		r.Header.Set("Authorization", "Bearer "+pushToken(t, key, url, account, now))
		return r
	}
	a.dashboard = nil
	if rec := post(a, id, "notify", `{"message":{}}`, good); rec.Code != http.StatusUnauthorized {
		t.Errorf("no advertised origin: %d", rec.Code)
	}
	if err := a.verifyGoogleOIDC(context.Background(), id, "notify", decl.Signature, req()); err == nil || !strings.Contains(err.Error(), "no advertised origin") {
		t.Errorf("no advertised origin: %v", err)
	}

	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	socketURL := a.dashboard.Origin() + "/hooks/" + id + "/notify"
	ran := len(*given)
	if rec := post(a, id, "notify", `{"message":{}}`, bearer(pushToken(t, key, socketURL, account, now))); rec.Code != http.StatusUnauthorized || len(*given) != ran {
		t.Errorf("a token for the socket's URL %s, no origin advertised: %d, and the operation ran %d time(s)", socketURL, rec.Code, len(*given)-ran)
	}
	if err := a.verifyGoogleOIDC(context.Background(), id, "notify", decl.Signature, req()); err == nil || !strings.Contains(err.Error(), "no advertised origin") {
		t.Errorf("a dashboard with no origin advertised: %v", err)
	}
	a.dashboard.SetOrigin("https://identity.example.test")
	a.cfgMu.Lock()
	a.cfg.Plugins.Settings[id]["push_account"] = " "
	a.cfgMu.Unlock()
	if rec := post(a, id, "notify", `{"message":{}}`, good); rec.Code != http.StatusUnauthorized {
		t.Errorf("no service account set: %d", rec.Code)
	}
	if err := a.verifyGoogleOIDC(context.Background(), id, "notify", decl.Signature, req()); err == nil || !strings.Contains(err.Error(), "names no service account") {
		t.Errorf("no service account set: %v", err)
	}
	// Even a token Google signed that carries no email is not accepted by a
	// host that was told no account.
	if rec := post(a, id, "notify", `{"message":{}}`, bearer(pushToken(t, key, url, "", now))); rec.Code != http.StatusUnauthorized {
		t.Errorf("a token with no email, and no account set: %d", rec.Code)
	}
}

// The URL a webhook is mounted at is composed once: the push's audience, and what
// a plugin is told, are the same string; no advertised origin, no URL — with no
// dashboard at all, and with one that answers at its socket and was given no
// origin to advertise.
func TestTheWebhookURLIsComposedOnce(t *testing.T) {
	a := liveApp(t)
	if got := a.webhookURL("com.example.mail", "notify"); got != "" {
		t.Fatalf("no origin, no URL: %q", got)
	}
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	if got := a.webhookURL("com.example.mail", "notify"); got != "" {
		t.Fatalf("a socket is not a public origin, so no URL: %q", got)
	}
	a.dashboard.SetOrigin("https://identity.example.test/")
	if got := a.webhookURL("com.example.mail", "notify"); got != "https://identity.example.test/hooks/com.example.mail/notify" {
		t.Fatalf("the URL: %q", got)
	}
}

// The options the host activates plugins with carry the one URL composition, so
// a plugin is told the same URL the host would verify a push against.
func TestThePluginOptionsCarryTheWebhookURL(t *testing.T) {
	a := liveApp(t)
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	a.dashboard.SetOrigin("https://identity.example.test")
	opts, err := a.buildPluginOptions(a.store, a.toolReg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.WebhookURL == nil || opts.WebhookURL("com.example.mail", "notify") != "https://identity.example.test/hooks/com.example.mail/notify" {
		t.Fatalf("the options carry no working webhook URL (set: %v)", opts.WebhookURL != nil)
	}
}

// flipCase swaps the case of every ASCII letter.
func flipCase(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = c - 'a' + 'A'
		case c >= 'A' && c <= 'Z':
			b[i] = c - 'A' + 'a'
		}
	}
	return string(b)
}
