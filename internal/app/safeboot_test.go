package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault/tlsfaulttest"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// birthFixture births a valid identity in dir and returns its paths.
func birthFixture(t *testing.T, dir, name string) (keyPath, ledgerPath, dbPath string) {
	t.Helper()
	keyPath = filepath.Join(dir, "identity.sec")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	dbPath = filepath.Join(dir, "aii.db")
	// The live constitution: TestBootSafeIsMinimalPosture asserts the
	// platform-owned SAFE Ring 0 is NOT the birthed one, so it reads the
	// same live text this births under.
	root := genesistest.NewRoot(t)
	result := root.Birth(t, genesis.BirthConfig{
		Name:        name,
		Ring0Bundle: root.Ring0Bundle(t),
		Root:        root.Env,
		KeyPath:     keyPath, LedgerPath: ledgerPath, DBPath: dbPath,
	})
	if _, err := result.Ledger.Append(ledger.EventRelationshipUpsert, 1,
		map[string]any{
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
	st, err := store.NewForTest(dbPath)
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
	// The log beside the rig's data, as a start in its own home has it:
	// the default is relative to the working directory, which for a test
	// is the package's source directory.
	cfg.Logs.Dir = filepath.Join(dir, "log")
	// A boot with no Ring 5 in hand fetches it from firewall_url. The
	// default names the platform, so every boot of every test asked it:
	// 35 requests in a run, and a run that failed when the gate host could
	// not resolve the name. The test seam serves the bytes of its one live
	// fetch; the boot verifies them as it verifies the platform's
	// (genesistest.Ring5Server). A test that wants no Ring 5 server, or a
	// refusing one, sets its own after this.
	cfg.Genesis.FirewallURL = genesistest.Ring5Server(t)
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
	defer stopApp(t, app)

	if reason, ok := app.SafeMode(); !ok || !strings.Contains(reason, "chain verification") {
		t.Fatalf("must be SAFE with the chain reason, got %q %v", reason, ok)
	}

	// THE SAFE CONSTITUTION IS PLATFORM-OWNED: Ring 0 must NOT be the
	// (rejected) ledger's constitution; it is the substrate's compiled-in
	// safe-mode posture.
	ring0 := app.rings.GetContent(ring.Ring0)
	born := genesistest.NewRoot(t).Constitution(t)
	if heading, _, _ := strings.Cut(born, "\n"); heading != "" && strings.Contains(ring0, heading) {
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
	stopApp(t, app)
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
	if err := startLiveUnseededForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up: %v", err)
	}
	defer stopApp(t, app)

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
	defer stopApp(t, app)
	reason, ok := app.SafeMode()
	if !ok || !strings.Contains(reason, "could not be read") || !strings.Contains(reason, "malformed line") {
		t.Fatalf("an unreadable record must enter SAFE naming the line, got %q %v", reason, ok)
	}
	if app.ledger != nil {
		t.Fatal("a record that refused to open was handed to the runtime anyway")
	}
	// The operator surface reads without a ledger handle, and the one
	// door that could write refuses instead of dereferencing nothing.
	if c, err := app.continuityState(); err != nil || c.LedgerSeq != 0 {
		t.Fatalf("continuity without a ledger: %+v %v", c, err)
	}
	door := &ledgerAdapter{Ledger: app.ledger, kp: app.keyPair, st: app.store}
	if _, err := door.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "x", "content": "no"}, ""); err == nil || !strings.Contains(err.Error(), "no ledger is open") {
		t.Fatalf("the door did not refuse without a ledger: %v", err)
	}
}

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
	if _, err := lg.Append(ledger.EventIntentionStateChange, 3,
		map[string]any{"id": "ghost", "state": "completed"}, kp); err != nil {
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
	defer stopApp(t, app)

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
	stopApp(t, app)
	hashAfter, _ := fileDigest(t, dbPath)
	if hashAfter != hashBefore {
		t.Fatal("a failed rebuild changed the projection database — partial state was published")
	}
}

