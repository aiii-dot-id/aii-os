package app

import (
	"strings"
	"testing"
)

// .
func TestSubagentGoalStatesTheBudgetAndTheBrief(t *testing.T) {
	g := buildSubagentGoal(1, "count the beans", 30, 64, 4, 1200)
	for _, want := range []string{"[sub-agent, depth 1] Your sub-goal: count the beans", "VERDICT first", "Your budget: 30 rounds and 64 calls per leg, up to 4 leg(s), 1200 s wall per leg", "`work update steps=`", "`work update next_move=`", "Reads first"} {
		if !strings.Contains(g, want) {
			t.Fatalf("goal framing missing %q:\n%s", want, g)
		}
	}
	if !strings.Contains(buildSubagentGoal(1, "x", 12, 64, 0, 600), "up to 1 leg(s)") {
		t.Fatal("a zero leg bound reads as one leg")
	}
}
