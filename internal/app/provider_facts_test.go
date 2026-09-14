package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .

func loadedRegistry(t *testing.T, path string, raw string) *providerRegistry {
	t.Helper()
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func savedOptions(t *testing.T, path string, name string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var on struct {
		Providers []struct {
			Name              string            `json:"name"`
			CredentialOptions map[string]string `json:"credential_options"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(b, &on); err != nil {
		t.Fatal(err)
	}
	for _, p := range on.Providers {
		if p.Name == name {
			return p.CredentialOptions
		}
	}
	t.Fatalf("provider %q not in the saved file", name)
	return nil
}

// .
func TestSavingDoesNotClaimVendorFactsAsOperatorChoices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	reg := loadedRegistry(t, path, `{"providers":[{"name":"Claude (Max/Pro)","url":"https://api.anthropic.com","api_type":"anthropic","credential":"claude-code"}]}`)

	// .
	e := &reg.Providers[0]
	if len(e.CredentialOptions) == 0 {
		t.Skip("the embedded registry ships no options for this credential; nothing to protect")
	}
	loaned := len(e.CredentialOptions)

	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	if got := savedOptions(t, path, "Claude (Max/Pro)"); len(got) != 0 {
		t.Fatalf("saving handed %d vendor fact(s) to the operator's file as their own: %v "+
			"— a shipped correction can never replace these now", loaned, got)
	}

	// .
	// .
	again, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Providers[0].CredentialOptions) != loaned {
		t.Fatalf("the vendor facts did not come back on reload: %v", again.Providers[0].CredentialOptions)
	}
}

// .
func TestAnOperatorsOwnValueSurvivesTheSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	reg := loadedRegistry(t, path, `{"providers":[{"name":"Claude (Max/Pro)","url":"https://api.anthropic.com","api_type":"anthropic","credential":"claude-code"}]}`)
	e := &reg.Providers[0]
	if len(e.CredentialOptions) == 0 {
		t.Skip("the embedded registry ships no options for this credential")
	}
	var key string
	for k := range e.CredentialOptions {
		key = k
		break
	}
	e.CredentialOptions[key] = "the-operator-typed-this"

	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	got := savedOptions(t, path, "Claude (Max/Pro)")
	if got[key] != "the-operator-typed-this" {
		t.Fatalf("the operator's own value was stripped as if it were ours: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("saving kept vendor facts alongside the operator's one choice: %v", got)
	}
}
