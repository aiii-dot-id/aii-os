package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
func TestChildRoundCeilingCannotExceedTheOperatorsCeiling(t *testing.T) {
	for _, c := range []struct {
		name             string
		maxRounds, child int
		wantChild        int
	}{
		{"both defaulted", 0, 0, 30},
		{"operator lowered the conversation ceiling below the child default", 5, 0, 5},
		{"operator asked for more child rounds than the conversation allows", 10, 50, 10},
		{"operator asked for fewer, which is allowed", 30, 4, 4},
		{"equal is allowed", 12, 12, 12},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Agency.MaxToolRounds = c.maxRounds
			cfg.Agency.SubagentMaxToolRounds = c.child
			applyDefaults(cfg)
			if got := cfg.Agency.SubagentMaxToolRounds; got != c.wantChild {
				t.Errorf("child ceiling = %d, want %d (conversation ceiling %d)", got, c.wantChild, cfg.Agency.MaxToolRounds)
			}
			if cfg.Agency.SubagentMaxToolRounds > cfg.Agency.MaxToolRounds {
				t.Errorf("child ceiling %d exceeds the operator's %d", cfg.Agency.SubagentMaxToolRounds, cfg.Agency.MaxToolRounds)
			}
		})
	}
}

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
func TestAnExplicitCallCeilingIsUsedExactlyAsWritten(t *testing.T) {
	for _, c := range []struct {
		name      string
		rounds    int
		calls     int
		wantCalls int
	}{
		{"both defaulted", 12, 0, 64},
		{"an operator ceiling BELOW the round count is honoured", 12, 3, 3},
		{"equal to the round count", 12, 12, 12},
		{"far above the round count", 4, 64, 64},
		{"one call across many rounds", 12, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Agency.SubagentMaxToolRounds = c.rounds
			cfg.Agency.SubagentMaxToolCalls = c.calls
			applyDefaults(cfg)
			if got := cfg.Agency.SubagentMaxToolCalls; got != c.wantCalls {
				t.Errorf("call ceiling = %d, want %d (round ceiling %d) — an explicit ceiling must never be raised",
					got, c.wantCalls, cfg.Agency.SubagentMaxToolRounds)
			}
		})
	}
}

// .
// .
// .
func TestANegativeCallCeilingIsRefusedAtLoad(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"negative calls", `{"agency":{"subagent_max_tool_calls":-1}}`, "subagent_max_tool_calls"},
		{"negative rounds", `{"agency":{"subagent_max_tool_rounds":-1}}`, "subagent_max_tool_rounds"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("a negative ceiling must be refused, not defaulted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the refusal must name the key, got: %v", err)
			}
		})
	}

	// .
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"subagent_max_tool_rounds":12,"subagent_max_tool_calls":3}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agency.SubagentMaxToolCalls != 3 {
		t.Fatalf("an explicit call ceiling of 3 must survive the load, got %d", cfg.Agency.SubagentMaxToolCalls)
	}
}
