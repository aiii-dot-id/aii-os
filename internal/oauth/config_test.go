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
	return NewOwned(kind, path, testOptions(kind, opts...))
}
