package oauth

import (
	"encoding/json"
	configdir "github.com/aiii-dot-id/aii-os/config"
)

func testConfig() (map[string]Provider, map[string]map[string]string) {
	var reg struct {
		OAuth     map[string]Provider `json:"oauth"`
		Providers []struct {
			Credential string            `json:"credential"`
			Options    map[string]string `json:"credential_options"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(configdir.Providers, &reg); err != nil {
		panic(err)
	}
	opts := map[string]map[string]string{}
	for _, p := range reg.Providers {
		opts[p.Credential] = p.Options
	}
	return reg.OAuth, opts
}
func testCatalog() map[string]Provider { c, _ := testConfig(); return c }
func testOptions(kind string, overrides ...map[string]string) map[string]string {
	_, opts := testConfig()
	out := copyMap(opts[kind])
	if out == nil {
		out = map[string]string{}
	}
	for _, m := range overrides {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
func testSource(kind string, opts ...map[string]string) (*Source, error) {
	return New(kind, testOptions(kind, opts...))
}
func testOwned(kind, path string, opts ...map[string]string) (*Source, error) {
	return NewOwnedConfigured(kind, path, testContract(kind), testOptions(kind, opts...))
}

// testContract is the shipped contract a credential kind is bound to, as
// the app binds it: an owned source refreshes through it, and its
// token_headers are the only User-Agent an OAuth request carries.
func testContract(kind string) OAuthParams {
	var reg struct {
		Providers []struct {
			Credential string `json:"credential"`
			OAuth      string `json:"oauth"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(configdir.Providers, &reg); err != nil {
		panic(err)
	}
	for _, p := range reg.Providers {
		if p.Credential == kind && p.OAuth != "" {
			return testCatalog()[p.OAuth].Params()
		}
	}
	return OAuthParams{TokenHeaders: testTokenHeaders()}
}

// testTokenHeaders is what the shipped contracts send to an authority,
// read from providers.json: a fixture contract carries the configured
// User-Agent the way every shipped one does, never a value of its own.
func testTokenHeaders() map[string]string {
	return copyMap(testCatalog()["openai-chatgpt"].TokenHeaders)
}
