package app

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"os"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestProviderEditsPreserveTopLevelRegistryMaps(t *testing.T) {
	seed := `{
  "providers": [
    {"name": "A", "api_type": "openai", "url": "https://a.example/v1", "models": ["m1"]},
    {"name": "B", "api_type": "openai", "url": "https://b.example/v1", "models": ["m2"]}
  ],
  "model_capabilities": {"sentinel-model": {"thinking": "budget"}},
  "dialect_effort_floor": {"sentinel-dialect": ["low", "high"]}
}`
	for _, tc := range []struct {
		name string
		act  func(t *testing.T, a *App)
	}{
		{"add", func(t *testing.T, a *App) {
			if err := a.setProvider(providerEntry{Name: "C", APIType: "openai", URL: "https://c.example/v1"}, false); err != nil {
				t.Fatal(err)
			}
		}},
		{"update", func(t *testing.T, a *App) {
			if err := a.setProvider(providerEntry{Name: "A", APIType: "openai", URL: "https://a2.example/v1"}, false); err != nil {
				t.Fatal(err)
			}
		}},
		{"delete", func(t *testing.T, a *App) {
			if err := a.deleteProvider("B"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newProvidersApp(t)
			if err := os.WriteFile(a.providersPath(), []byte(seed), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.act(t, a)
			raw, err := os.ReadFile(a.providersPath())
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{`"model_capabilities"`, `"sentinel-model"`, `"dialect_effort_floor"`, `"sentinel-dialect"`} {
				if !strings.Contains(string(raw), key) {
					t.Fatalf("after %s the file lost %s:\n%s", tc.name, key, raw)
				}
			}
			var back struct {
				ModelCapabilities  map[string]json.RawMessage `json:"model_capabilities"`
				DialectEffortFloor map[string][]string        `json:"dialect_effort_floor"`
			}
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatal(err)
			}
			if _, ok := back.ModelCapabilities["sentinel-model"]; !ok || len(back.DialectEffortFloor["sentinel-dialect"]) != 2 {
				t.Fatalf("after %s the decoded maps are wrong: %+v", tc.name, back)
			}
			// .
			// .
			if ok, err := fileperm.IsRestrictedToOwner(a.providersPath()); err != nil || !ok {
				t.Fatalf("the providers file is not restricted to its owner (ok=%v err=%v)", ok, err)
			}
		})
	}
}

// .
// .
func TestStrippingFillsDoesNotMutateTheSource(t *testing.T) {
	a := newProvidersApp(t)
	seed := `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code"}],
	  "model_capabilities":{"sentinel-model":{"thinking":"budget"}}}`
	if err := os.WriteFile(a.providersPath(), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	filledBefore := len(reg.Providers[0].CredentialOptions)
	if filledBefore == 0 {
		t.Fatal("the fixture did not receive embedded fills; the test cannot prove non-mutation")
	}
	out := stripEmbeddedFills(reg)
	if len(reg.Providers[0].CredentialOptions) != filledBefore {
		t.Fatalf("stripping mutated the source: %d options now, %d before", len(reg.Providers[0].CredentialOptions), filledBefore)
	}
	if _, ok := out.ModelCapabilities["sentinel-model"]; !ok {
		t.Fatal("the stripped copy lost model_capabilities")
	}
}
