package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
func TestSafeModeBootsReadOnly(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SafeTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	raw, err := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `{"content":"`)
	if i < 0 {
		t.Fatalf("no content hash in ledger line 1 (%d lines, first %.80q)", len(lines), l0)
	}
	j := i + len(`{"content":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	l0 = l0[:j] + string(flip) + l0[j+1:]
	lines[0] = l0
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	kp, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.VerifyChain(filepath.Join(dir, "ledger.jsonl"), kp.PublicKeyBytes(), nil); err == nil {
		t.Fatal("corruption did not land — the flip was a no-op")
	}

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must succeed read-only, not die: %v", err)
	}
	defer app.Stop()

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("must be in SAFE with the chain reason, got %q %v", reason, ok)
	}

	// .
	// .
	// .
	turnsBefore, _ := app.store.ConversationTurnCount()
	if err := app.engine.RecordConversationTurn("operator", "are you there?"); err != nil {
		t.Fatalf("read-only conversation must work: %v", err)
	}
	turnsAfter, _ := app.store.ConversationTurnCount()
	if turnsAfter != turnsBefore {
		t.Fatal("SAFE conversation must not write the store — transient only")
	}
	if st := app.engine.SafeTranscript(); len(st) != 1 || st[0].Content != "are you there?" {
		t.Fatalf("SAFE turn must land in the transient transcript, got %+v", st)
	}

	// .
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{"content": "x"}); err == nil {
		t.Fatal("note must refuse in SAFE")
	}
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{"variant": "intention.create", "statement": "x"}); err == nil {
		t.Fatal("commit must refuse in SAFE")
	}

	// .
	// .
	if res, _ := app.toolReg.Execute(context.Background(), "shell", map[string]interface{}{"command": "true"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("shell must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "write", map[string]interface{}{"file_path": "x.txt", "content": "y"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("write must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "ls", map[string]interface{}{}); strings.Contains(res.Error, "safe mode") {
		t.Fatalf("read-only diagnostic surface must continue in SAFE, got %+v", res)
	}

	// .
	if _, err := app.store.ClaimWork(nil, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// .
	app.witnessAttempt(true)
	if _, ok := app.SafeMode(); !ok {
		t.Fatal("SAFE never self-exits — witness recovery must not clear it")
	}
}

func TestStartLiveFailureReleasesLedger(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "FailedStart")
	cfg := safebootConfig(t, dir, "FailedStart", keyPath, ledgerPath, dbPath)
	cfg.LLM.Provider = "missing"

	if err := startLiveForTest(New(cfg)); err == nil {
		t.Fatal("invalid provider unexpectedly started")
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatalf("failed startup retained the ledger lock: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
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
func TestSafeModeSignatureCorruptionFreezesMinting(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SafeTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// .
	raw, _ := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `"sig":"`)
	if i < 0 {
		t.Fatal("no signature field in birth event")
	}
	j := i + len(`"sig":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	l0 = l0[:j] + string(flip) + l0[j+1:]
	lines[0] = l0
	os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(strings.Join(lines, "\n")), 0640)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must succeed read-only, not die: %v", err)
	}
	defer app.Stop()

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("signature corruption must enter SAFE at boot, got %q %v", reason, ok)
	}

	// .
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{"content": "x"}); err == nil {
		t.Fatal("note MINTED under a signature-corrupted chain — the SAFE freeze is decorative again")
	}

	// .
	seqBefore := app.ledger.LastSeq()
	if _, err := app.ledger.Append(ledger.EventExperienceCreate, "SigTest", 4,
		map[string]interface{}{"content": "bypass attempt"}, app.keyPair); err == nil {
		t.Fatal("direct Append succeeded under SAFE — the ledger freeze is missing")
	}
	if app.ledger.LastSeq() != seqBefore {
		t.Fatal("ledger advanced under SAFE")
	}
}

// .
func TestDegradedWitnessDetection(t *testing.T) {
	a := &App{}
	a.witnessAttempt(false)
	a.witnessAttempt(false)
	if _, degraded := a.DegradedWitnessSince(); degraded {
		t.Fatal("2 failures must not degrade yet")
	}
	a.witnessAttempt(false)
	if _, ok := a.DegradedWitnessSince(); !ok {
		t.Fatal("3 consecutive failures = DEGRADED(witness)")
	}
	a.witnessAttempt(true)
	if _, ok := a.DegradedWitnessSince(); ok {
		t.Fatal("success must clear DEGRADED")
	}
	// .
	a.enterSafe("test")
	a.witnessAttempt(true)
	if _, ok := a.SafeMode(); !ok {
		t.Fatal("SAFE never self-exits")
	}
	a.resetModeForTest()
}

// .
func TestSafeReasonIsFirst(t *testing.T) {
	a := &App{}
	a.enterSafe("original cause")
	a.enterSafe("later different cause")
	a.enterSafe("later integrity trigger")
	reason, ok := a.SafeMode()
	if !ok || reason != "original cause" {
		t.Fatalf("the first SAFE reason is the cause of record, got %q", reason)
	}
	a.resetModeForTest()
}
