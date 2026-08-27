package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A token file this identity OWNS works for any provider that issues
// one, on every platform — nothing is being read out of another app's
// sandbox, so the mobile restriction does not apply.
func TestOwnedTokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	b, _ := json.Marshal(map[string]any{
		"access_token":  "acc-1",
		"refresh_token": "ref-1",
		"expires_at":    time.Now().Add(time.Hour).Unix(),
		"token_url":     "https://issuer.example/token",
		"client_id":     "cid",
		"headers":       map[string]string{"X-Extra": "v"},
	})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(KindFilePrefix + path)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := s.Credential(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cr.Token != "acc-1" {
		t.Fatalf("token: %q", cr.Token)
	}
	if cr.Headers["X-Extra"] != "v" {
		t.Fatalf("the file's own headers must travel, got %v", cr.Headers)
	}
	// An owned file forces no dialect and no endpoint: it is a credential
	// for whatever provider the entry names.
	if s.Dialect() != "" || s.BaseURL() != "" {
		t.Fatalf("an owned file must not override the entry, got %q/%q", s.Dialect(), s.BaseURL())
	}
}

func TestExpiredSourceRequiresOwnerRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	original, _ := json.Marshal(map[string]any{
		"access_token": "expired", "refresh_token": "must-not-be-spent",
		"expires_at": time.Now().Add(-time.Hour).Unix(),
		"token_url":  "http://127.0.0.1:1",
	})
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := New(KindFilePrefix + path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Credential(t.Context()); !errors.Is(err, ErrOwnerRefreshRequired) {
		t.Fatalf("expired owner source must fail with a typed recovery route, got %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("owner original changed: err=%v bytes_equal=%v", err, string(got) == string(original))
	}
}

func TestStaleAcceptsOnlyOwnerUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	writeToken := func(token string) {
		b, _ := json.Marshal(map[string]any{"access_token": token, "expires_at": int64(4102444800)})
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeToken("token-one")
	source, err := New(KindFilePrefix + path)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := source.Credential(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Stale(t.Context(), cred.Gen); !errors.Is(err, ErrOwnerRefreshRequired) {
		t.Fatalf("unchanged rejected source must require its owner, got %v", err)
	}
	writeToken("token-two")
	if err := source.Stale(t.Context(), cred.Gen); err != nil {
		t.Fatalf("owner update was not accepted: %v", err)
	}
	next, err := source.Credential(t.Context())
	if err != nil || next.Token != "token-two" {
		t.Fatalf("owner update not loaded: token=%q err=%v", next.Token, err)
	}
}

func TestOwnerRewriteSupersedesMemoryEvenWithSameSizeAndMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	first, _ := json.Marshal(map[string]any{"access_token": "token-one", "expires_at": int64(4102444800)})
	second, _ := json.Marshal(map[string]any{"access_token": "token-two", "expires_at": int64(4102444800)})
	if len(first) != len(second) {
		t.Fatal("test credentials must have equal size")
	}
	if err := os.WriteFile(path, first, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := New(KindFilePrefix + path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, second, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	cred, err := source.Credential(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cred.Token != "token-two" {
		t.Fatalf("owner rewrite was masked by stale runtime state: %q", cred.Token)
	}
}

// The vendor shapes are accepted too, because an operator may simply have
// copied one — the file is theirs, so the reader meets it where it is.
func TestOwnedFileAcceptsVendorShapes(t *testing.T) {
	dir := t.TempDir()
	for name, payload := range map[string]any{
		"claude.json": map[string]any{"claudeAiOauth": map[string]any{
			"accessToken": "a", "refreshToken": "r",
			"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
			"scopes":    []string{"user:inference"}}},
		"codex.json": map[string]any{"tokens": map[string]any{
			"access_token": "b", "refresh_token": "r", "account_id": "acct"}},
	} {
		p := filepath.Join(dir, name)
		b, _ := json.Marshal(payload)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := New(KindFilePrefix + p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := s.Credential(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// An expired source names its owner-mediated recovery route.
func TestOwnedFileNamesRecoveryRoute(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.json")
	b, _ := json.Marshal(map[string]any{
		"access_token": "a", "refresh_token": "r",
		"expires_at": time.Now().Add(-time.Hour).Unix(),
	})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(KindFilePrefix + p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Credential(context.Background())
	if !errors.Is(err, ErrOwnerRefreshRequired) || !contains(err.Error(), "own tool") {
		t.Fatalf("want the typed owner recovery route, got: %v", err)
	}
}
