package app

import "testing"

// The wall's notion of "local" is the entry's own Local flag — the one
// PreferLocalForRoles already routes by — for a tagged role's route and
// for the active model when the spawn is untagged.
func TestRouteIsLocalReadsTheEntrysOwnDeclaration(t *testing.T) {
	a := newProvidersApp(t)
	reg := providerRegistry{Providers: []providerEntry{
		{Name: "Local test provider", APIType: "openai", URL: "http://192.0.2.6:8081", Local: true, Models: []string{"glm"}},
		{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}},
	}}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)"
	a.cfg.Agency.Roles = map[string]RoleRoute{"local": {Provider: "Local test provider", Model: "glm"}, "cloud": {Provider: "Claude (Max/Pro)", Model: "claude-opus-5"}}
	a.cfgMu.Unlock()
	for role, want := range map[string]bool{"local": true, "cloud": false, "": false, "no-such-role": false} {
		if got := a.routeIsLocal(role); got != want {
			t.Fatalf("routeIsLocal(%q) = %v, want %v", role, got, want)
		}
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Local test provider"
	a.cfgMu.Unlock()
	if !a.routeIsLocal("") {
		t.Fatal("an untagged spawn on a local active model must get the local wall")
	}
}
