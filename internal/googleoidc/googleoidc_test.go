package googleoidc

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	audience = "https://identity.example.test/hooks/com.aiii.gmail/notify"
	account  = "push@project.iam.gserviceaccount.com"
	kid      = "key-1"
)

var t0 = time.Unix(1_790_000_000, 0)

// rsaKey is generated once: key generation is the slow part of these tests.
var rsaKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

var otherKey = sync.OnceValue(func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
})

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwk(id string, k *rsa.PublicKey) map[string]any {
	e := []byte{byte(k.E >> 16), byte(k.E >> 8), byte(k.E)}
	return map[string]any{"kid": id, "kty": "RSA", "alg": "RS256", "use": "sig", "n": b64(k.N.Bytes()), "e": b64(e)}
}

// google is the key endpoint: what it serves, how it is cached, and how often
// it was asked.
type google struct {
	srv     *httptest.Server
	fetches atomic.Int32
	status  atomic.Int32
	body    atomic.Value // string
	cache   atomic.Value // string
}

func newGoogle(t *testing.T) *google {
	g := &google{}
	g.status.Store(200)
	g.body.Store(marshal(map[string]any{"keys": []any{jwk(kid, &rsaKey().PublicKey)}}))
	g.cache.Store("public, max-age=3600")
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.fetches.Add(1)
		w.Header().Set("Cache-Control", g.cache.Load().(string))
		w.WriteHeader(int(g.status.Load()))
		_, _ = w.Write([]byte(g.body.Load().(string)))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func marshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// clock is a Verifier's time, moved by the test.
type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time          { return time.Unix(c.t.Load(), 0) }
func (c *clock) advance(d time.Duration) { c.t.Add(int64(d / time.Second)) }

func rig(t *testing.T) (*Verifier, *google, *clock) {
	g := newGoogle(t)
	c := &clock{}
	c.t.Store(t0.Unix())
	v := New(g.srv.Client())
	v.url, v.now = g.srv.URL, c.now
	return v, g, c
}

// claims is a token's payload; change fields to build a refusal.
func claims(change func(map[string]any)) map[string]any {
	m := map[string]any{"iss": "https://accounts.google.com", "aud": audience, "email": account, "email_verified": true,
		"iat": t0.Unix() - 10, "exp": t0.Unix() + 3590}
	if change != nil {
		change(m)
	}
	return m
}

func header(alg, id string) map[string]any {
	h := map[string]any{"alg": alg, "typ": "JWT"}
	if id != "" {
		h["kid"] = id
	}
	return h
}

// token signs claims under key with RS256, as Google does.
func token(k *rsa.PrivateKey, h, c map[string]any) string {
	signed := b64([]byte(marshal(h))) + "." + b64([]byte(marshal(c)))
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signed + "." + b64(sig)
}

func good(change func(map[string]any)) string {
	return token(rsaKey(), header("RS256", kid), claims(change))
}

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("not a typed refusal: %v", err)
	}
	return r.Reason
}

func TestAGoogleTokenForThisWebhookIsAccepted(t *testing.T) {
	v, g, _ := rig(t)
	if err := v.Verify(context.Background(), good(nil), audience, account); err != nil {
		t.Fatalf("a token Google signed for this webhook: %v", err)
	}
	if err := v.Verify(context.Background(), good(nil), audience, strings.ToUpper(account)); err != nil {
		t.Errorf("an email that differs only in case: %v", err)
	}
	if n := g.fetches.Load(); n != 1 {
		t.Errorf("two verifications fetched Google's keys %d times", n)
	}
}

