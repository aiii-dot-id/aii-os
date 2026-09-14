package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// .
// .
type authority struct {
	mu       sync.Mutex
	requests []url.Values
	answer   map[string]any
	status   int
	delay    time.Duration
}

func (a *authority) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		a.mu.Lock()
		a.requests = append(a.requests, r.PostForm)
		answer, status, delay := a.answer, a.status, a.delay
		a.mu.Unlock()
		time.Sleep(delay)
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(answer)
	}))
}

func (a *authority) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.requests)
}

func (a *authority) last() url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) == 0 {
		return nil
	}
	return a.requests[len(a.requests)-1]
}

// .
// .
// .
// .
func TestTheGenericExchangeSpeaksPlainOAuth(t *testing.T) {
	a := &authority{answer: map[string]any{"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600, "scope": "calendar.readonly"}}
	ts := a.serve(t)
	defer ts.Close()
	p := OAuthParams{ClientID: "cid", ClientSecret: "csecret", TokenURL: ts.URL}
	tok, err := refreshWith(context.Background(), ts.Client(), p, "rt-0")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "at-1" || tok.Refresh != "rt-1" || tok.Scope != "calendar.readonly" || tok.Expires.IsZero() {
		t.Fatalf("tokens: %+v", tok)
	}
	if got := a.last(); got.Get("grant_type") != "refresh_token" || got.Get("refresh_token") != "rt-0" || got.Get("client_id") != "cid" || got.Get("client_secret") != "csecret" {
		t.Fatalf("the refresh grant with the client secret: %v", got)
	}

	a.answer = map[string]any{"access_token": "gh-1"}
	tok, err = refreshWith(context.Background(), ts.Client(), OAuthParams{ClientID: "cid", TokenURL: ts.URL}, "rt-0")
	if err != nil || tok.Access != "gh-1" || !tok.Expires.IsZero() || tok.Refresh != "" {
		t.Fatalf("a token without expiry or refresh: %+v %v", tok, err)
	}
	if got := a.last(); got.Has("client_secret") {
		t.Fatal("no secret configured, none sent")
	}

	a.answer = map[string]any{"error": "invalid_grant", "error_description": "Token has been expired or revoked."}
	a.status = 400
	if _, err := refreshWith(context.Background(), ts.Client(), p, "rt-0"); !errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("invalid_grant is classified: %v", err)
	}
	a.answer = map[string]any{"error": "server_error"}
	a.status = 500
	if _, err := refreshWith(context.Background(), ts.Client(), p, "rt-0"); err == nil || errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("another failure is not invalid_grant: %v", err)
	}
	a.answer = map[string]any{"token_type": "bearer"}
	a.status = 200
	if _, err := refreshWith(context.Background(), ts.Client(), p, "rt-0"); err == nil {
		t.Fatal("a response without an access token is refused")
	}
}

// .
// .
// .
// .
// .
func TestAProfileSourceOwnsAndRefreshesItsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles", "gmail.json")
	expired := &Tokens{Access: "at-old", Refresh: "rt-keep", Scope: "s", Expires: time.Now().Add(-time.Minute)}
	if err := WriteTokenFile(path, expired); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the token file is private to the owner: %v %v", fi.Mode(), err)
	}
	raw, _ := os.ReadFile(path)
	st, err := parseGeneric(raw)
	if err != nil || st.access != "at-old" || st.refresh != "rt-keep" || !st.owned || st.expires.IsZero() || !nearExpiry(st) {
		t.Fatalf("the generic parser reads the profile's file: %+v %v", st, err)
	}

	a := &authority{answer: map[string]any{"access_token": "at-new", "expires_in": 3600}, delay: 50 * time.Millisecond}
	ts := a.serve(t)
	defer ts.Close()
	p := OAuthParams{ClientID: "cid", TokenURL: ts.URL}
	src, err := NewProfileSource(path, p, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !src.Owned() {
		t.Fatal("a profile source is owned by construction")
	}
	// .
	var wg sync.WaitGroup
	creds := make([]Credential, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			creds[i], errs[i] = src.Credential(context.Background())
		}(i)
	}
	wg.Wait()
	for i := range creds {
		if errs[i] != nil || creds[i].Token != "at-new" {
			t.Fatalf("waiter %d: %v %+v", i, errs[i], creds[i])
		}
	}
	if a.count() != 1 {
		t.Fatalf("one refresh serves every waiter: %d round-trips", a.count())
	}
	raw, _ = os.ReadFile(path)
	st, _ = parseGeneric(raw)
	if st.access != "at-new" || st.refresh != "rt-keep" || !st.owned {
		t.Fatalf("the file carries the new access token and the kept refresh token: %+v", st)
	}
	// .
	if c, err := src.Credential(context.Background()); err != nil || c.Token != "at-new" || a.count() != 1 {
		t.Fatalf("a fresh token is served from the file: %+v %v (%d)", c, err, a.count())
	}
	// .
	gen := src.Generation()
	a.answer = map[string]any{"access_token": "at-forced", "refresh_token": "rt-rotated", "expires_in": 3600}
	c, err := src.ForceRefresh(context.Background(), gen)
	if err != nil || c.Token != "at-forced" || a.count() != 2 {
		t.Fatalf("a forced refresh renews a token that was not near expiry: %+v %v (%d)", c, err, a.count())
	}
	raw, _ = os.ReadFile(path)
	st, _ = parseGeneric(raw)
	if st.refresh != "rt-rotated" {
		t.Fatalf("a rotated refresh token is stored: %+v", st)
	}
	// .
	if c, err := src.ForceRefresh(context.Background(), gen); err != nil || c.Token != "at-forced" || a.count() != 2 {
		t.Fatalf("a caller with the old generation takes the renewed credential: %+v %v (%d)", c, err, a.count())
	}
	// .
	a.answer = map[string]any{"error": "invalid_grant"}
	a.status = 400
	if _, err := src.ForceRefresh(context.Background(), src.Generation()); !errors.Is(err, ErrGrantInvalid) {
		t.Fatalf("a revoked grant is named: %v", err)
	}
	// .
	if _, err := NewProfileSource(filepath.Join(dir, "none.json"), p, ts.Client()); err == nil {
		t.Fatal("a profile without a token file has no source")
	}
}

// .
// .
func TestProviderTemplatesAreData(t *testing.T) {
	g, ok := ProviderTemplate("google")
	if !ok || g.TokenURL == "" || g.AuthorizeParams["access_type"] != "offline" || len(g.Hosts) == 0 || len(g.Scopes["calendar"].Read) == 0 {
		t.Fatalf("google: %+v", g)
	}
	g.Hosts[0] = "changed"
	if again, _ := ProviderTemplate("google"); again.Hosts[0] == "changed" {
		t.Fatal("a caller's narrowing must not reach the table")
	}
	for _, name := range ProviderNames() {
		if p, ok := ProviderTemplate(name); !ok || p.TokenURL == "" || p.AuthorizeURL == "" || len(p.Hosts) == 0 {
			t.Fatalf("%s: incomplete template %+v", name, p)
		}
	}
	if _, ok := ProviderTemplate("acme"); ok {
		t.Fatal("an unknown authority has no template")
	}
}
