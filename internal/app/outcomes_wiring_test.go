package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .
// .
// .
// .
func TestABootHandsMetabolismTheOutcomeIntake(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "OutcomesWiring",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://x", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = cfgPath
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	for _, name := range []string{"consolidate", "rhythm"} {
		owner, ok := app.timeFac.OwnerFor(name)
		if !ok {
			t.Fatalf("%s is not registered with TIME", name)
		}
		wired, ok := owner.(interface{ OutcomesWired() bool })
		if !ok {
			t.Fatalf("%s cannot say whether it was handed the outcome intake", name)
		}
		if !wired.OutcomesWired() {
			t.Errorf("THE BOOT NEVER HANDED %s THE OUTCOME INTAKE: what the identity finishes and drops is unread again, in a running identity only", name)
		}
	}
	// .
	for _, name := range []string{"dream", "rhythm"} {
		owner, _ := app.timeFac.OwnerFor(name)
		wired, ok := owner.(interface{ ConversationWired() bool })
		if !ok {
			t.Fatalf("%s cannot say whether it was handed the transcript", name)
		}
		if !wired.ConversationWired() {
			t.Errorf("THE BOOT NEVER HANDED %s THE TRANSCRIPT: what was said is unread again, in a running identity only", name)
		}
	}
	// .
	if fp, err := app.store.OwnFingerprint(); err != nil || fp == "" {
		t.Fatalf("a born identity's projection gives fingerprint %q (err %v) — the intake could cite nothing", fp, err)
	}
}
