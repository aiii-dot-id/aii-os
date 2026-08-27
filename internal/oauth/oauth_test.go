package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// requireAdoption skips where the platform cannot adopt at all. Found by
// running this suite on a real Android device: the code was right and
// the tests were desktop-assuming.
func requireAdoption(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("this platform cannot adopt another tool's credentials (see TestPlatformContract)")
	}
}

func fixtureHome(t *testing.T) string {
	t.Helper()
	requireAdoption(t)
	dir := t.TempDir()
	old := homeDir
	homeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { homeDir = old })
	return dir
}

func write(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jwtWith(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "x." + base64.RawURLEncoding.EncodeToString(b) + ".y"
}

func claudeTestOptions(extra map[string]string) map[string]string {
	options := map[string]string{
		"billing_text":          "billing-marker",
		"header_anthropic-beta": "oauth-test",
		"header_user-agent":     "claude-cli/test",
		"header_x-app":          "cli",
	}
	for name, value := range extra {
		options[name] = value
	}
	return options
}

// The two real vendor shapes decode: a millisecond expiry on one, a JWT
// claim on the other, and the account id becomes a header.
func TestAdoptsBothVendorShapes(t *testing.T) {
	home := fixtureHome(t)
	exp := time.Now().Add(time.Hour)
	write(t, filepath.Join(home, ".claude", ".credentials.json"), map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "acc-a", "refreshToken": "ref-a",
			"expiresAt": exp.UnixMilli(),
			"scopes":    []string{"user:profile", "user:inference"},
		}})
	write(t, filepath.Join(home, ".codex", "auth.json"), map[string]any{
		"tokens": map[string]any{
			"access_token":  jwtWith(map[string]any{"exp": exp.Unix()}),
			"refresh_token": "ref-o", "account_id": "acct-1",
		}})

	claudeOptions := claudeTestOptions(nil)
	claude, err := New(KindClaudeCode, claudeOptions)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := claude.Credential(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cr.Token != "acc-a" {
		t.Fatalf("claude token: %q", cr.Token)
	}
	if claude.Dialect() != "anthropic" || claude.BaseURL() != "" {
		t.Fatalf("claude forces the native dialect and no base override, got %q/%q", claude.Dialect(), claude.BaseURL())
	}
	if claude.BillingText() == "" {
		t.Fatal("Claude Code OAuth must carry its billing system block")
	}
	for _, header := range []string{"anthropic-beta", "user-agent", "x-app"} {
		if cr.Headers[header] == "" {
			t.Fatalf("Claude Code OAuth lacks %s: %v", header, cr.Headers)
		}
	}
	rotated, err := New(KindClaudeCode, claudeTestOptions(map[string]string{"billing_text": "updated-marker"}))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.BillingText() != "updated-marker" {
		t.Fatalf("operator billing override ignored: %q", rotated.BillingText())
	}

	codex, err := New(KindCodex)
	if err != nil {
		t.Fatal(err)
	}
	cr, err = codex.Credential(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cr.Headers["ChatGPT-Account-ID"] != "acct-1" {
		t.Fatalf("the account id must ride as a header, got %v", cr.Headers)
	}
	// A ChatGPT credential is only valid on the ChatGPT backend, whatever
	// the provider entry says.
	if codex.BaseURL() == "" || codex.Dialect() != "chatgpt" {
		t.Fatalf("codex must force its own base and dialect, got %q/%q", codex.BaseURL(), codex.Dialect())
	}
}

// NOTE (2026-08-23): the "Claude Code credential without its request
// contract is refused" test moved to internal/app
// (TestCredentialRefusedWhenRegistryOptionsMissing). The required
// options are declared by config/providers.json now, not by a slice in
// this package, so the gate runs where the registry is known.

// A credential that cannot do inference is refused at CONFIGURATION time,
// with the reason — not left to fail later as an opaque 403.
func TestScopeGateRefusesNonInferenceCredential(t *testing.T) {
	home := fixtureHome(t)
	write(t, filepath.Join(home, ".claude", ".credentials.json"), map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "acc", "refreshToken": "ref",
			"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
			"scopes":    []string{"user:profile", "user:mcp_servers"},
		}})
	_, err := New(KindClaudeCode, claudeTestOptions(nil))
	if err == nil {
		t.Fatal("a credential without user:inference must be refused")
	}
	if got := err.Error(); !contains(got, "user:inference") || !contains(got, "cannot serve inference") {
		t.Fatalf("the refusal must name what is missing, got: %v", err)
	}
}

