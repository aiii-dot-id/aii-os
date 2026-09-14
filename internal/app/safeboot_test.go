package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
func birthFixture(t *testing.T, dir, name string) (keyPath, ledgerPath, dbPath string) {
	t.Helper()
	keyPath = filepath.Join(dir, "identity.sec")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	dbPath = filepath.Join(dir, "aii.db")
	// .
	// .
	// .
	root := genesistest.NewRoot(t)
	result := root.Birth(t, genesis.BirthConfig{
		Name:        name,
		Ring0Bundle: root.MintRing0Bundle(t, "# Constitution\nHonesty."),
		Root:        root.Env,
		KeyPath:     keyPath, LedgerPath: ledgerPath, DBPath: dbPath,
	})
	if _, err := result.Ledger.Append(ledger.EventRelationshipUpsert, result.KeyPair.Fingerprint(), 1,
		map[string]interface{}{
			"id": "rel_fixture", "counterpart_name": "Operator", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "Test relationship",
			"operator_approval_excerpt": "Yes — rel_fixture approved.",
			"operator_approval_turn":    1,
			"approval_basis":            "conversation_turn",
		}, result.KeyPair); err != nil {
		t.Fatal(err)
	}
	result.Ledger.Close()
	return keyPath, ledgerPath, dbPath
}

// .
// .
// .
func buildPriorProjection(t *testing.T, ledgerPath, dbPath string) {
	t.Helper()
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReplayFromFile(ledgerPath); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func tamperChain(t *testing.T, keyPath, ledgerPath string) {
	t.Helper()
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `{"content":"`)
	if i < 0 {
		t.Fatalf("no content hash in ledger line 1")
	}
	j := i + len(`{"content":"`)
	flip := byte('0')
	if l0[j] == '0' {
		flip = '1'
	}
	lines[0] = l0[:j] + string(flip) + l0[j+1:]
	if err := os.WriteFile(ledgerPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err == nil {
		t.Fatal("corruption did not land — the flip was a no-op")
	}
}

func safebootConfig(t *testing.T, dir, _ string, keyPath, ledgerPath, dbPath string) *Config {
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		KeyPath: keyPath, LedgerPath: ledgerPath, DBPath: dbPath,
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = filepath.Join(dir, "config.json")
	return cfg
}

func fileDigest(t *testing.T, path string) (string, time.Time) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), info.ModTime()
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
func TestBootSafeIsMinimalPosture(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeMin")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)

	hashBefore, mtimeBefore := fileDigest(t, dbPath)

	app := New(safebootConfig(t, dir, "SafeMin", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up (minimal posture), not die: %v", err)
	}
	defer app.Stop()

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("must be SAFE with the chain reason, got %q %v", reason, ok)
	}

	// .
	// .
	// .
	ring0 := app.rings.GetContent(ring.Ring0)
	if strings.Contains(ring0, "Honesty.") {
		t.Fatal("Ring 0 was loaded from the REJECTED ledger — the tampered constitution governs the SAFE conversation")
	}
	if !strings.Contains(ring0, "SAFE MODE") {
		t.Fatalf("Ring 0 must carry the platform safe-mode posture, got %.120q", ring0)
	}

	// .
	// .
	if app.timeFac != nil {
		t.Fatal("TIME was started in boot-SAFE (its legacy-alarm cleanup mutates durable alarms)")
	}
	if app.executor != nil {
		t.Fatal("the work-queue executor was started in boot-SAFE")
	}

	// .
	if len(app.plugins) != 0 || len(app.sectionActs) != 0 {
		t.Fatalf("plugins/sections activated in boot-SAFE: %d/%d", len(app.plugins), len(app.sectionActs))
	}

	// .
	var rels int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil {
		t.Fatalf("prior projection must be readable in SAFE: %v", err)
	}
	if rels != 1 {
		t.Fatalf("prior projection content missing (relationships=%d, want the founding 1)", rels)
	}
	// .
	if err := app.store.AddConversationTurn("operator", "probe"); err == nil {
		t.Fatal("the store accepted a durable write in boot-SAFE — it must be mounted read-only")
	}

	// .
	before, _ := app.store.ConversationTurnCount()
	if err := app.engine.RecordConversationTurn("operator", "are you there?"); err != nil {
		t.Fatalf("SAFE conversation must work: %v", err)
	}
	after, _ := app.store.ConversationTurnCount()
	if before != after {
		t.Fatal("SAFE conversation wrote the store")
	}
	if st := app.engine.SafeTranscript(); len(st) != 1 {
		t.Fatalf("SAFE turn must land in the transient transcript, got %d", len(st))
	}

	// .
	if app.dashboard == nil || app.conv == nil {
		t.Fatal("boot-SAFE must keep the operator surface and the conversation loop")
	}

	// .
	// .
	// .
	app.Stop()
	hashAfter, mtimeAfter := fileDigest(t, dbPath)
	if hashAfter != hashBefore {
		t.Fatal("SAFE boot rewrote the projection database — the rejected ledger was replayed")
	}
	if !mtimeAfter.Equal(mtimeBefore) {
		t.Fatal("SAFE boot modified the projection database file (mtime changed) — durable writes while integrity is unverified")
	}
	if info, err := os.Stat(dbPath + "-wal"); err == nil && info.Size() > 0 {
		t.Fatal("SAFE boot left staged writes in the WAL — durable writes while integrity is unverified")
	}
}

