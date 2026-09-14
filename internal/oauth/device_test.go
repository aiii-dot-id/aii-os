package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
// .
func TestTheDeviceCodeGrantPollsUntilTheOperatorConsents(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	verdicts := []string{"authorization_pending", "slow_down", ""}
	revoked := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("scope") != "repo" {
			t.Errorf("device request: %v", r.PostForm)
		}
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-EFGH", "verification_uri": "https://auth.example/device", "expires_in": 600, "interval": 0.01})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		i := polls
		polls++
		mu.Unlock()
		if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.PostForm.Get("device_code") != "dc" {
			t.Errorf("poll: %v", r.PostForm)
		}
		w.Header().Set("Content-Type", "application/json")
		if i < len(verdicts) && verdicts[i] != "" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]any{"error": verdicts[i]})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "gh-token", "scope": "repo"})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		revoked = r.PostForm.Get("token")
		mu.Unlock()
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	p := OAuthParams{ClientID: "cid", TokenURL: ts.URL + "/token", Scope: "repo"}
	d, err := StartDevice(context.Background(), ts.Client(), ts.URL+"/device", p)
	if err != nil || d.UserCode != "ABCD-EFGH" || d.VerificationURI == "" {
		t.Fatalf("start: %+v %v", d, err)
	}
	tok, err := PollDevice(context.Background(), ts.Client(), p, d)
	if err != nil || tok.Access != "gh-token" || !tok.Expires.IsZero() {
		t.Fatalf("poll: %+v %v", tok, err)
	}
	if polls != 3 {
		t.Fatalf("pending, slow_down, then issued: %d polls", polls)
	}
	if err := Revoke(context.Background(), ts.Client(), ts.URL+"/revoke", "gh-token", p); err != nil || revoked != "gh-token" {
		t.Fatalf("revoke: %v (%q)", err, revoked)
	}

	verdicts = []string{"access_denied"}
	polls = 0
	if _, err := PollDevice(context.Background(), ts.Client(), p, d); !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("a refusal is classified: %v", err)
	}
	verdicts = []string{"expired_token"}
	polls = 0
	if _, err := PollDevice(context.Background(), ts.Client(), p, d); !errors.Is(err, ErrDeviceExpired) {
		t.Fatalf("an expiry is classified: %v", err)
	}
	d.Expires = time.Now().Add(-time.Second)
	if _, err := PollDevice(context.Background(), ts.Client(), p, d); !errors.Is(err, ErrDeviceExpired) {
		t.Fatalf("a lapsed code is not polled: %v", err)
	}
	if _, err := StartDevice(context.Background(), ts.Client(), "", p); err == nil {
		t.Fatal("no device endpoint, no device sign-in")
	}
}
