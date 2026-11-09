package app

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// THE STAMPED WALL MUST MATCH THE ROUTE. resolveRunTarget sends a
// tagged role with no explicit route to the first local entry that
// answers when PreferLocalForRoles is on; routeIsLocal (the wall oracle at
// enqueue) used to miss that path and stamp the 600s remote wall on a spawn
// the router sent local — the advertised 1800s lost.
// It then took the first local entry LISTED, answering or not: a dead entry first got its limits stamped on a spawn the
// router sent to a live one after it, and with none answering a spawn the
// router sent to the remote active model was stamped the local wall.
func TestTheCheckboxRoutedLocalRoleGetsTheLocalWall(t *testing.T) {
	srv, _ := localTestServer(t, http.StatusOK, "glm")
	dead := providerEntry{Name: "Local server", APIType: "openai", URL: "http://127.0.0.1:" + strconv.Itoa(deadPort(t)) + "/v1", Local: true, DefaultModel: "seed"}
	live := providerEntry{Name: "GLM (local)", APIType: "openai", URL: srv.URL + "/v1", Local: true, DefaultModel: "glm"}
	remote := providerEntry{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}}

	a := newProvidersApp(t)
	save := func(entries ...providerEntry) {
		t.Helper()
		if _, err := saveProvidersFile(a.providersPath(), &providerRegistry{Providers: entries}); err != nil {
			t.Fatal(err)
		}
	}
	save(dead, live, remote)
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)" // active model is REMOTE
	a.cfg.Agency.PreferLocalForRoles = true // but the checkbox is on
	a.cfg.Agency.Roles = nil                // and the role has no explicit route
	a.cfgMu.Unlock()

	// A dead local entry first: the router sends the role to the live one
	// after it, so the stamp is the live one's, with the local wall.
	route := a.resolveRunTarget("researcher")
	if route.provider != live.Name {
		t.Fatalf("the router sent the role to %q, want %q", route.provider, live.Name)
	}
	if got := a.routeLimits("researcher").profile; !strings.Contains(got, live.Name) {
		t.Fatalf("the stamped limits are for %q; the router sent the role to %q", got, live.Name)
	}
	if !localWall(a, "researcher") {
		t.Fatal("a checkbox-routed role was not recognised as local — it would be stamped the remote wall")
	}

	// No local entry answers: the router uses the remote active model, and
	// the stamp follows it.
	save(dead, remote)
	if localWall(a, "researcher") {
		t.Fatal("with no local entry answering the router uses the remote active model, but the role was stamped the local wall")
	}

	// With the checkbox OFF and no route, the same role follows the
	// remote active model.
	save(dead, live, remote)
	a.cfgMu.Lock()
	a.cfg.Agency.PreferLocalForRoles = false
	a.cfgMu.Unlock()
	if localWall(a, "researcher") {
		t.Fatal("with the checkbox off and a remote active model, the role must not be local")
	}
}

// AN EXPLICIT ROUTE IS STAMPED AS THE ROUTER RESOLVES IT (0.2.1 #2, host
// 2026-11-05 23:27Z). routeLimits stamped any routed entry that merely
// existed; the router falls back to the active model when the route does
// not resolve. A local entry with no model, routed with no model, does not
// resolve ("no model"): the router runs the role on the remote active model,
// and the stamp must not be that entry's local wall.
func TestAnExplicitRouteThatDoesNotResolveIsStampedWhereTheRouterSendsIt(t *testing.T) {
	srv, _ := localTestServer(t, http.StatusOK, "glm")
	bare := providerEntry{Name: "Bare local", APIType: "openai", URL: srv.URL + "/v1", Local: true}
	live := providerEntry{Name: "GLM (local)", APIType: "openai", URL: srv.URL + "/v1", Local: true, DefaultModel: "glm"}
	remote := providerEntry{Name: "Claude (Max/Pro)", APIType: "anthropic", URL: "https://api.anthropic.com", Models: []string{"claude-opus-5"}}

	a := newProvidersApp(t)
	if _, err := saveProvidersFile(a.providersPath(), &providerRegistry{Providers: []providerEntry{bare, live, remote}}); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.cfg.LLM.Provider = "Claude (Max/Pro)" // active model is REMOTE
	a.cfg.Agency.PreferLocalForRoles = false
	a.cfg.Agency.Roles = map[string]RoleRoute{"researcher": {Provider: bare.Name}}
	a.cfgMu.Unlock()

	// Precondition on the router's own decision input (resolveRunTarget
	// falls back, cause route-down, exactly when this errors). The fallback
	// itself is not called: this harness has no active client to fall to.
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.resolveRouteLLM(a.configSnapshot(), reg, bare.Name, ""); err == nil {
		t.Fatal("precondition: a local entry with no model, routed with no model, should not resolve")
	}
	if localWall(a, "researcher") {
		t.Fatal("an explicit route that did not resolve was stamped its entry's local wall; the router ran the role on the remote active model")
	}

	// The same route with a model resolves: the router runs it on the
	// local entry, and the stamp is that entry's, with the local wall.
	a.cfgMu.Lock()
	a.cfg.Agency.Roles = map[string]RoleRoute{"researcher": {Provider: live.Name, Model: "glm"}}
	a.cfgMu.Unlock()
	if route := a.resolveRunTarget("researcher"); route.provider != live.Name {
		t.Fatalf("precondition: the router sent a resolving route to %q, want %q", route.provider, live.Name)
	}
	if got := a.routeLimits("researcher").profile; !strings.Contains(got, live.Name) {
		t.Fatalf("the stamped limits are for %q; the router sent the role to %q", got, live.Name)
	}
	if !localWall(a, "researcher") {
		t.Fatal("a resolving explicit route to a local entry was not stamped the local wall")
	}
}
