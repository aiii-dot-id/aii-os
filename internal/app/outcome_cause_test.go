package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
// .
// .
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
				// .
				// .
				// .
				"down": {Provider: "ghost", Model: "m"},
			}},
		},
		composer: prompt.New(ring.NewManager(), 32000),
		llmSwap: newSwappableLLM(llm.New(&llm.ClientConfig{
			Endpoint: "http://active.invalid", Model: "main",
		})),
	}

	// .
	noRoute := a.resolveRunTarget("typo")
	if !noRoute.fallback {
		t.Fatalf("unrouted name should be fallback, got %+v", noRoute)
	}
	if noRoute.cause == "" {
		t.Fatal("no-route fallback carried no cause — parent cannot distinguish typo from outage; see test comment")
	}

	// .
	routeDown := a.resolveRunTarget("down")
	if !routeDown.fallback {
		t.Fatalf("unresolvable route should be fallback, got %+v", routeDown)
	}
	if routeDown.cause == noRoute.cause {
		t.Fatalf("route-down and no-route carry the same cause %q — one signal, two owners, no surprise", routeDown.cause)
	}

}

// .
// .
// .
// .
// .
func TestTheDeliveredOutcomeLineCarriesEachCause(t *testing.T) {
	seen := map[string]string{}
	for _, cause := range []string{"no-route", "route-down", "providers-unavailable"} {
		line := subagentOutcomeHeader("critic", "m1", true, cause, 4, 12, 23, 64, 6, "1200", false)
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
	// .
	ok := subagentOutcomeHeader("critic", "m1", false, "", 4, 12, 23, 64, 6, "1200", false)
	if !strings.Contains(ok, "fallback=false") || !strings.Contains(ok, `cause=""`) {
		t.Errorf("a clean run is not distinguishable from a fallback: %s", ok)
	}
	// .
	if !strings.Contains(ok, "rounds=4/12") || !strings.Contains(ok, "llm_calls=6") {
		t.Errorf("rounds and provider calls are not reported separately: %s", ok)
	}
}

// .
// .
// .
// .
// .
func TestTheOutcomeLineReportsToolCallUtilisation(t *testing.T) {
	line := subagentOutcomeHeader("worker", "m1", false, "", 4, 12, 23, 64, 6, "1200", false)
	if !strings.Contains(line, "tool_calls=23/64") {
		t.Fatalf("the outcome line must report calls used against the call ceiling: %s", line)
	}
	// .
	// .
	if !strings.Contains(line, "rounds=4/12") {
		t.Fatalf("rounds must remain distinct from calls: %s", line)
	}
	if !strings.Contains(line, "llm_calls=6") {
		t.Fatalf("provider calls must remain distinct from tool calls: %s", line)
	}
}
