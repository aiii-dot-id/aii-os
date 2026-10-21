package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
)

// routedApp is a live identity on the provider "test" whose role "bench"
// routes to a second entry, "Bench", with overrides for both.
func routedApp(t *testing.T, overrides string) *App {
	t.Helper()
	app, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, app) })
	writeTestProviders(t, dir,
		providerEntry{Name: "test", APIType: "openai", URL: "https://x", APIKey: "sk-x", DefaultModel: "m", Default: true},
		providerEntry{Name: "Bench", APIType: "openai", URL: "http://127.0.0.1:1/v1", APIKey: "sk-b", DefaultModel: "bench-1", Models: []string{"bench-1"}})
	app.cfgMu.Lock()
	app.cfg.Agency.Roles = map[string]RoleRoute{"bench": {Provider: "Bench", Model: "bench-1"}, "lost": {Provider: "Missing", Model: "x"}}
	app.cfg.Overrides = configWith(t, overrides).Overrides
	app.cfgMu.Unlock()
	return app
}

// A ROUTED CHILD RUNS ON ITS MODEL'S LIMITS: its rounds, calls, legs, fence,
// tool-result size and wall are those of the entry and model its role
// routes to, with their overrides, not the identity's.
func TestARoutedChildRunsOnItsModelsLimits(t *testing.T) {
	app := routedApp(t, `{"Bench":{"agency":{"subagent_max_tool_calls":"unlimited","subagent_max_legs":9,"turn_token_budget":1234,"subagent_wall_seconds":7200},"models":{"bench-1":{"agency":{"subagent_max_tool_rounds":7},"prompt":{"max_tool_result_chars":555}}}}}`)
	target := app.resolveRunTarget("bench")
	if target.fellBack() || target.provider != "Bench" {
		t.Fatalf("rig: the role did not route: %+v", target.cause)
	}
	l := target.limits
	if l.Value(subCallsSetting) != limits.Unlimited || l.Value(subLegsSetting) != limits.Of(9) || l.Value(turnTokenBudgetSetting) != limits.Of(1234) ||
		l.Value(subRoundsSetting) != limits.Of(7) || l.N(toolResultCharsSetting) != 555 || l.N(subWallSetting) != 7200 {
		t.Fatalf("the routed child's limits: calls %s, legs %s, fence %s, rounds %s, result %d, wall %d",
			l.Value(subCallsSetting), l.Value(subLegsSetting), l.Value(turnTokenBudgetSetting), l.Value(subRoundsSetting), l.N(toolResultCharsSetting), l.N(subWallSetting))
	}
}

// A CHILD THAT FALLS BACK RUNS ON THE ACTIVE MODEL'S LIMITS: a route that
// does not resolve sends the child to the identity's own model, and its
// limits go with it.
func TestAChildThatFallsBackRunsOnTheActiveModelsLimits(t *testing.T) {
	app := routedApp(t, `{"test":{"agency":{"subagent_max_tool_calls":9}},"Bench":{"agency":{"subagent_max_tool_calls":99}}}`)
	target := app.resolveRunTarget("lost")
	if !target.fellBack() {
		t.Fatalf("rig: a route to a missing entry did not fall back")
	}
	if got := target.limits.Value(subCallsSetting); got != limits.Of(9) {
		t.Fatalf("a child that fell back to the active model holds %s calls; want the active model's 9", got)
	}
}

// THE LEASE IS STAMPED FROM THE ROUTE'S WALL, AND THE SPAWN RESULT STATES THE
// ROUTED MODEL'S BUDGET: one reading of the route (Ports.RouteLimits) gives
// the wall the request carries — the lease is that wall and a minute — and
// the budget the result tells the parent, in the approved words for a
// lifted limit.
func TestTheSpawnResultStatesTheRoutedModelsBudget(t *testing.T) {
	app := routedApp(t, `{"Bench":{"agency":{"subagent_max_tool_calls":"unlimited","subagent_wall_seconds":7200,"subagent_max_legs":2}}}`)
	if got := app.spawnLimitsFor("bench"); got.WallSeconds != 7200 || got.Calls != limits.Unlimited || got.Legs != limits.Of(2) {
		t.Fatalf("the route's spawn limits: %+v", got)
	}
	if got := app.spawnLimitsFor(""); got.WallSeconds != 1200 || got.Calls != limits.Of(64) {
		t.Fatalf("an untagged spawn's limits are not the identity's own model's: %+v", got)
	}
	out, err := app.engine.ExecuteAction(app.lifetime(), "verb", "work", map[string]any{"action": "spawn", "goal": "bench it", "role": "bench"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Its budget: 30 rounds and unlimited calls per leg, up to 2 leg(s), 7200 s wall per leg"; !strings.Contains(out, want) {
		t.Fatalf("the spawn result does not state the routed model's budget %q:\n%s", want, out)
	}
}

// THE CHILD'S HEADER AND GOAL SAY "UNLIMITED" WHERE A LIMIT IS LIFTED, in the
// approved form; at numbers they are the text they were.
func TestTheChildHeaderSaysUnlimited(t *testing.T) {
	goal := buildSubagentGoal(1, "count", limits.Unlimited, limits.Unlimited, limits.Unlimited, 7200)
	if want := "Your budget: unlimited rounds and unlimited calls per leg, up to unlimited leg(s), 7200 s wall per leg."; !strings.Contains(goal, want) {
		t.Fatalf("the goal does not say %q:\n%s", want, goal)
	}
	header := subagentOutcomeHeader("worker", "m", "", 3, limits.Unlimited, 40, limits.Unlimited, 5, "100", false)
	if !strings.Contains(header, "rounds=3/unlimited tool_calls=40/unlimited") {
		t.Fatalf("the header does not say the lifted limits: %s", header)
	}
	if got := subagentOutcomeHeader("worker", "m", "", 3, limits.Of(12), 40, limits.Of(64), 5, "100", false); !strings.Contains(got, "rounds=3/12 tool_calls=40/64") {
		t.Fatalf("at numbers the header changed: %s", got)
	}
}