// THE CUSTODY LAW: the owner's file is never written. Two writers plus
// refresh-token rotation would lock the operator out of their own tool.
func TestNeverWritesTheOwnersFile(t *testing.T) {
	home := fixtureHome(t)
	path := filepath.Join(home, ".claude", ".credentials.json")
	write(t, path, map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "acc", "refreshToken": "ref",
			"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
			"scopes":    []string{"user:inference"},
		}})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)

	s, err := New(KindClaudeCode, claudeTestOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Credential(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// A 401 that cannot be refreshed must not write either.
	_ = s.Stale(context.Background(), 1)

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the owner-maintained credential file was modified")
	}
	fi2, _ := os.Stat(path)
	if !fi.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("the owner-maintained credential file was touched")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("a temp copy of the credential was left on disk")
	}
}

// An expired credential fails with its owner-mediated recovery route.
func TestExpiredCredentialSaysWhatToDo(t *testing.T) {
	home := fixtureHome(t)
	write(t, filepath.Join(home, ".claude", ".credentials.json"), map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken": "acc", "refreshToken": "",
			"expiresAt": time.Now().Add(-time.Hour).UnixMilli(),
			"scopes":    []string{"user:inference"},
		}})
	s, err := New(KindClaudeCode, claudeTestOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Credential(context.Background())
	if err == nil || !contains(err.Error(), "refresh it with its own tool") {
		t.Fatalf("want an instruction, got: %v", err)
	}
}

func TestUnknownKindNamesTheKnownOnes(t *testing.T) {
	requireAdoption(t)
	if _, err := New("nope"); err == nil || !contains(err.Error(), KindClaudeCode) {
		t.Fatalf("want the known kinds listed, got: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Reported live: the dashboard said "valid to 17:52" and the identity
// died at 17:37 with "expired or too close to expiry". The vendor
// expiry and the USABLE boundary are different instants, and every
// surface that reports usability must subtract the same skew. This pins
// the boundary itself so the two can never drift apart again.
func TestUsableBoundaryIsTheSkewBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     time.Duration // time until the vendor expiry
		usable bool
	}{
		{"well inside its life", ExpirySkew + 30*time.Minute, true},
		{"just outside the skew", ExpirySkew + 2*time.Minute, true},
		{"inside the skew", ExpirySkew - 2*time.Minute, false},
		{"the live report: 7 minutes left", 7 * time.Minute, false},
		{"already expired", -time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := fixtureHome(t)
			write(t, filepath.Join(home, ".claude", ".credentials.json"), map[string]any{
				"claudeAiOauth": map[string]any{
					"accessToken": "acc-a", "refreshToken": "ref-a",
					"expiresAt": time.Now().Add(tc.in).UnixMilli(),
					"scopes":    []string{"user:profile", "user:inference"},
				}})
			src, err := New(KindClaudeCode, claudeTestOptions(nil))
			if err != nil {
				t.Fatal(err)
			}
			_, err = src.Credential(context.Background())
			if tc.usable && err != nil {
				t.Fatalf("credential with %v left was refused: %v", tc.in, err)
			}
			if !tc.usable {
				if err == nil {
					t.Fatalf("credential with %v left was accepted; the runtime must refuse inside the skew", tc.in)
				}
				if !errors.Is(err, ErrOwnerRefreshRequired) {
					t.Fatalf("refusal must be classified for the operator, got %v", err)
				}
			}
			// The published boundary is what a surface should render.
			if got, want := src.Info().ExpiresAt.Add(-ExpirySkew), time.Now().Add(tc.in-ExpirySkew); got.Sub(want) > time.Minute || want.Sub(got) > time.Minute {
				t.Fatalf("usable boundary %v is not expiry-minus-skew %v", got, want)
			}
		})
	}
}
