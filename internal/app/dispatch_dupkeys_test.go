package app

import (
	"context"
	"os"
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
func newDupKeysApp(t *testing.T) (*App, string, string) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DispatchDup",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{
			KeyPath:    filepath.Join(dir, "identity.sec"),
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
	t.Cleanup(app.Stop)

	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("landed"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return app, dir, real
}

func readCall(id, args string) llm.ToolCall {
	tc := llm.ToolCall{ID: id, Type: "function"}
	tc.Function.Name = "read"
	tc.Function.Arguments = args
	return tc
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
// .
// .
// .
// .
// .
// .
func TestDuplicateArgKeysCountedAtDispatch(t *testing.T) {
	app, _, real := newDupKeysApp(t)

	if got := app.toolReg.DuplicateArgKeyCount(); got != 0 {
		t.Fatalf("counter started at %d, want 0", got)
	}

	// .
	// .
	clean := readCall("d0", `{"file_path":`+strconv.Quote(real)+`}`)
	if out := app.executeToolCall(context.Background(), clean).Text; !strings.Contains(out, "landed") {
		t.Fatalf("clean read failed: %s", out)
	}
	if got := app.toolReg.DuplicateArgKeyCount(); got != 0 {
		t.Fatalf("clean call incremented the counter to %d", got)
	}

	// .
	// .
	// .
	// .
	// .
	agree := readCall("d1", `{"file_path":`+strconv.Quote(real)+`,"file_path":`+strconv.Quote(real)+`}`)
	out := app.executeToolCall(context.Background(), agree).Text
	if !strings.Contains(out, "landed") {
		t.Fatalf("agreeing duplicate was not executed — refusal must fire on disagreement only: %s", out)
	}
	if got := app.toolReg.DuplicateArgKeyCount(); got != 1 {
		t.Fatalf("agreeing duplicate left counter at %d, want 1", got)
	}
}

// .
// .
// .
// .
func TestConflictingArgKeysRefusedAtDispatch(t *testing.T) {
	app, dir, real := newDupKeysApp(t)

	ghost := filepath.Join(dir, "ghost.txt")
	conflict := readCall("d2", `{"file_path":`+strconv.Quote(real)+`,"file_path":`+strconv.Quote(ghost)+`}`)
	out := app.executeToolCall(context.Background(), conflict).Text

	if got := app.toolReg.DuplicateArgKeyCount(); got != 1 {
		t.Fatalf("conflicting call left counter at %d, want 1", got)
	}
	// .
	if strings.Contains(out, "landed") {
		t.Fatalf("refusal still read the FIRST copy — nothing may execute: %s", out)
	}
	if !strings.Contains(out, "NOTHING was executed") {
		t.Fatalf("refusal must say plainly that nothing ran, got: %s", out)
	}
	// .
	// .
	if !strings.Contains(out, "file_path") {
		t.Fatalf("refusal must name the conflicting key, got: %s", out)
	}
	if !strings.Contains(out, "Reissue") {
		t.Fatalf("refusal must name the recovery, got: %s", out)
	}
	// .
	// .
	if strings.Contains(out, "malformed tool arguments") {
		t.Fatalf("conflict was reported as a parse failure: %s", out)
	}
}
