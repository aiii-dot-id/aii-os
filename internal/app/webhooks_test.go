package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
// .
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
		a.cfg.Plugins.Settings = map[string]map[string]interface{}{}
	}
	a.cfg.Plugins.Settings[pluginID] = map[string]interface{}{setting: handle}
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

// .
// .
// .
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
	// .
	rec = post(a, id, "sms", body, map[string]string{"X-Signature": sign(body)})
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: %d", rec.Code)
	}
	if unseen, _ = a.store.InboundSince(0); len(unseen) != 1 {
		t.Fatalf("a replayed arrival is not a second message: %d", len(unseen))
	}
}

// .
// .
// .
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
	if len(*calls) != 3 {
		t.Fatalf("three verified requests ran their operations: %v", *calls)
	}
	// .
	a.cfgMu.Lock()
	a.cfg.Plugins.Grants[id] = broker.Grant{}
	a.cfgMu.Unlock()
	if rec := post(a, id, "tg", "{}", map[string]string{"X-Telegram-Bot-Api-Secret-Token": "abc123"}); rec.Code != http.StatusUnauthorized || len(*calls) != 3 {
		t.Fatalf("an unadmitted handle verifies nothing: %d", rec.Code)
	}
	_ = context.Background()
}

// .
// .
// .
// .
// .
// .
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
	// .
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

// .
// .
// .
// .
// .
// .
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
