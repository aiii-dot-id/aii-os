package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"
)

// EVERY REQUEST TO AN AUTHORITY CARRIES ITS CONTRACT'S CONFIGURED HEADERS
// (the operator, 2026-11-11: "ALL OAuth headers are config file only"; ruling
// of 2026-11-11 §2e, §2g). For each shipped contract, every path it has
// (code exchange, refresh, device start and poll, revocation) is sent to a
// local server that records the final request, and the User-Agent and every
// other token header on it is the one providers.json names. With the value
// removed, or set empty, nothing is sent.

// fakeAuth answers every path a shipped contract may use, and records
// each request's path and headers.
type fakeAuth struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []struct {
		path    string
		headers http.Header
	}
}

const deviceVerifier = "fixture-verifier"

func newAuthority(t *testing.T) *fakeAuth {
	t.Helper()
	a := &fakeAuth{}
	sum := sha256.Sum256([]byte(deviceVerifier))
	// An access token every shipped contract accepts: a JWT carrying the
	// account claim the ChatGPT contract requires.
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	access := enc(map[string]string{"alg": "none"}) + "." + enc(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct"}}) + ".sig"
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.seen = append(a.seen, struct {
			path    string
			headers http.Header
		}{r.URL.Path, r.Header.Clone()})
		a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "r", "expires_in": 3600, "token_type": "bearer"})
		case "/device":
			fmt.Fprint(w, `{"device_code":"dc","user_code":"ABCD","verification_uri":"https://authority.example/device","expires_in":600,"interval":0.01}`)
		case "/start":
			fmt.Fprint(w, `{"device_auth_id":"dai","usercode":"ABCD","interval":"1"}`)
		case "/poll":
			json.NewEncoder(w).Encode(map[string]string{"authorization_code": "issued", "code_verifier": deviceVerifier, "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:])})
		case "/revoke":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *fakeAuth) requests() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.seen)
}

// at points a shipped contract's endpoints at the authority, keeping every
// other configured fact, its token headers included. A plugin contract
// ships without a client, which is the operator's own registration.
func (a *fakeAuth) at(p Provider) Provider {
	if p.ClientID == "" {
		p.ClientID = "fixture-client"
	}
	p.TokenURL = a.srv.URL + "/token"
	if p.DevicePollURL != "" {
		p.DeviceURL, p.DevicePollURL = a.srv.URL+"/start", a.srv.URL+"/poll"
	} else if p.DeviceURL != "" {
		p.DeviceURL = a.srv.URL + "/device"
	}
	if p.RevokeURL != "" {
		p.RevokeURL = a.srv.URL + "/revoke"
	}
	return p
}

