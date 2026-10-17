package app

import "testing"

func TestTheCheckboxRoutedLocalRoleGetsTheLocalWall(t *testing.T) {
	a := newProvidersApp(t)
	reg := providerRegistry{Providers: []providerEntry{
		{Name: "Local test provider", APIType: "openai", URL: "http://192.0.2.6:8081", Local: true, Models: []string{"glm"}},
		{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}},
	}}
	if _, err := saveProvidersFile(a.providersPath(), &reg); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)" // active model is REMOTE
	a.cfg.Agency.PreferLocalForRoles = true // but the checkbox is on
	a.cfg.Agency.Roles = nil                // and the role has no explicit route
	a.cfgMu.Unlock()

	// A tagged role with no explicit route: the router sends it to the
	// local entry, so the oracle must call it local.
	if !a.routeIsLocal("researcher") {
		t.Fatal("a checkbox-routed role was not recognised as local — it would be stamped the remote wall")
	}
	// With the checkbox OFF and no route, the same role follows the
	// remote active model.
	a.cfgMu.Lock()
	a.cfg.Agency.PreferLocalForRoles = false
	a.cfgMu.Unlock()
	if a.routeIsLocal("researcher") {
		t.Fatal("with the checkbox off and a remote active model, the role must not be local")
	}
}
