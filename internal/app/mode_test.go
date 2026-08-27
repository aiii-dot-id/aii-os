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

// SAFE MODE, end to end: a corrupted ledger boots READ-ONLY (not dead)
// — conversation records to the store, note/commit refuse honestly,
// the queue freezes, and the mode never self-exits.
func TestSafeModeBootsReadOnly(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SafeTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// Corrupt the chain: tamper the birth event's payload hash binding by
	// flipping a hex char of the content hash in line 1. GUARDED flip
	// (2026-08-18 flake, ~90min of ghost-hunting distilled): the old code
	// set the first hex char to "0" unconditionally — a 1-in-16 NO-OP
	// when that nibble already was zero, and the test then demanded SAFE
	// from a perfectly healthy chain. Corruption tests must assert their
	// corruption LANDED.
	raw, err := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `"content_hash":"`)
	if i < 0 {
		t.Fatalf("no content_hash in ledger line 1 (%d lines, first %.80q)", len(lines), l0)
	}
	j := i + len(`"content_hash":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	l0 = l0[:j] + string(flip) + l0[j+1:]
	lines[0] = l0
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	// Precondition: the corruption is on disk and the chain really fails
	// before the path goes anywhere near startLive.
	kp, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.VerifyChain(filepath.Join(dir, "ledger.jsonl"),
		map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err == nil {
		t.Fatal("corruption did not land — the flip was a no-op")
	}

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "SafeTest", KeyPath: filepath.Join(dir, "identity.sec"),
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

	// Conversation survives — TRANSIENTLY (canon IDENTITY_SEMANTICS §10:
	// no database writes while integrity is unverified; SAFE_DEGRADED P3:
	// "read-only and transient"). The turn is held in memory, never stored.
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

	// Verbs refuse honestly.
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{"content": "x"}); err == nil {
		t.Fatal("note must refuse in SAFE")
	}
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "commit", map[string]interface{}{"variant": "intention.create", "statement": "x"}); err == nil {
		t.Fatal("commit must refuse in SAFE")
	}

	// Canon §10: mutation and outside-world tools are disabled in SAFE;
	// the read-only diagnostic surface continues.
	if res, _ := app.toolReg.Execute(context.Background(), "shell", map[string]interface{}{"command": "true"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("shell must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "write", map[string]interface{}{"file_path": "x.txt", "content": "y"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("write must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "ls", map[string]interface{}{}); strings.Contains(res.Error, "safe mode") {
		t.Fatalf("read-only diagnostic surface must continue in SAFE, got %+v", res)
	}

	// The queue is frozen (forensic snapshot).
	if _, err := app.store.ClaimWork(nil, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// And it stays SAFE — no self-exit API even exists.
	app.witnessAttempt(true) // witness recovery must NOT exit SAFE
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

// S1 regression (2026-08-17 external review): SIGNATURE-only corruption.
// The content-hash test above passes even with a broken SAFE wire,
// because the tail check independently rejects a content_hash mismatch
// at append time. A flipped signature is invisible to the tail check
// (it validates seq/prev_hash/content_hash only) — the ONLY thing
// standing between it and a fresh mint is SAFE actually being applied.
// Before the fix, enterSafe ran while store/engine were nil and this
// exact scenario kept minting behind the SAFE banner.
func TestSafeModeSignatureCorruptionFreezesMinting(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SigTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// Corrupt ONLY the signature of line 1: hashes and linkage stay valid.
	raw, _ := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `"signature":"`)
	if i < 0 {
		t.Fatal("no signature field in birth event")
	}
	j := i + len(`"signature":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	l0 = l0[:j] + string(flip) + l0[j+1:]
	lines[0] = l0
	os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(strings.Join(lines, "\n")), 0640)

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "SigTest", KeyPath: filepath.Join(dir, "identity.sec"),
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

	// The verbs must refuse — the engine actually carries SAFE now.
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]interface{}{"content": "x"}); err == nil {
		t.Fatal("note MINTED under a signature-corrupted chain — the SAFE freeze is decorative again")
	}

	// And the ledger itself refuses, for any caller that skips the verbs.
	seqBefore := app.ledger.LastSeq()
	if _, err := app.ledger.Append(ledger.EventExperienceCreate, "SigTest", 4,
		map[string]interface{}{"content": "bypass attempt"}, app.keyPair); err == nil {
		t.Fatal("direct Append succeeded under SAFE — the ledger freeze is missing")
	}
	if app.ledger.LastSeq() != seqBefore {
		t.Fatal("ledger advanced under SAFE")
	}
}

// DEGRADED(witness): 3 consecutive failures enter, success exits, SAFE dominates.
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
	// SAFE dominates: once safe, witness news changes nothing.
	a.enterSafe("test")
	a.witnessAttempt(true)
	if _, ok := a.SafeMode(); !ok {
		t.Fatal("SAFE never self-exits")
	}
	a.resetModeForTest() // stop the test's beacon goroutine
}

// The FIRST reason stands: a later trigger must not rewrite the record.
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
