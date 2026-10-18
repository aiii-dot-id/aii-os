package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// A RUNNING IDENTITY'S METABOLISM IS HANDED ITS NEW SOURCES: the outcome
// intake, and the transcript. Each arrives by two setters — the record's outcomes to the facility, the facility to
// RHYTHM — and every test of either sets it by hand. Only a boot can show
// that the boot does: a setter nobody called is an intake that works in
// every test and never runs in a life.
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
	defer stopApp(t, app)

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
	// And the transcript, DREAM's second source, by the same two setters.
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
	// And what CONSOLIDATE keeps for its own passes after a refusal, which
	// DREAM alone is handed: unset, DREAM reads the whole queue as it did.
	owner, _ := app.timeFac.OwnerFor("dream")
	if wired, ok := owner.(interface{ HeldWired() bool }); !ok || !wired.HeldWired() {
		t.Error("THE BOOT NEVER HANDED dream WHAT CONSOLIDATION HOLDS: a batch it was refused is DREAM's again at the next tick, in a running identity only")
	}
	// A real birth's genesis carries the fingerprint the intake cites with.
	if fp, err := app.store.OwnFingerprint(); err != nil || fp == "" {
		t.Fatalf("a born identity's projection gives fingerprint %q (err %v) — the intake could cite nothing", fp, err)
	}
}