func TestASchemaThisBinaryDoesNotDeclareBootsSafe(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seed  func(t *testing.T, db *sql.DB)
		wants []string // in the SAFE reason beside safeSchema's line; nil means a live boot
	}{
		{name: "healthy"},
		{name: "column", seed: execAll(`ALTER TABLE turn_metrics ADD COLUMN future_tokens INTEGER`, `UPDATE turn_metrics SET future_tokens = 7`),
			wants: []string{"future_tokens", "hold data and are not declared"}},
		{name: "table", seed: execAll(`CREATE TABLE future_notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL) STRICT`, `INSERT INTO future_notes (body) VALUES ('kept by a newer version')`),
			wants: []string{"table:future_notes", "never declared"}},
		// A newer declaration that let calls be NULL, and a row that uses it:
		// this binary's NOT NULL refuses the row when it rebuilds the table.
		{name: "rows", seed: nullableCallsWithANullRow,
			wants: []string{"rebuild turn_metrics", "NOT NULL", "the rows stay as they are"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Schema")
			buildPriorProjection(t, ledgerPath, dbPath)
			st, err := store.NewForTest(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 3, Rounds: 1, Source: "operator"}); err != nil {
				st.Close()
				t.Fatal(err)
			}
			if tc.seed != nil {
				tc.seed(t, st.DB())
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			before, _ := fileDigest(t, dbPath)

			a := New(safebootConfig(t, dir, "Schema", keyPath, ledgerPath, dbPath))
			if err := startLiveForTest(a); err != nil {
				t.Fatalf("startup must not fail on a schema it does not declare: %v", err)
			}
			defer stopApp(t, a)
			reason, safe := a.SafeMode()
			if tc.wants == nil {
				if safe {
					t.Fatalf("a healthy database booted SAFE: %s", reason)
				}
			} else {
				if !safe {
					t.Fatalf("a database holding what this binary does not declare (%s) booted live", tc.name)
				}
				for _, want := range append(tc.wants, safePostures[safeSchema].what, safePostures[safeSchema].route) {
					if !strings.Contains(reason, want) {
						t.Errorf("the SAFE reason does not say %q: %s", want, reason)
					}
				}
				if strings.Contains(reason, "restarting") {
					t.Errorf("with no update in flight the reason promises a rollback: %s", reason)
				}
			}

			if _, err := os.Stat(filepath.Join(dir, ".boot_completed")); (err == nil) != (tc.wants == nil) {
				t.Fatalf("boot-health marker present=%v after a boot that was SAFE=%v", err == nil, safe)
			}

			// The identity's data, read through the store: the memory view
			// holds no relationship and no turn.
			var rels int
			if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&rels); err != nil || rels != 1 {
				t.Fatalf("the store does not hold the identity's relationship (%d, %v): the empty memory view was mounted", rels, err)
			}
			if turns, _, _, _, _, _, err := a.store.RhythmStats(time.Hour); err != nil || turns != 1 {
				t.Fatalf("the store does not read the recorded turn (%d, %v)", turns, err)
			}
			if tc.wants == nil {
				return
			}
			if err := a.store.AddConversationTurn("operator", "probe"); err == nil {
				t.Fatal("the SAFE store accepted a write: it must be the read-only mount")
			}
			stopApp(t, a)
			if after, _ := fileDigest(t, dbPath); after != before {
				t.Fatal("the refused startup or SAFE changed the database")
			}
		})
	}
}

func TestASchemaSafeBootLeavesAnUpdateRollbackArmed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, keyPath, ledgerPath string, db *sql.DB)
		armed  bool
	}{
		{name: "schema", armed: true, damage: func(t *testing.T, _, _ string, db *sql.DB) {
			execAll(`ALTER TABLE turn_metrics ADD COLUMN future_tokens INTEGER`, `UPDATE turn_metrics SET future_tokens = 7`)(t, db)
		}},
		{name: "chain", damage: func(t *testing.T, keyPath, ledgerPath string, _ *sql.DB) { tamperChain(t, keyPath, ledgerPath) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Update")
			buildPriorProjection(t, ledgerPath, dbPath)
			st, err := store.NewForTest(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 3, Rounds: 1}); err != nil {
				st.Close()
				t.Fatal(err)
			}
			tc.damage(t, keyPath, ledgerPath, st.DB())
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			// What a swap leaves (internal/updates swapBinary): the backup,
			// the record with no boot counted yet, the marker retired. The
			// record's new_sha256 names no real binary, so a start that did
			// roll back would find the test binary "replaced by hand" and
			// retire the state rather than write over it.
			backup := []byte("the previous binary")
			sum := sha256.Sum256(backup)
			if err := os.WriteFile(filepath.Join(dir, "aii.previous"), backup, 0o755); err != nil {
				t.Fatal(err)
			}
			record := `{"attempts":0,"backup_sha256":"` + hex.EncodeToString(sum[:]) + `","new_sha256":"` + strings.Repeat("0", 64) + `"}`
			if err := os.WriteFile(filepath.Join(dir, ".update_pending"), []byte(record), 0o600); err != nil {
				t.Fatal(err)
			}

			a := New(safebootConfig(t, dir, "Update", keyPath, ledgerPath, dbPath))
			if err := startLiveForTest(a); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, a)
			reason, safe := a.SafeMode()
			if !safe {
				t.Fatal("the fixture did not boot SAFE")
			}
			if got := updates.RollbackArmed(dir); got != tc.armed {
				t.Fatalf("rollback armed after the SAFE boot = %v, want %v (reason: %s)", got, tc.armed, reason)
			}
			if says := strings.Contains(reason, "restarting rolls back to the previous version"); says != tc.armed {
				t.Fatalf("the reason says restarting rolls back = %v, want %v: %s", says, tc.armed, reason)
			}
		})
	}
}

