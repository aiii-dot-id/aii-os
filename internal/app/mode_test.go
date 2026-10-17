package app

import (
	"context"
	"database/sql"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
	"net/url"
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
	// Precondition: the corruption is on disk and the chain really fails
	// before the path goes anywhere near startLive.
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
	defer stopApp(t, app)

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("must be in SAFE with the chain reason, got %q %v", reason, ok)
	}

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
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "x"}); err == nil {
		t.Fatal("note must refuse in SAFE")
	}
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "commit", map[string]any{"variant": "intention.create", "statement": "x"}); err == nil {
		t.Fatal("commit must refuse in SAFE")
	}

	// Canon §10: mutation and outside-world tools are disabled in SAFE;
	// the read-only diagnostic surface continues.
	if res, _ := app.toolReg.Execute(context.Background(), "shell", map[string]any{"command": "true"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("shell must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "write", map[string]any{"file_path": "x.txt", "content": "y"}); !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("write must be refused in SAFE, got %+v", res)
	}
	if res, _ := app.toolReg.Execute(context.Background(), "ls", map[string]any{}); strings.Contains(res.Error, "safe mode") {
		t.Fatalf("read-only diagnostic surface must continue in SAFE, got %+v", res)
	}

	// The queue is frozen (forensic snapshot).
	if _, err := app.store.ClaimWork(nil, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// And it stays SAFE — no self-exit API even exists.
	app.witnessAttempt(nil) // witness recovery must NOT exit SAFE
	if _, ok := app.SafeMode(); !ok {
		t.Fatal("SAFE never self-exits — witness recovery must not clear it")
	}
}

// A start that stops gives the record back. A provider that does not
// resolve used to end the start with an error; it now holds the page for a
// start that could not complete (could_not_start_test.go), and that page
// holds no record open either.
func TestStartLiveFailureReleasesLedger(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "FailedStart")
	cfg := safebootConfig(t, dir, "FailedStart", keyPath, ledgerPath, dbPath)
	cfg.LLM.Provider = "missing"

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("a provider that does not resolve must stop at the page, not end the start: %v", err)
	}
	defer stopApp(t, app)
	if reason, safe := app.SafeMode(); !safe || !strings.Contains(reason, "LLM substrate: ") {
		t.Fatalf("invalid provider unexpectedly started: safe=%v %q", safe, reason)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatalf("failed startup retained the ledger lock: %v", err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeModeSignatureCorruptionFreezesMinting(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SafeTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// Corrupt ONLY the signature of line 1: hashes and linkage stay valid.
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
	defer stopApp(t, app)

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("signature corruption must enter SAFE at boot, got %q %v", reason, ok)
	}

	// The verbs must refuse — the engine actually carries SAFE now.
	if _, err := app.engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "x"}); err == nil {
		t.Fatal("note MINTED under a signature-corrupted chain — the SAFE freeze is decorative again")
	}

	// A rejected opening must publish no writable ledger at all. Runtime
	// SAFE still freezes an already-open ledger; that bypass is exercised by
	// TestEnteringSafeFreezesTheProjection using a valid author and payload.
	if app.ledger != nil {
		t.Fatal("signature-corrupted boot published a writable ledger")
	}
	if after := lockedRead(t, cfg.Identity.LedgerPath); string(after) != strings.Join(lines, "\n") {
		t.Fatal("signature-corrupted ledger changed under SAFE")
	}
}

