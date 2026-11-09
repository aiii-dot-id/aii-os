package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// shippedSubscriptionEntry is the registry's own entry for a subscription's
// credential, options and address with it, so the probe reaches the store
// and not the missing-options refusal before it.
func shippedSubscriptionEntry(t *testing.T, kind string) providerEntry {
	t.Helper()
	for _, e := range embeddedRegistry().Providers {
		if e.Credential == kind {
			return e
		}
	}
	t.Fatalf("the embedded registry ships no entry with credential %q", kind)
	return providerEntry{}
}

// emptyHome points the credential stores at a home with nothing in it.
func emptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// needsDesktopStore skips the tests that must reach a credential store on disk.
// On macOS the Claude Code store falls back to the account's Keychain, which
// may hold a real sign-in and puts an approval dialog in front of the lookup;
// on mobile adoption is refused before any store is read (oauth.ErrNotOnMobile).
// The wrong-address and missing-options cases need no store and run everywhere.
func needsDesktopStore(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		t.Skip("the Keychain of the account running the tests may hold a Claude Code sign-in; the row's words are proved on Linux and Windows")
	case "android", "ios":
		t.Skip("adopted credentials are a desktop feature; on mobile the store is refused before it is read (oauth.ErrNotOnMobile)")
	}
}

// A subscription whose sign-in is not on this computer says the one thing
// the person can do, not the file the error names. On a computer without
// developer tools every choice had said "paste a key".
func TestANotSignedInSubscriptionSaysWhatToDo(t *testing.T) {
	needsDesktopStore(t)
	emptyHome(t)
	want := map[string]string{
		"claude-code": "Sign in to Claude Code on this computer and your subscription works here. No key is needed.",
		"codex":       "Sign in to the Codex app on this computer and your subscription works here. No key is needed.",
	}
	for kind, sentence := range want {
		a := &App{}
		probe := a.probeOne(shippedSubscriptionEntry(t, kind))
		if probe.state != probeNoCredential {
			t.Fatalf("%s with no store on this computer: state %q (%s), want %q", kind, probe.state, probe.reason, probeNoCredential)
		}
		if probe.reason != sentence {
			t.Fatalf("%s with no store on this computer says %q, want %q", kind, probe.reason, sentence)
		}
	}
}

// A credential that IS on this computer but cannot serve inference keeps
// the scope refusal's own words: telling that person to sign in would send
// them to do again what they have done.
func TestAPresentCredentialWithoutInferenceKeepsItsRefusal(t *testing.T) {
	needsDesktopStore(t)
	home := emptyHome(t)
	raw, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:mcp_servers"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	probe := a.probeOne(shippedSubscriptionEntry(t, "claude-code"))
	if probe.state != probeNoCredential {
		t.Fatalf("a present credential without inference: state %q (%s), want %q", probe.state, probe.reason, probeNoCredential)
	}
	if probe.reason == signInReason("claude-code") || !strings.Contains(probe.reason, "cannot serve inference") {
		t.Fatalf("a present credential without inference says %q, want the scope refusal's own words", probe.reason)
	}
}

// An entry that lacks the options its credential requires is refused before
// any store is opened, and keeps that refusal: the blanket wrap of every
// credential error is the thing this test forbids.
func TestAnEntryMissingItsOptionsKeepsThatRefusal(t *testing.T) {
	emptyHome(t)
	a := &App{}
	probe := a.probeOne(providerEntry{Name: "bare", URL: credentialsHome("claude-code"), Credential: "claude-code"})
	if probe.state != probeNoCredential {
		t.Fatalf("an entry without its options: state %q (%s), want %q", probe.state, probe.reason, probeNoCredential)
	}
	if probe.reason == signInReason("claude-code") || !strings.Contains(probe.reason, "requires provider options") {
		t.Fatalf("an entry without its options says %q, want the missing-options refusal", probe.reason)
	}
}

// An entry that carries a subscription's credential at an address this
// release does not ship for it is refused for the address, and keeps the
// refusal's own words: signing in would not help.
func TestAWrongAddressKeepsItsOwnRefusal(t *testing.T) {
	emptyHome(t)
	a := &App{}
	e := shippedSubscriptionEntry(t, "claude-code")
	e.Name, e.URL = "Away", "https://elsewhere.test/v1"
	probe := a.probeOne(e)
	if probe.state != probeNoCredential {
		t.Fatalf("a subscription entry at a foreign address: state %q, want %q", probe.state, probeNoCredential)
	}
	if probe.reason == signInReason("claude-code") || !strings.Contains(probe.reason, "goes only to") {
		t.Fatalf("a subscription entry at a foreign address says %q, want the address refusal's own words", probe.reason)
	}
}
