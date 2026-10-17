package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// A broad network grant is not permission to send an auth profile's secret
// to another port. Drive real redirects and inspect the receiving server,
// including the plaintext downgrade Go's hostname-only header rule permits.
func TestAuthProfileRedirectCannotLeaveItsPinnedDestination(t *testing.T) {
	for _, scheme := range []string{"bearer", "basic", "path"} {
		for _, targetTLS := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/targetTLS=%v", scheme, targetTLS), func(t *testing.T) {
				const secret = "fixture-user:fixture-secret"
				t.Setenv("AII_TEST_REDIRECT_SECRET", secret)
				var targetHits, leaked, allowedHits, originHits atomic.Int32
				expectedHeader := "Bearer " + secret
				if scheme == "basic" {
					expectedHeader = "Basic " + base64.StdEncoding.EncodeToString([]byte(secret))
				}
				hasSecret := func(r *http.Request) bool {
					if scheme == "path" {
						return strings.Contains(r.URL.Path, secret)
					}
					return r.Header.Get("Authorization") == expectedHeader
				}
				targetHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					targetHits.Add(1)
					if hasSecret(r) {
						leaked.Add(1)
					}
					fmt.Fprint(w, `{}`)
				})
				var target *httptest.Server
				if targetTLS {
					target = httptest.NewTLSServer(targetHandler)
				} else {
					target = httptest.NewServer(targetHandler)
				}
				t.Cleanup(target.Close)
				origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case strings.HasPrefix(r.URL.Path, "/escape/"):
						originHits.Add(1)
						http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
					case strings.HasPrefix(r.URL.Path, "/same/"):
						http.Redirect(w, r, strings.Replace(r.URL.Path, "/same/", "/final/", 1), http.StatusFound)
					default:
						if hasSecret(r) {
							allowedHits.Add(1)
						}
						fmt.Fprint(w, `{}`)
					}
				}))
				t.Cleanup(origin.Close)
				host, port := tsHostPort(t, origin)
				guard := func(_ context.Context, raw string) error {
					u, err := url.Parse(raw)
					if err == nil && (u.Scheme+"://"+u.Host == origin.URL || u.Scheme+"://"+u.Host == target.URL) {
						return nil
					}
					return firewall.ErrEgressBlocked
				}
				h := newHost(t, newStore(t), Config{
					Grants: map[string]Grant{"p": {Hosts: []string{host}, CredentialHandles: []string{"api"}}},
					AuthProfiles: map[string]AuthProfile{"api": {
						Scheme: scheme, SecretEnv: "AII_TEST_REDIRECT_SECRET", Host: host, Port: port,
					}},
					Guard: guard, Transport: origin.Client().Transport,
				})
				b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + host})
				suffix := "item"
				if scheme == "path" {
					suffix = CredentialPlaceholder
				}
				m := dispatch(t, b, netParams(origin.URL+"/escape/"+suffix, `{"auth_profile":"api"}`))
				if targetHits.Load() != 0 {
					t.Errorf("redirect reached a different port: requests=%d carrying secret=%d", targetHits.Load(), leaked.Load())
				}
				receipt := wantResult(t, m, statusFailed, reasonNetRemoteFailed) // the first hop answered: not a policy denial (C2)
				if originHits.Load() != 1 || receiptField(t, receipt, "effect") != EffectPerformed {
					t.Fatal("redirect refusal must not retry or erase the answered first hop")
				}
				whole, err := json.Marshal(m)
				if err != nil || strings.Contains(string(whole), secret) || strings.Contains(string(whole), base64.StdEncoding.EncodeToString([]byte(secret))) {
					t.Fatal("redirect refusal exposed the credential")
				}
				// Refusal must not disable valid authenticated redirects or later calls.
				wantResult(t, dispatch(t, b, netParams(origin.URL+"/same/"+suffix, `{"auth_profile":"api"}`)), statusSucceeded, "")
				if allowedHits.Load() != 1 {
					t.Fatal("same-destination redirect lost the admitted credential")
				}
				// Without a credential, the broader network grant still governs.
				wantResult(t, dispatch(t, b, netParams(origin.URL+"/escape/public", "")), statusSucceeded, "")
				if targetHits.Load() != 1 || leaked.Load() != 0 {
					t.Fatal("public redirect was blocked or acquired a credential")
				}
			})
		}
	}
}

