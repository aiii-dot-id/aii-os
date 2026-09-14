package identity

import (
	"context"
	"strings"
	"testing"
)

// .
// .
// .
func TestSpawnResultStatesTheBudget(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.SetAgencyLimits(2, 2, 20, 600)
	out, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "spawn", "goal": "size me"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Its budget:") {
		t.Fatalf("no budget stated yet, none should be quoted: %s", out)
	}
	engine.SetSpawnBudget(30, 64, 4)
	out, err = engine.ExecuteAction(context.Background(), "verb", "work", map[string]interface{}{"action": "spawn", "goal": "size me too"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Its budget: 30 rounds and 64 calls per leg, up to 4 leg(s), 600 s wall per leg", "wakes you to harvest", "record this ID in your plan"} {
		if !strings.Contains(out, want) {
			t.Fatalf("spawn result missing %q:\n%s", want, out)
		}
	}
}