// Every rule, refused by its own reason: a token that fails any one of them
// never reaches the plugin.
func TestATokenThatBreaksARuleIsRefusedByItsReason(t *testing.T) {
	v, _, _ := rig(t)
	sign := func(h, c map[string]any) string { return token(rsaKey(), h, c) }
	hmacTok := func() string { // the key-confusion token: HS256 keyed by the public modulus
		signed := b64([]byte(marshal(header("HS256", kid)))) + "." + b64([]byte(marshal(claims(nil))))
		m := hmac.New(sha256.New, rsaKey().PublicKey.N.Bytes())
		m.Write([]byte(signed))
		return signed + "." + b64(m.Sum(nil))
	}
	forged := good(nil)
	parts := strings.Split(forged, ".")
	swapped := parts[0] + "." + b64([]byte(marshal(claims(func(m map[string]any) { m["email"] = "eve@evil.test" })))) + "." + parts[2]

	for _, tc := range []struct {
		name string
		tok  string
		want Reason
	}{
		{"two segments", "a.b", ReasonMalformed},
		{"four segments", "a.b.c.d", ReasonMalformed},
		{"a header that is not base64", "!!!." + parts[1] + "." + parts[2], ReasonMalformed},
		{"a header that is not JSON", b64([]byte("nope")) + "." + parts[1] + "." + parts[2], ReasonMalformed},
		{"over the size bound, though Google signed it", good(func(m map[string]any) { m["pad"] = strings.Repeat("x", maxTokenBytes) }), ReasonMalformed},
		{"algorithm none", b64([]byte(marshal(header("none", kid)))) + "." + parts[1] + ".", ReasonAlgorithm},
		{"HS256 keyed by the public key", hmacTok(), ReasonAlgorithm},
		{"no key named", sign(header("RS256", ""), claims(nil)), ReasonMalformed},
		{"a payload changed after signing", swapped, ReasonSignature},
		{"signed by another key under Google's kid", token(otherKey(), header("RS256", kid), claims(nil)), ReasonSignature},
		{"another issuer", sign(header("RS256", kid), claims(func(m map[string]any) { m["iss"] = "https://evil.test" })), ReasonIssuer},
		{"another audience", sign(header("RS256", kid), claims(func(m map[string]any) { m["aud"] = "https://elsewhere.test/hooks/x/y" })), ReasonAudience},
		{"another service account", sign(header("RS256", kid), claims(func(m map[string]any) { m["email"] = "eve@evil.test" })), ReasonEmail},
		{"an email Google did not verify", sign(header("RS256", kid), claims(func(m map[string]any) { m["email_verified"] = false })), ReasonEmail},
		{"no email", sign(header("RS256", kid), claims(func(m map[string]any) { delete(m, "email") })), ReasonEmail},
		{"expired past the skew", sign(header("RS256", kid), claims(func(m map[string]any) { m["exp"] = t0.Unix() - 61 })), ReasonExpired},
		{"no expiry", sign(header("RS256", kid), claims(func(m map[string]any) { delete(m, "exp") })), ReasonExpired},
		{"issued in the future", sign(header("RS256", kid), claims(func(m map[string]any) { m["iat"] = t0.Unix() + 61 })), ReasonNotYetValid},
		{"not before the future", sign(header("RS256", kid), claims(func(m map[string]any) { m["nbf"] = t0.Unix() + 61 })), ReasonNotYetValid},
	} {
		if got := reasonOf(t, v.Verify(context.Background(), tc.tok, audience, account)); got != tc.want {
			t.Errorf("%s: refused as %q, want %q", tc.name, got, tc.want)
		}
	}
	// A caller that names no audience or no account accepts no one — not even
	// a token Google signed that carries none.
	blank := token(rsaKey(), header("RS256", kid), claims(func(m map[string]any) { m["aud"], m["email"] = "", "" }))
	if got := reasonOf(t, v.Verify(context.Background(), blank, "", account)); got != ReasonAudience {
		t.Errorf("no audience named: %q", got)
	}
	if got := reasonOf(t, v.Verify(context.Background(), blank, audience, "")); got != ReasonEmail {
		t.Errorf("no service account named: %q", got)
	}
	// The skew is a minute either way.
	for name, change := range map[string]func(map[string]any){
		"expired inside the skew":  func(m map[string]any) { m["exp"] = t0.Unix() - 30 },
		"issued inside the skew":   func(m map[string]any) { m["iat"] = t0.Unix() + 30 },
		"not before inside skew":   func(m map[string]any) { m["nbf"] = t0.Unix() + 30 },
		"the other issuer spelled": func(m map[string]any) { m["iss"] = "accounts.google.com" },
	} {
		if err := v.Verify(context.Background(), sign(header("RS256", kid), claims(change)), audience, account); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Google's keys are the boundary's one dependency, and it fails closed.
func TestGooglesKeysAreFetchedBoundedAndFailClosed(t *testing.T) {
	v, g, c := rig(t)
	ctx := context.Background()

	// A kid Google does not publish: refused, and asked about once a minute
	// however many arrive.
	for i := 0; i < 5; i++ {
		tok := token(rsaKey(), header("RS256", "forged-"+string(rune('a'+i))), claims(nil))
		if got := reasonOf(t, v.Verify(ctx, tok, audience, account)); got != ReasonKeyUnknown {
			t.Fatalf("a forged kid: %q", got)
		}
	}
	if n := g.fetches.Load(); n != 1 {
		t.Errorf("five forged kids fetched Google's keys %d times", n)
	}
	if err := v.Verify(ctx, good(nil), audience, account); err != nil || g.fetches.Load() != 1 {
		t.Errorf("a real key after them: %v after %d fetches", err, g.fetches.Load())
	}

	// The keys are kept for max-age, then fetched again.
	c.advance(59 * time.Minute)
	if err := v.Verify(ctx, good(func(m map[string]any) { m["iat"], m["exp"] = c.now().Unix(), c.now().Unix()+3600 }), audience, account); err != nil || g.fetches.Load() != 1 {
		t.Errorf("inside max-age: %v after %d fetches", err, g.fetches.Load())
	}
	c.advance(2 * time.Minute)
	if err := v.Verify(ctx, good(func(m map[string]any) { m["iat"], m["exp"] = c.now().Unix(), c.now().Unix()+3600 }), audience, account); err != nil || g.fetches.Load() != 2 {
		t.Errorf("past max-age: %v after %d fetches", err, g.fetches.Load())
	}

	// A fetch that fails refuses the token — and expired keys are not used.
	c.advance(2 * time.Hour)
	g.status.Store(500)
	fresh := func(m map[string]any) { m["iat"], m["exp"] = c.now().Unix(), c.now().Unix()+3600 }
	if got := reasonOf(t, v.Verify(ctx, good(fresh), audience, account)); got != ReasonKeysUnavailable {
		t.Errorf("Google answering 500: %q", got)
	}
	n := g.fetches.Load()
	if got := reasonOf(t, v.Verify(ctx, good(fresh), audience, account)); got != ReasonKeysUnavailable || g.fetches.Load() != n {
		t.Errorf("a second try inside the minute: %q after %d more fetches", got, g.fetches.Load()-n)
	}
	c.advance(61 * time.Second)
	g.status.Store(200)
	if err := v.Verify(ctx, good(fresh), audience, account); err != nil {
		t.Errorf("Google back, a minute on: %v", err)
	}

	// Answers that are not a key set hold nothing.
	for name, body := range map[string]string{"not JSON": "<html>", "no RSA key": `{"keys":[{"kid":"x","kty":"EC"}]}`, "no keys": `{"keys":[]}`,
		"a modulus too short": `{"keys":[{"kid":"k","kty":"RSA","n":"AQAB","e":"AQAB"}]}`, "over the bound": strings.Repeat(" ", maxKeysBytes+1)} {
		v2, g2, _ := rig(t)
		g2.body.Store(body)
		if got := reasonOf(t, v2.Verify(ctx, good(nil), audience, account)); got != ReasonKeysUnavailable {
			t.Errorf("an answer that is %s: %q", name, got)
		}
	}
}

func TestConcurrentTokensMakeOneFetch(t *testing.T) {
	v, g, _ := rig(t)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	tok := good(nil)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- v.Verify(context.Background(), tok, audience, account) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a concurrent verification: %v", err)
		}
	}
	if n := g.fetches.Load(); n != 1 {
		t.Errorf("24 concurrent verifications fetched %d times", n)
	}
}

func TestTheKeysAreKeptForWhatGoogleSaysWithinBounds(t *testing.T) {
	for cc, want := range map[string]time.Duration{
		"public, max-age=17000": 17000 * time.Second,
		"max-age=1":             time.Minute,
		"max-age=0":             time.Minute,
		"max-age=99999999":      24 * time.Hour,
		"no-cache":              time.Hour,
		"":                      time.Hour,
		"max-age=abc":           time.Hour,
	} {
		if got := keyTTL(cc); got != want {
			t.Errorf("Cache-Control %q keeps the keys %s, want %s", cc, got, want)
		}
	}
}
