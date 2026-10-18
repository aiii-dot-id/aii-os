package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func TestRouteResolutionDistinguishesFallbackCauses(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "active", URL: "http://active.invalid", DefaultModel: "main", Default: true},
	)
	a := &App{
		cfg: &Config{
			SourcePath: filepath.Join(dir, "config.json"),
			LLM:        LLMConfig{Provider: "active", Model: "main"},
			Prompt:     PromptConfig{MaxTokens: 32000},
			Agency: AgencyConfig{Roles: map[string]RoleRoute{
				// A route that exists in config but points at a
				// provider that does not exist in the registry —
				// resolves to route-down, not no-route.
				"down": {Provider: "ghost", Model: "m"},
			}},
		},
		composer: prompt.New(ring.NewManager(), 32000),
		llmSwap: newSwappableLLM(llm.New(&llm.ClientConfig{
			Endpoint: "http://active.invalid", Model: "main",
		})),
	}

	// Cause 1: the name the parent typed is not in agency.roles.
	noRoute := a.resolveRunTarget("typo")
	if !noRoute.fellBack() {
		t.Fatalf("unrouted name should be fallback, got %+v", noRoute)
	}
	if noRoute.cause == "" {
		t.Fatal("no-route fallback carried no cause — parent cannot distinguish typo from outage; see test comment")
	}

	// Cause 2: a configured route whose provider does not resolve.
	routeDown := a.resolveRunTarget("down")
	if !routeDown.fellBack() {
		t.Fatalf("unresolvable route should be fallback, got %+v", routeDown)
	}
	if routeDown.cause == noRoute.cause {
		t.Fatalf("route-down and no-route carry the same cause %q — one signal, two owners, no surprise", routeDown.cause)
	}

}

// The PARENT reads the outcome line, and the cause is its only way to
// tell a mistyped role from a configured route that is down from a
// provider outage. Those need different responses — fix the spawn call,
// report the route, wait for the provider — so the three must arrive
// distinguishable, in the line that is actually delivered.
func TestTheDeliveredOutcomeLineCarriesEachCause(t *testing.T) {
	seen := map[string]string{}
	for _, cause := range []string{"no-route", "route-down", "providers-unavailable"} {
		line := subagentOutcomeHeader("critic", "m1", cause, 4, 12, 23, 64, 6, "1200", false)
		if !strings.Contains(line, `cause="`+cause+`"`) {
			t.Errorf("the delivered line does not carry cause %q: %s", cause, line)
		}
		if !strings.Contains(line, "fallback=true") {
			t.Errorf("a fallback is not marked as one: %s", line)
		}
		if prev, dup := seen[line]; dup {
			t.Errorf("two causes produce the same line — the parent cannot tell them apart:\n  %s\n  %s", prev, line)
		}
		seen[line] = line
	}
	// A run that did NOT fall back says so, and carries no cause.
	ok := subagentOutcomeHeader("critic", "m1", "", 4, 12, 23, 64, 6, "1200", false)
	if !strings.Contains(ok, "fallback=false") || !strings.Contains(ok, `cause=""`) {
		t.Errorf("a clean run is not distinguishable from a fallback: %s", ok)
	}
	// And the two budget facts stay in their own units.
	if !strings.Contains(ok, "rounds=4/12") || !strings.Contains(ok, "llm_calls=6") {
		t.Errorf("rounds and provider calls are not reported separately: %s", ok)
	}
}

func TestTheOutcomeLineReportsToolCallUtilisation(t *testing.T) {
	line := subagentOutcomeHeader("worker", "m1", "", 4, 12, 23, 64, 6, "1200", false)
	if !strings.Contains(line, "tool_calls=23/64") {
		t.Fatalf("the outcome line must report calls used against the call ceiling: %s", line)
	}
	// Rounds and calls are different quantities and must stay separately
	// readable — one round can carry a wide batch.
	if !strings.Contains(line, "rounds=4/12") {
		t.Fatalf("rounds must remain distinct from calls: %s", line)
	}
	if !strings.Contains(line, "llm_calls=6") {
		t.Fatalf("provider calls must remain distinct from tool calls: %s", line)
	}
}
