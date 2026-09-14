package app

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .
// .
// .
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
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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

	// .
	// .
	call1 := llm.ToolCall{ID: "p1", Type: "function"}
	call1.Function.Name = "read"
	call1.Function.Arguments = `{"file_path": "x"`
	got := app.executeToolCall(context.Background(), call1).Text
	if !strings.Contains(got, "malformed tool arguments") {
		t.Fatalf("malformed args dispatched instead of rejected; got: %s", got)
	}
	if strings.Contains(got, "no such file") || strings.Contains(got, "Error: failed to read") {
		t.Fatalf("malformed args leaked downstream: %s", got)
	}

	// .
	// .
	call2 := llm.ToolCall{ID: "p1b", Type: "function"}
	call2.Function.Name = "read"
	call2.Function.Arguments = `{"file_path": ` + strconv.Quote(filepath.Join(dir, "ok.txt")) + `}`
	argsOK := app.executeToolCall(context.Background(), call2).Text
	if strings.Contains(argsOK, "malformed") {
		t.Fatalf("well-formed args wrongly rejected: %s", argsOK)
	}
}

// .
// .
func TestEmptyToolArgsStillDispatch(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DispatchP1",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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

	// .
	// .
	call3 := llm.ToolCall{ID: "p1c", Type: "function"}
	call3.Function.Name = "recall"
	call3.Function.Arguments = `{"source": "alarms"}`
	got := app.executeToolCall(context.Background(), call3).Text
	if strings.Contains(got, "malformed") {
		t.Fatalf("valid recall args wrongly rejected: %s", got)
	}
	t.Logf("recall source=alarms returned: %s", got)
}
