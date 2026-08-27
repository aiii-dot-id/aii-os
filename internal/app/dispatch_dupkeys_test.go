package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// newDupKeysApp builds a live app whose tool root is a temp dir, plus the
// path of a real file inside it.
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
			Name: "DispatchDup", KeyPath: filepath.Join(dir, "identity.sec"),
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

// The corrupted-emission channel no other seam can see (forensics
// 2026-08-24): a tool call whose argument object repeats a key is VALID
// JSON, and encoding/json keeps the LAST copy — so the call dispatches on
// whatever landed last. The parse seam parses it, schema validation finds
// the key present, and target-miss telemetry fires only when the surviving
// value also fails to exist.
//
// Policy, measured rather than assumed (field scan, 2036 production calls:
// 21 repeated a key, 13 drifted, 8 restated the same value verbatim):
// COUNT every repetition, REFUSE only disagreement. This test pins both
// halves, because collapsing them in either direction has a real cost —
// refuse-all blocks 8 unambiguous calls in 21, observe-all lets 13
// dispatch on a value nobody chose.
func TestDuplicateArgKeysCountedAtDispatch(t *testing.T) {
	app, _, real := newDupKeysApp(t)

	if got := app.toolReg.DuplicateArgKeyCount(); got != 0 {
		t.Fatalf("counter started at %d, want 0", got)
	}

	// A clean call must not touch the counter — a counter that counts
	// healthy traffic tells the operator nothing.
	clean := readCall("d0", `{"file_path":"`+real+`"}`)
	if out := app.executeToolCall(context.Background(), clean); !strings.Contains(out, "landed") {
		t.Fatalf("clean read failed: %s", out)
	}
	if got := app.toolReg.DuplicateArgKeyCount(); got != 0 {
		t.Fatalf("clean call incremented the counter to %d", got)
	}

	// Agreement: the key restated with the SAME value. Counted, because
	// the repetition is still evidence of a degraded emission — but it
	// MUST still execute. This is the no-side-effect guarantee: 8 of the
	// 21 field cases look exactly like this, and every one of them named
	// the file its author meant.
	agree := readCall("d1", `{"file_path":"`+real+`","file_path":"`+real+`"}`)
	out := app.executeToolCall(context.Background(), agree)
	if !strings.Contains(out, "landed") {
		t.Fatalf("agreeing duplicate was not executed — refusal must fire on disagreement only: %s", out)
	}
	if got := app.toolReg.DuplicateArgKeyCount(); got != 1 {
		t.Fatalf("agreeing duplicate left counter at %d, want 1", got)
	}
}

// Disagreement is the harmful shape: the value that executes is not the
// value that was stated first, and the tool's own "not found" then reads
// exactly like a fact about the world. Refuse it, name it, and execute
// nothing.
func TestConflictingArgKeysRefusedAtDispatch(t *testing.T) {
	app, dir, real := newDupKeysApp(t)

	ghost := filepath.Join(dir, "ghost.txt")
	conflict := readCall("d2", `{"file_path":"`+real+`","file_path":"`+ghost+`"}`)
	out := app.executeToolCall(context.Background(), conflict)

	if got := app.toolReg.DuplicateArgKeyCount(); got != 1 {
		t.Fatalf("conflicting call left counter at %d, want 1", got)
	}
	// Nothing executed: neither the stated value nor the drifted one.
	if strings.Contains(out, "landed") {
		t.Fatalf("refusal still read the FIRST copy — nothing may execute: %s", out)
	}
	if !strings.Contains(out, "NOTHING was executed") {
		t.Fatalf("refusal must say plainly that nothing ran, got: %s", out)
	}
	// The refusal must name the offending key and the recovery, or it
	// teaches nothing and the next emission repeats the mistake.
	if !strings.Contains(out, "file_path") {
		t.Fatalf("refusal must name the conflicting key, got: %s", out)
	}
	if !strings.Contains(out, "Reissue") {
		t.Fatalf("refusal must name the recovery, got: %s", out)
	}
	// It must not be mistaken for the parse seam's error: that one means
	// invalid JSON, this one means valid JSON that disagrees with itself.
	if strings.Contains(out, "malformed tool arguments") {
		t.Fatalf("conflict was reported as a parse failure: %s", out)
	}
}
