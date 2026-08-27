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

// birthFixture births a valid identity in dir and returns its paths.
func birthFixture(t *testing.T, dir, name string) (keyPath, ledgerPath, dbPath string) {
	t.Helper()
	keyPath = filepath.Join(dir, "identity.sec")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	dbPath = filepath.Join(dir, "aii.db")
	// Explicit constitution: TestBootSafeIsMinimalPosture asserts the
	// platform-owned SAFE Ring 0 does NOT contain this birthed text, so the
	// bundle must carry exactly it (not the genesistest default).
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

// buildPriorProjection materializes the ledger into the DB exactly the
// way a clean boot would, then closes — the durable footprint a prior
// healthy life leaves behind.
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

// tamperChain flips one content_hash nibble in ledger line 1 (the
// guarded-flip pattern from mode_test.go) and asserts the corruption
// landed.
func tamperChain(t *testing.T, keyPath, ledgerPath string) {
	t.Helper()
	raw, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	l0 := lines[0]
	i := strings.Index(l0, `"content_hash":"`)
	if i < 0 {
		t.Fatalf("no content_hash in ledger line 1")
	}
	j := i + len(`"content_hash":"`)
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
	if err := ledger.VerifyChain(ledgerPath, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err == nil {
		t.Fatal("corruption did not land — the flip was a no-op")
	}
}

func safebootConfig(t *testing.T, dir, name, keyPath, ledgerPath, dbPath string) *Config {
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		Name: name, KeyPath: keyPath, LedgerPath: ledgerPath, DBPath: dbPath,
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

// Cluster A probe (canon SAFE_MODE.md §4.1: on boot-time verification
// failure "Mutable boot work stops ... no database, ledger, checkpoint,
// cache, trace, audit, plugin, process, shell, or other filesystem
// mutation may begin"; SAFE_MODE_PLUGIN_LIFECYCLE.md §3.2: "do not
// launch or activate any plugin"; local R55: no database writes while
// integrity is unverified): a boot whose chain verification fails must
// come up in the MINIMAL SAFE posture — conversation + operator surface
// + read-only introspection + beacon — and must NOT replay the rejected
// ledger, NOT write the store, NOT load Ring 0 from the rejected chain,
// NOT start TIME/cognition.
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

	// THE SAFE CONSTITUTION IS PLATFORM-OWNED: Ring 0 must NOT be the
	// (rejected) ledger's constitution; it is the substrate's compiled-in
	// safe-mode posture.
	ring0 := app.rings.GetContent(ring.Ring0)
	if strings.Contains(ring0, "Honesty.") {
		t.Fatal("Ring 0 was loaded from the REJECTED ledger — the tampered constitution governs the SAFE conversation")
	}
	if !strings.Contains(ring0, "SAFE MODE") {
		t.Fatalf("Ring 0 must carry the platform safe-mode posture, got %.120q", ring0)
	}

	// NO COGNITION, NO TIME (canon §3.3.1: alarm firing/rearm BLOCKED;
	// mutating facilities blocked): the cognitive runtime is not built.
	if app.timeFac != nil {
		t.Fatal("TIME was started in boot-SAFE (its legacy-alarm cleanup mutates durable alarms)")
	}
	if app.executor != nil {
		t.Fatal("the work-queue executor was started in boot-SAFE")
	}

	// NO PLUGIN OR SECTION ACTIVATION (lifecycle canon §3.2).
	if len(app.plugins) != 0 || len(app.sectionActs) != 0 {
		t.Fatalf("plugins/sections activated in boot-SAFE: %d/%d", len(app.plugins), len(app.sectionActs))
	}

	// READ-ONLY INTROSPECTION: the prior admitted projection is readable…
	var rels int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil {
		t.Fatalf("prior projection must be readable in SAFE: %v", err)
	}
	if rels != 1 {
		t.Fatalf("prior projection content missing (relationships=%d, want the founding 1)", rels)
	}
	// …and UNWRITABLE — not merely unwritten.
	if err := app.store.AddConversationTurn("operator", "probe"); err == nil {
		t.Fatal("the store accepted a durable write in boot-SAFE — it must be mounted read-only")
	}

	// Conversation continues, transiently (R55).
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

	// The operator surface (repair path) is up.
	if app.dashboard == nil || app.conv == nil {
		t.Fatal("boot-SAFE must keep the operator surface and the conversation loop")
	}

	// NO REPLAY, NO DURABLE WRITES — asserted after Stop so WAL staging
	// cannot hide them: Close checkpoints any staged write into the main
	// file, so an unchanged digest here means nothing was EVER written.
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

// A tampered ledger with NO existing projection database must not
// CREATE one: SAFE boots against an empty in-memory view instead.
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

// Cluster B tie-in (canon PROJECTION.md §9 state D / re-bake: a rebuild
// that cannot complete admits nothing): a VERIFIED chain whose replay
// fails — a signed event the materializer refuses — must boot SAFE with
// the prior projection preserved, not WARN and continue on a partial
// database.
func TestBootSafeOnReplayFailure(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "ReplayFail")
	buildPriorProjection(t, ledgerPath, dbPath)

	// Poison the ledger with a VALIDLY SIGNED event whose materialization
	// refuses (unknown intention target) — the raw-append bypass shape.
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
	if err := ledger.VerifyChain(ledgerPath, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
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

	// The prior admitted projection survived the failed rebuild.
	var rels int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil {
		t.Fatal(err)
	}
	if rels != 1 {
		t.Fatalf("failed rebuild destroyed the prior projection (relationships=%d)", rels)
	}

	// Nothing of the failed rebuild PUBLISHED (post-Stop, checkpoint done):
	// the projection file is byte-identical to the prior admitted state.
	app.Stop()
	hashAfter, _ := fileDigest(t, dbPath)
	if hashAfter != hashBefore {
		t.Fatal("a failed rebuild changed the projection database — partial state was published")
	}
}
