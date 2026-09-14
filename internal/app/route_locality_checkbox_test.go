package app

import "testing"

// .
// .
// .
// .
// .
func TestTheCheckboxRoutedLocalRoleGetsTheLocalWall(t *testing.T) {
	a := newProvidersApp(t)
	reg := providerRegistry{Providers: []providerEntry{
		{Name: "GLM (M3)", APIType: "openai", URL: "http://192.0.2.6:8081", Local: true, Models: []string{"glm"}},
		{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}},
	}}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)"
	a.cfg.Agency.PreferLocalForRoles = true
	a.cfg.Agency.Roles = nil
	a.cfgMu.Unlock()

	// .
	// .
	if !a.routeIsLocal("researcher") {
		t.Fatal("a checkbox-routed role was not recognised as local — it would be stamped the remote wall")
	}
	// .
	// .
	a.cfgMu.Lock()
	a.cfg.Agency.PreferLocalForRoles = false
	a.cfgMu.Unlock()
	if a.routeIsLocal("researcher") {
		t.Fatal("with the checkbox off and a remote active model, the role must not be local")
	}
}