type authRedirectTransport func(*http.Request) (*http.Response, error)

func (f authRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Keep host, effective port and scheme independent: the real-server test's
// downgrade also changes ports, so it cannot by itself prove the HTTPS rule.
// This transport records requests without DNS, sockets or outside credentials.
func TestAuthRedirectChecksHostPortAndHTTPSIndependently(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		allow        bool
		plaintextOK  bool
	}{
		{"relative", "/final", true, false},
		{"explicit default port", "https://api.example.test:443/final", true, false},
		{"other port", "https://api.example.test:444/final", false, false},
		{"other host", "https://other.example.test:443/final", false, false},
		{"subdomain", "https://child.api.example.test:443/final", false, false},
		{"downgrade same port", "http://api.example.test:443/final", false, false},
		{"local consent is not public consent", "http://api.example.test:443/final", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AII_TEST_REDIRECT_SECRET", "fixture-token")
			var finalHits atomic.Int32
			var finalAuth atomic.Value
			transport := authRedirectTransport(func(r *http.Request) (*http.Response, error) {
				header, status := make(http.Header), http.StatusOK
				if r.URL.Path == "/start" {
					header.Set("Location", tc.target)
					status = http.StatusFound
				} else {
					finalHits.Add(1)
					finalAuth.Store(r.Header.Get("Authorization"))
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			hosts := []string{"api.example.test", "other.example.test", "child.api.example.test"}
			h := newHost(t, newStore(t), Config{
				Grants: map[string]Grant{"p": {Hosts: hosts, CredentialHandles: []string{"api"}, PlaintextCredentials: tc.plaintextOK}},
				AuthProfiles: map[string]AuthProfile{"api": {
					Scheme: "bearer", SecretEnv: "AII_TEST_REDIRECT_SECRET", Host: "api.example.test", Port: 443,
				}},
				Guard: func(context.Context, string) error { return nil }, Transport: transport,
			})
			b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:api.example.test", "net.outbound:other.example.test", "net.outbound:child.api.example.test"})
			m := dispatch(t, b, netParams("https://api.example.test/start", `{"auth_profile":"api"}`))
			if tc.allow {
				wantResult(t, m, statusSucceeded, "")
				if finalHits.Load() != 1 || finalAuth.Load() != "Bearer fixture-token" {
					t.Fatal("valid redirect lost the admitted credential")
				}
			} else {
				if finalHits.Load() != 0 {
					t.Error("out-of-profile redirect reached the transport")
				}
				wantResult(t, m, statusDenied, reasonPolicyDeny)
			}
		})
	}
}

func TestAuthRedirectPreservesExplicitLocalPlaintextConsent(t *testing.T) {
	var finalHits atomic.Int32
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") == "Bearer fixture-local-token" {
			finalHits.Add(1)
		}
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(device.Close)
	host, port := tsHostPort(t, device)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	t.Setenv("AII_TEST_REDIRECT_SECRET", "fixture-local-token")
	for _, consent := range []bool{false, true} {
		h := newHost(t, newStore(t), Config{
			Grants: map[string]Grant{"p": {Local: []string{hostPort}, CredentialHandles: []string{"api"}, PlaintextCredentials: consent}},
			AuthProfiles: map[string]AuthProfile{"api": {
				Scheme: "bearer", SecretEnv: "AII_TEST_REDIRECT_SECRET", Host: host, Port: port,
			}},
		})
		b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
		m := dispatch(t, b, netParams(device.URL+"/start", `{"auth_profile":"api"}`))
		if consent {
			wantResult(t, m, statusSucceeded, "")
		} else {
			wantResult(t, m, statusDenied, reasonAuthRequiresHTTPS)
		}
	}
	if finalHits.Load() != 1 {
		t.Fatal("explicit local consent must permit the authenticated redirect")
	}
}
