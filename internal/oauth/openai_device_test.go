package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenAIDeviceUsesConfiguredEndpointsAndSharedExchange(t *testing.T) {
	verifier := "test-verifier"
	sum := sha256.Sum256([]byte(verifier))
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			var in map[string]string
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["client_id"] != "configured-client" {
				t.Error("device start did not use configured JSON client")
			}
			fmt.Fprint(w, `{"device_auth_id":"private-device-id","usercode":"ABCD","interval":"1"}`)
		case "/poll":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["device_auth_id"] != "private-device-id" || in["user_code"] != "ABCD" {
				t.Error("wrong device poll")
			}
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"authorization_code": "issued-code", "code_verifier": verifier, "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:])})
		case "/exchange":
			_ = r.ParseForm()
			if r.Form.Get("code") != "issued-code" || r.Form.Get("code_verifier") != verifier || r.Form.Get("redirect_uri") != "https://authority.example/device-return" || r.Form.Get("configured_field") != "configured-value" {
				t.Errorf("wrong shared code exchange fields")
			}
			fmt.Fprint(w, `{"access_token":"opaque-access"}`)
		default:
			t.Error("unconfigured endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := Provider{DeviceExpiresSeconds: 900, ClientID: "configured-client", DeviceURL: server.URL + "/start", DevicePollURL: server.URL + "/poll", TokenURL: server.URL + "/exchange", VerificationURI: "https://authority.example/device", DeviceRedirectURI: "https://authority.example/device-return", TokenParams: map[string]any{"configured_field": "configured-value"}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d, err := StartOpenAIDevice(ctx, server.Client(), p)
	if err != nil {
		t.Fatal(err)
	}
	if d.UserCode != "ABCD" || d.VerificationURI != p.VerificationURI {
		t.Fatal("incorrect browser view")
	}
	d.Interval = time.Millisecond
	tok, err := PollOpenAIDevice(ctx, server.Client(), server.Client(), p, p.Params(), d)
	if err != nil || tok.Access != "opaque-access" || polls.Load() != 2 {
		t.Fatalf("device exchange: %v", err)
	}
	// .
	if tok.Refresh != "" || !tok.Expires.IsZero() {
		t.Fatal("invented token fields")
	}
}

func TestOpenAIDeviceCancellationStopsPolling(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(404) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PollOpenAIDevice(ctx, server.Client(), server.Client(), Provider{DevicePollURL: server.URL}, OAuthParams{}, &DeviceAuthorization{Expires: time.Now().Add(time.Minute)})
	if err == nil || calls.Load() != 0 {
		t.Fatal("cancelled device sign-in reached the authority")
	}
}