// everyPath sends each request the contract has, and names them.
func everyPath(ctx context.Context, client *http.Client, p Provider) ([]string, error) {
	params := p.Params()
	paths := []string{"code exchange", "refresh"}
	if _, err := exchange(ctx, client, params, map[string]any{"grant_type": "authorization_code", "code": "c"}); err != nil {
		return paths[:1], err
	}
	if _, err := refreshWith(ctx, client, params, "r"); err != nil {
		return paths, err
	}
	switch {
	case p.DevicePollURL != "":
		paths = append(paths, "device start", "device poll (and its exchange)")
		d, err := StartOpenAIDevice(ctx, client, p)
		if err != nil {
			return paths, err
		}
		d.Interval = time.Millisecond
		if _, err := PollOpenAIDevice(ctx, client, client, p, params, d); err != nil {
			return paths, err
		}
	case p.DeviceURL != "":
		paths = append(paths, "device start", "device poll")
		d, err := StartDevice(ctx, client, p.DeviceURL, params)
		if err != nil {
			return paths, err
		}
		if _, err := PollDevice(ctx, client, params, d); err != nil {
			return paths, err
		}
	}
	if p.RevokeURL != "" {
		paths = append(paths, "revoke")
		if err := Revoke(ctx, client, p.RevokeURL, "a", params); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

func TestEveryAuthorityRequestCarriesTheContractsConfiguredHeaders(t *testing.T) {
	catalog := testCatalog()
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) < 6 {
		t.Fatalf("%d shipped contracts read; the test is not reading providers.json", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			shipped, _ := ProviderTemplate(name, catalog)
			if shipped.TokenHeaders["User-Agent"] == "" {
				t.Fatal("the shipped contract configures no User-Agent")
			}
			auth := newAuthority(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			paths, err := everyPath(ctx, auth.srv.Client(), auth.at(shipped))
			if err != nil {
				t.Fatalf("%v: %v", paths, err)
			}
			auth.mu.Lock()
			defer auth.mu.Unlock()
			for _, r := range auth.seen {
				for k, v := range shipped.TokenHeaders {
					if got := r.headers.Values(k); len(got) != 1 || got[0] != v {
						t.Errorf("%s: %s = %q, config says %q", r.path, k, got, v)
					}
				}
			}
			t.Logf("captured %d requests on %v", len(auth.seen), paths)
		})
	}
}

// A CHANGED VALUE CHANGES THE WIRE, AND A REMOVED OR EMPTY ONE SENDS
// NOTHING. The negative removes the User-Agent from the contract itself:
// there is no other source, and Go's own default is never sent.
func TestAnAuthorityRequestWithoutAConfiguredUserAgentIsNotSent(t *testing.T) {
	catalog := testCatalog()
	for _, name := range []string{"github", "openai-chatgpt", "anthropic-claude"} {
		shipped, ok := ProviderTemplate(name, catalog)
		if !ok {
			t.Fatalf("no shipped contract %q", name)
		}
		t.Run(name+", changed", func(t *testing.T) {
			auth := newAuthority(t)
			p := auth.at(shipped)
			p.TokenHeaders = map[string]string{"User-Agent": "fixture-agent/9"}
			if _, err := everyPath(t.Context(), auth.srv.Client(), p); err != nil {
				t.Fatal(err)
			}
			auth.mu.Lock()
			defer auth.mu.Unlock()
			for _, r := range auth.seen {
				if r.headers.Get("User-Agent") != "fixture-agent/9" {
					t.Errorf("%s: User-Agent = %q, want the configured fixture-agent/9", r.path, r.headers.Get("User-Agent"))
				}
			}
		})
		for _, tc := range []struct {
			label   string
			headers map[string]string
			typed   bool
		}{{"removed", nil, true}, {"empty", map[string]string{"User-Agent": ""}, false}} {
			t.Run(name+", "+tc.label, func(t *testing.T) {
				auth := newAuthority(t)
				p := auth.at(shipped)
				p.TokenHeaders = tc.headers
				params := p.Params()
				calls := map[string]func() error{
					"code exchange": func() error {
						_, err := exchange(t.Context(), auth.srv.Client(), params, map[string]any{"grant_type": "authorization_code"})
						return err
					},
					"refresh": func() error { _, err := refreshWith(t.Context(), auth.srv.Client(), params, "r"); return err },
					"revoke":  func() error { return Revoke(t.Context(), auth.srv.Client(), auth.srv.URL+"/revoke", "a", params) },
				}
				if p.DevicePollURL != "" {
					calls["device start"] = func() error { _, err := StartOpenAIDevice(t.Context(), auth.srv.Client(), p); return err }
					calls["device poll"] = func() error {
						_, err := PollOpenAIDevice(t.Context(), auth.srv.Client(), auth.srv.Client(), p, params, &DeviceAuthorization{DeviceCode: "dai", UserCode: "ABCD", Interval: time.Millisecond, Expires: time.Now().Add(time.Minute)})
						return err
					}
				} else if p.DeviceURL != "" {
					calls["device start"] = func() error { _, err := StartDevice(t.Context(), auth.srv.Client(), p.DeviceURL, params); return err }
					calls["device poll"] = func() error {
						_, err := PollDevice(t.Context(), auth.srv.Client(), params, &DeviceAuthorization{DeviceCode: "dc", Interval: time.Millisecond, Expires: time.Now().Add(time.Minute)})
						return err
					}
				}
				for path, call := range calls {
					err := call()
					var unset *HeaderNotConfiguredError
					if err == nil || tc.typed != errors.As(err, &unset) {
						t.Errorf("%s: err = %v, want a refusal (typed: %v)", path, err, tc.typed)
					}
				}
				if n := auth.requests(); n != 0 {
					t.Fatalf("%d requests reached the authority; none may be sent without a configured User-Agent", n)
				}
			})
		}
	}
}