// DEGRADED(witness): 3 consecutive failures enter, success exits, SAFE dominates.
func TestDegradedWitnessDetection(t *testing.T) {
	a := &App{}
	a.witnessAttempt(errors.New("no answer"))
	a.witnessAttempt(errors.New("no answer"))
	if _, _, degraded := a.DegradedWitnessSince(); degraded {
		t.Fatal("2 failures must not degrade yet")
	}
	a.witnessAttempt(errors.New("no answer"))
	if _, _, ok := a.DegradedWitnessSince(); !ok {
		t.Fatal("3 consecutive failures = DEGRADED(witness)")
	}
	a.witnessAttempt(nil)
	if _, _, ok := a.DegradedWitnessSince(); ok {
		t.Fatal("success must clear DEGRADED")
	}
	// SAFE dominates: once safe, witness news changes nothing.
	a.enterSafe("test")
	a.witnessAttempt(nil)
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

func TestEnteringSafeFreezesTheProjection(t *testing.T) {
	r := newRestoreRig(t)
	a := r.boot(t)
	defer func() { a.resetModeForTest(); stopApp(t, a) }()
	if err := a.store.AddOutboxMessage("before", "operator", "", "written before SAFE", nil); err != nil {
		t.Fatalf("rig: the store refuses before any freeze: %v", err)
	}

	a.enterSafe("an integrity failure")
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("rig: the identity did not enter SAFE")
	}
	if reason, frozen := a.store.Frozen(); !frozen || reason != "an integrity failure" {
		t.Fatalf("SAFE did not freeze the projection: frozen=%v reason=%q", frozen, reason)
	}
	seqBefore := a.ledger.LastSeq()
	if _, err := a.ledger.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "safe_bypass", "content": "bypass attempt"}, a.keyPair); err == nil {
		t.Fatal("direct Append succeeded under runtime SAFE")
	}
	if a.ledger.LastSeq() != seqBefore {
		t.Fatal("ledger advanced under runtime SAFE")
	}
	err := a.store.AddOutboxMessage("during", "operator", "", "written during SAFE", nil)
	var fe *store.FrozenError
	if !errors.As(err, &fe) {
		t.Fatalf("A MESSAGE WAS WRITTEN WHILE THE IDENTITY WAS SAFE: %v", err)
	}
	// And the evidence is still readable, which is the point of keeping it.
	if _, err := a.store.OutboxOutcomesSince(0); err != nil {
		t.Errorf("SAFE made the projection unreadable: %v", err)
	}
}

func TestEnteringSafeDoesNotWaitBehindAParkedHousekeep(t *testing.T) {
	dir := t.TempDir()
	st := newAdmissionStore(t, dir)
	a := &App{store: st}
	defer a.resetModeForTest()

	// Another connection takes the database's write lock and keeps it.
	path := filepath.Join(dir, "identity.db")
	dsn := "file:" + strings.ReplaceAll(url.PathEscape(filepath.ToSlash(path)), "%2F", "/") +
		"?vfs=" + compressvfs.Name + "&_pragma=busy_timeout(5000)"
	holder, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	ctx := context.Background()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
			_ = conn.Close()
		}
	}
	defer release()

	housekept := make(chan error, 1)
	go func() {
		_, err := st.Housekeep(ctx)
		housekept <- err
	}()
	// Parked: a read waits only while a writer holds the store's lock,
	// and the pass is the only writer here.
	for deadline := time.Now().Add(5 * time.Second); ; {
		probe := make(chan struct{})
		go func() { _, _ = st.PendingWorkCount(); close(probe) }()
		parked := false
		select {
		case <-probe:
		case <-time.After(300 * time.Millisecond):
			parked = true
		}
		if parked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inconclusive: Housekeep never held the store's writer lock")
		}
	}

	entered := make(chan struct{})
	go func() {
		a.enterSafe("an integrity failure")
		close(entered)
	}()
	select {
	case <-entered:
		if reason, frozen := st.Frozen(); !frozen || reason != "an integrity failure" {
			t.Errorf("SAFE entry returned without freezing the projection: %v %q", frozen, reason)
		}
	case <-time.After(2 * time.Second):
		t.Error("SAFE entry waited behind a Housekeep parked on the database; it waits for nothing")
	}
	select {
	case err := <-housekept:
		t.Fatalf("inconclusive: Housekeep was not parked while SAFE entry ran: %v", err)
	default:
	}

	release()
	select {
	case <-housekept:
	case <-time.After(30 * time.Second):
		t.Fatal("the parked Housekeep never returned")
	}
	<-entered
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: "wake.timer", Payload: "{}", Source: "time"}); !errors.As(err, new(*store.FrozenError)) {
		t.Errorf("a queue write after SAFE entry was not refused by the store's handle: %v", err)
	}
}
