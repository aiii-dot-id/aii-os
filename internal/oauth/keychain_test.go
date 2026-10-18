package oauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The item name is a VENDOR FACT and rides credential_options, so a
// vendor that renames it is a table edit rather than a release.
func TestTheKeychainItemIsACredentialOption(t *testing.T) {
	sp, err := specFor(KindClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	sp, err = applyOverrides(sp, map[string]string{"keychain_service": "Some Item"})
	if err != nil {
		t.Fatalf("keychain_service was refused: %v", err)
	}
	if sp.keychain != "Some Item" {
		t.Fatalf("keychain = %q, want %q", sp.keychain, "Some Item")
	}
	// The option table still refuses what it does not know: adding one
	// name must not have opened the door to any name.
	if _, err := applyOverrides(sp, map[string]string{"keychain_servce": "typo"}); err == nil {
		t.Fatal("a misspelled option was accepted")
	}
}

// The shipped registry names the item Claude Code actually uses, so an
// operator who never edits anything is served.
func TestTheShippedClaudeEntryNamesItsKeychainItem(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "providers.json"))
	if err != nil {
		t.Skipf("registry not readable from here: %v", err)
	}
	var reg struct {
		Providers []struct {
			Name              string            `json:"name"`
			Credential        string            `json:"credential"`
			CredentialOptions map[string]string `json:"credential_options"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range reg.Providers {
		if p.Credential != KindClaudeCode {
			continue
		}
		found = true
		if got := p.CredentialOptions["keychain_service"]; got != "Claude Code-credentials" {
			t.Errorf("%s keychain_service = %q, want %q", p.Name, got, "Claude Code-credentials")
		}
	}
	if !found {
		t.Fatal("no shipped provider adopts the claude-code credential")
	}
}

// A file that exists is answered from the file on EVERY platform: it
// costs nothing and needs no approval.
func TestAFileThatExistsIsReadWithoutTheKeychain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := adoptedBytes(t.Context(), "Some Item", path)
	if err != nil || !strings.Contains(string(got), "from\":\"file") {
		t.Fatalf("got %q, err %v — the file must win", got, err)
	}
}

// With no item configured, an absent file is an absent file — the error
// must stay os.IsNotExist so the caller can still say "not signed in".
func TestAnAbsentFileWithNoItemStaysNotExist(t *testing.T) {
	_, err := adoptedBytes(t.Context(), "", filepath.Join(t.TempDir(), "nope.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want a not-exist error", err)
	}
}
