package app

import "testing"

// .
// .
// .
func TestRouteIsLocalReadsTheEntrysOwnDeclaration(t *testing.T) {
	a := newProvidersApp(t)
	reg := providerRegistry{Providers: []providerEntry{
		{Name: "GLM-5.3 (M3)", APIType: "openai", URL: "http://192.0.2.6:8081", Local: true, Models: []string{"glm"}},
		{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}},
	}}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)"
	a.cfg.Agency.Roles = map[string]RoleRoute{"flash": {Provider: "GLM-5.3 (M3)", Model: "glm"}, "cloud": {Provider: "Claude (Max/Pro)", Model: "claude-opus-5"}}
	a.cfgMu.Unlock()
	for role, want := range map[string]bool{"flash": true, "cloud": false, "": false, "no-such-role": false} {
		if got := a.routeIsLocal(role); got != want {
			t.Fatalf("routeIsLocal(%q) = %v, want %v", role, got, want)
		}
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "GLM-5.3 (M3)"
	a.cfgMu.Unlock()
	if !a.routeIsLocal("") {
		t.Fatal("an untagged spawn on a local active model must get the local wall")
	}
}