// execAll seeds a database with statements, in order.
func execAll(statements ...string) func(*testing.T, *sql.DB) {
	return func(t *testing.T, db *sql.DB) {
		t.Helper()
		for _, q := range statements {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// nullableCallsWithANullRow rewrites turn_metrics as a newer declaration
// might — calls without NOT NULL — and stores a row outside the rhythm
// window that uses it.
func nullableCallsWithANullRow(t *testing.T, db *sql.DB) {
	t.Helper()
	var declared string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'turn_metrics'`).Scan(&declared); err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(strings.Replace(declared, "turn_metrics", "turn_metrics_newer", 1), "calls    INTEGER NOT NULL", "calls    INTEGER", 1)
	if newer == strings.Replace(declared, "turn_metrics", "turn_metrics_newer", 1) {
		t.Fatal("rig: turn_metrics no longer declares calls as it did")
	}
	execAll(newer, `INSERT INTO turn_metrics_newer SELECT * FROM turn_metrics`, `DROP TABLE turn_metrics`,
		`ALTER TABLE turn_metrics_newer RENAME TO turn_metrics`,
		`INSERT INTO turn_metrics (ts_ms, calls, read_only, spawned, harvested) VALUES (1, NULL, 0, 0, 0)`)(t, db)
}

func TestEachSafeTriggerSaysItsOwnTruth(t *testing.T) {
	const detail = "the runtime's own words"
	bootHolds, runHolds := safeHolds(true), safeHolds(false)
	for tr := safeRecord; tr <= lastSafeTrigger; tr++ {
		p, ok := safePostures[tr]
		if !ok || p.what == "" || p.route == "" || p.why == "" {
			t.Fatalf("trigger %d has no words (what, route, why)", tr)
		}
		if reason := safeReason(tr, detail); !strings.HasPrefix(reason, p.what+": "+detail+". ") || !strings.Contains(reason, p.route) {
			t.Errorf("trigger %d: the operator's line is not what, the runtime's words, route: %q", tr, reason)
		}
		// Where the resident reads it: the boot posture for a boot
		// trigger, and the turn's SAFE section wherever Ring 0 holds the
		// constitution (every runtime trigger; a boot stopped after the
		// constitution was sealed). Each carries its own paragraph, the
		// runtime's words and the list true for its posture, and no other
		// trigger's paragraph.
		said := map[string]string{"turn": safeTurnNotice(tr, detail)}
		holds, wrong := runHolds, bootHolds
		if tr.atBoot() {
			said["posture"] = safeBootPosture(tr, detail)
			holds, wrong = bootHolds, runHolds
		}
		for where, text := range said {
			if !strings.Contains(text, p.why) || !strings.Contains(text, detail) || !strings.Contains(text, holds) || strings.Contains(text, wrong) {
				t.Errorf("trigger %d (%s): its paragraph, the runtime's words or its own posture's list is missing:\n%s", tr, where, text)
			}
			// The two Ring 5 triggers share their paragraph: what the
			// resident is told of a Ring 5 SAFE does not change with the
			// operator's route.
			for other := safeRecord; other <= lastSafeTrigger; other++ {
				if other != tr && safePostures[other].why != p.why && strings.Contains(text, safePostures[other].why) {
					t.Errorf("trigger %d (%s): carries trigger %d's paragraph", tr, where, other)
				}
			}
			if strings.Contains(text, "failed integrity verification") {
				t.Errorf("trigger %d (%s): still says the record failed integrity verification", tr, where)
			}
		}
	}
	// Only a damaged record is restored from a snapshot: a schema this
	// version does not declare, and a missing Ring 5, are not that.
	for _, tr := range []safeTrigger{safeSchema, safeRing5, safeRing5Refused} {
		for _, said := range []string{safeBootPosture(tr, detail), safeReason(tr, detail)} {
			for _, never := range []string{"integrity", "known-good", "backup", "snapshot"} {
				if strings.Contains(strings.ToLower(said), never) {
					t.Errorf("trigger %d says %q:\n%s", tr, never, said)
				}
			}
		}
	}
}

// THE REAL BOOT SAYS IT: four boot triggers driven through startLive. Each
// seals the posture with its own paragraph and the runtime's words, and
// the operator's notice on the dashboard (the continuity state) is that
// trigger's line — the same reason the log beacon and every refusal quote.
func TestASafeBootSaysWhatHappened(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger safeTrigger
		detail  string // a piece of the runtime's own words
		setup   func(t *testing.T, cfg *Config, dir, keyPath, ledgerPath, dbPath string)
		noRing5 bool
	}{
		{name: "record", trigger: safeRecord, detail: "chain verification failed at startup",
			setup: func(t *testing.T, _ *Config, _, keyPath, ledgerPath, _ string) { tamperChain(t, keyPath, ledgerPath) }},
		{name: "ring5", trigger: safeRing5, detail: "security_posture.absent", noRing5: true,
			setup: func(t *testing.T, cfg *Config, _, _, _, _ string) { cfg.Genesis.FirewallURL = "" }},
		{name: "ring5 refused", trigger: safeRing5Refused, detail: "security_posture.verify_fail: required Ring 5 unavailable or unverifiable", noRing5: true,
			setup: func(t *testing.T, cfg *Config, _, _, _, _ string) {
				now := time.Now()
				srv, _, _ := tlsfaulttest.Server(t, []string{"mail.example"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour))
				cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts = srv.URL, 1
			}},
		{name: "restore", trigger: safeRestore, detail: "a restore journal is here and does not read",
			setup: func(t *testing.T, _ *Config, dir, _, _, _ string) {
				if err := os.WriteFile(filepath.Join(dir, continuity.RestoreJournalName), []byte("not a journal"), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "schema", trigger: safeSchema, detail: "future_tokens hold data and are not declared",
			setup: func(t *testing.T, _ *Config, _, _, _, dbPath string) {
				st, err := store.NewForTest(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 1, Rounds: 1}); err != nil {
					t.Fatal(err)
				}
				execAll(`ALTER TABLE turn_metrics ADD COLUMN future_tokens INTEGER`, `UPDATE turn_metrics SET future_tokens = 7`)(t, st.DB())
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Says")
			buildPriorProjection(t, ledgerPath, dbPath)
			cfg := safebootConfig(t, dir, "Says", keyPath, ledgerPath, dbPath)
			tc.setup(t, cfg, dir, keyPath, ledgerPath, dbPath)
			a := New(cfg)
			start := startLiveForTest
			if tc.noRing5 {
				start = func(a *App) error { return a.startLive() }
			}
			if err := start(a); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, a)
			reason, safe := a.SafeMode()
			p := safePostures[tc.trigger]
			if p.what == "" || p.route == "" || p.why == "" {
				t.Fatalf("trigger %d has no words", tc.trigger)
			}
			if !safe || !strings.HasPrefix(reason, p.what+": ") || !strings.Contains(reason, tc.detail) || !strings.HasSuffix(reason, p.route+".") {
				t.Fatalf("the reason is not trigger %d's line with the runtime's words (safe=%v): %s", tc.trigger, safe, reason)
			}
			ring0 := a.rings.GetContent(ring.Ring0)
			if !strings.Contains(ring0, p.why) || !strings.Contains(ring0, tc.detail) {
				t.Fatalf("the posture lacks its paragraph or the runtime's words:\n%s", ring0)
			}
			for other := safeRecord; other <= safeDatabaseFormat; other++ {
				if why := safePostures[other].why; other != tc.trigger && why != "" && why != p.why && strings.Contains(ring0, why) {
					t.Fatalf("the posture carries trigger %d's paragraph:\n%s", other, ring0)
				}
			}
			if extra := a.safeTurnSection(); extra != "" {
				t.Fatalf("the posture is in Ring 0 and the turn repeats it:\n%s", extra)
			}
			c, err := a.continuityState()
			if err != nil || c.Mode != "safe" || c.SafeReason != reason {
				t.Fatalf("the operator's notice is not the reason: mode=%q notice=%q err=%v", c.Mode, c.SafeReason, err)
			}
		})
	}
}