// .
// .
func TestBootSafeCreatesNoDatabase(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeNoDB")
	tamperChain(t, keyPath, ledgerPath)

	app := New(safebootConfig(t, dir, "SafeNoDB", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up: %v", err)
	}
	defer app.Stop()

	if _, ok := app.SafeMode(); !ok {
		t.Fatal("must be SAFE")
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("boot-SAFE CREATED the projection database — a durable write while integrity is unverified")
	}
	if app.store == nil {
		t.Fatal("SAFE still needs a (memory) store for the operator surface")
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
func TestBootSafeOnAnUnreadableLedger(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Unreadable")
	buildPriorProjection(t, ledgerPath, dbPath)
	f, err := os.OpenFile(ledgerPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"drill":"this line is not a ledger record"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ledger.New(ledgerPath); err == nil {
		t.Fatal("precondition: the damaged ledger opened")
	}

	app := New(safebootConfig(t, dir, "Unreadable", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("an unreadable record must come up SAFE, not die: %v", err)
	}
	defer app.Stop()
	reason, ok := app.SafeMode()
	if !ok || !strings.Contains(reason, "could not be read") || !strings.Contains(reason, "malformed ledger line") {
		t.Fatalf("an unreadable record must enter SAFE naming the line, got %q %v", reason, ok)
	}
	if app.ledger != nil {
		t.Fatal("a record that refused to open was handed to the runtime anyway")
	}
	// .
	// .
	if c, err := app.continuityState(); err != nil || c.LedgerSeq != 0 {
		t.Fatalf("continuity without a ledger: %+v %v", c, err)
	}
	door := &ledgerAdapter{Ledger: app.ledger, kp: app.keyPair, st: app.store}
	if _, err := door.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "x", "content": "no"}, ""); err == nil || !strings.Contains(err.Error(), "no ledger is open") {
		t.Fatalf("the door did not refuse without a ledger: %v", err)
	}
}

func TestBootSafeOnReplayFailure(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "ReplayFail")
	buildPriorProjection(t, ledgerPath, dbPath)

	// .
	// .
	kp, err := crypto.LoadKeyPair(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventIntentionStateChange, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "ghost", "state": "completed"}, kp); err != nil {
		lg.Close()
		t.Fatal(err)
	}
	lg.Close()
	if _, err := ledger.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("precondition: the poisoned chain still VERIFIES (it is validly signed): %v", err)
	}

	hashBefore, _ := fileDigest(t, dbPath)

	app := New(safebootConfig(t, dir, "ReplayFail", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("replay-failure boot must come up SAFE, not die: %v", err)
	}
	defer app.Stop()

	reason, ok := app.SafeMode()
	if !ok || !strings.Contains(reason, "replay") {
		t.Fatalf("a failed projection rebuild must enter SAFE with the replay reason, got %q %v", reason, ok)
	}

	// .
	var rels int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil {
		t.Fatal(err)
	}
	if rels != 1 {
		t.Fatalf("failed rebuild destroyed the prior projection (relationships=%d)", rels)
	}

	// .
	// .
	app.Stop()
	hashAfter, _ := fileDigest(t, dbPath)
	if hashAfter != hashBefore {
		t.Fatal("a failed rebuild changed the projection database — partial state was published")
	}
}
