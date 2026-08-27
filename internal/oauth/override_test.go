package oauth

import (
	"os"
	"path/filepath"
	"testing"
)

// An absolute path is absolute. Splitting it into segments and joining
// under $HOME silently relocated it — the operator would have pointed at
// /etc/aii/token.json and been read from $HOME/etc/aii/token.json.
func TestAbsoluteOverridePathIsNotRelocated(t *testing.T) {
	home := fixtureHome(t)
	real := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(real, []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","scopes":["user:inference"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(KindClaudeCode, claudeTestOptions(map[string]string{"file": real}))
	if err != nil {
		t.Fatalf("an absolute override must be honoured: %v", err)
	}
	if s.Path() != real {
		t.Fatalf("path = %q, want %q", s.Path(), real)
	}
	if filepath.HasPrefix(s.Path(), home) {
		t.Fatalf("the override was relocated under the home directory: %s", s.Path())
	}
}

// A relative override still resolves under the home directory, and a
// tilde path is expanded rather than taken literally.
func TestRelativeAndTildeOverrides(t *testing.T) {
	home := fixtureHome(t)
	for _, v := range []string{"sub/dir/tok.json", "~/sub/dir/tok.json"} {
		p := filepath.Join(home, "sub", "dir", "tok.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","scopes":["user:inference"]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := New(KindClaudeCode, claudeTestOptions(map[string]string{"file": v}))
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if s.Path() != p {
			t.Fatalf("%s -> %q, want %q", v, s.Path(), p)
		}
	}
}

// The other vendor facts are correctable too, because they move on the
// vendor's schedule.
func TestOverridesReachTheSpec(t *testing.T) {
	home := fixtureHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"),
		[]byte(`{"tokens":{"access_token":"a","refresh_token":"r","account_id":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New(KindCodex, map[string]string{
		"base_url":             "https://elsewhere.example",
		"header_X-Client-Name": "aii-os",
		"query_client_version": "9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseURL() != "https://elsewhere.example" {
		t.Fatalf("base_url override ignored: %q", s.BaseURL())
	}
	if s.DiscoveryQuery()["client_version"] != "9.9.9" {
		t.Fatalf("query override ignored: %v", s.DiscoveryQuery())
	}
	cr, err := s.Credential(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cr.Headers["X-Client-Name"] != "aii-os" {
		t.Fatalf("header override ignored: %v", cr.Headers)
	}
	if cr.Headers["ChatGPT-Account-ID"] != "x" {
		t.Fatalf("the credential's own headers must survive an override: %v", cr.Headers)
	}
}

func TestInvalidOverrideFailsClosed(t *testing.T) {
	for _, options := range []map[string]string{
		{"heder_user-agent": "typo"},
		{"header_": "value"},
		{"query_": "value"},
		{"billing_text": ""},
	} {
		if _, err := New(KindClaudeCode, claudeTestOptions(options)); err == nil {
			t.Fatalf("invalid credential options were accepted: %#v", options)
		}
	}
}
