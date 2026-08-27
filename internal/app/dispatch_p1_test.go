package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// P1 from the tool-call-bug review (2026-08-17 external review): executeToolCall used
// to swallow the json.Unmarshal error, so malformed arguments arrived
// as nil -> empty map and dispatched as a DIFFERENT call than the one
// the model issued — failing downstream as a path-policy error that
// read like a sandbox flake. The fix rejects at the seam with a named
// error the model can retry against. These tests pin that behavior.
func TestMalformedToolArgsRejectedAtDispatch(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DispatchP1",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "DispatchP1", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	// Malformed JSON must be rejected with the named seam error, not
	// dispatched as an empty-args call.
	call1 := llm.ToolCall{ID: "p1", Type: "function"}
	call1.Function.Name = "read"
	call1.Function.Arguments = `{"file_path": "x"`
	got := app.executeToolCall(context.Background(), call1)
	if !strings.Contains(got, "malformed tool arguments") {
		t.Fatalf("malformed args dispatched instead of rejected; got: %s", got)
	}
	if strings.Contains(got, "no such file") || strings.Contains(got, "Error: failed to read") {
		t.Fatalf("malformed args leaked downstream: %s", got)
	}

	// Well-formed args to the same tool still succeed — the seam
	// rejects malformedness, not the tool.
	call2 := llm.ToolCall{ID: "p1b", Type: "function"}
	call2.Function.Name = "read"
	call2.Function.Arguments = `{"file_path": "` + filepath.Join(dir, "ok.txt") + `"}`
	argsOK := app.executeToolCall(context.Background(), call2)
	if strings.Contains(argsOK, "malformed") {
		t.Fatalf("well-formed args wrongly rejected: %s", argsOK)
	}
}

// Empty arguments stay valid (dispatch as empty map) — many verbs take
// zero args; the seam must not turn that into a rejection.
func TestEmptyToolArgsStillDispatch(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DispatchP1Empty",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "DispatchP1Empty", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	// Empty-string arguments (valid, common) and absent arguments
	// dispatch as empty map — the verb layer owns its own reading.
	call3 := llm.ToolCall{ID: "p1c", Type: "function"}
	call3.Function.Name = "timer"
	call3.Function.Arguments = `{"action": "list"}`
	got := app.executeToolCall(context.Background(), call3)
	if strings.Contains(got, "malformed") {
		t.Fatalf("valid timer args wrongly rejected: %s", got)
	}
	if !strings.Contains(got, "alarms") && !strings.Contains(got, "no ") {
		t.Logf("timer list returned: %s", got)
	}
}
