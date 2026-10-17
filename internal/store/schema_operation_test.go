package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestSchemaCopyUsesSQLiteColumnAndRowIDMetadata(t *testing.T) {
	for _, tc := range []struct{ name, before, after, seed, query string }{
		{"without_rowid", `CREATE TABLE facts(id TEXT PRIMARY KEY,v BLOB) WITHOUT ROWID;`, `CREATE TABLE facts(id TEXT PRIMARY KEY,v BLOB,tag TEXT DEFAULT '') WITHOUT ROWID;`, `INSERT INTO facts VALUES ('kept',X'00FF');`, `SELECT id||':'||hex(v)||':'||tag FROM facts`},
		{"descending_integer_pk", `CREATE TABLE facts(id INTEGER PRIMARY KEY DESC,v TEXT);`, `CREATE TABLE facts(id INTEGER PRIMARY KEY DESC,v TEXT,tag TEXT DEFAULT '');`, `INSERT INTO facts(rowid,id,v) VALUES (71,9,'kept');`, `SELECT v||':'||rowid||':'||id FROM facts`},
		{"generated_to_stored", `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':derived') STORED);`, `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT,tag TEXT DEFAULT '');`, `INSERT INTO facts(id) VALUES ('kept');`, `SELECT v FROM facts`},
		{"stored_to_generated", `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT);`, `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':derived') STORED);`, `INSERT INTO facts VALUES ('kept','kept:derived');`, `SELECT v FROM facts`},
		{"generated_expression", `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':old') STORED);`, `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':new') STORED);`, `INSERT INTO facts(id) VALUES ('kept');`, `SELECT v FROM facts`},
		{"removed_generated", `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':old') STORED);`, `CREATE TABLE facts(id TEXT PRIMARY KEY);`, `INSERT INTO facts(id) VALUES ('kept');`, `SELECT id FROM facts`},
		{"quoted_name", `CREATE TABLE "facts with space"("key" TEXT PRIMARY KEY,"body" TEXT);`, `CREATE TABLE "facts with space"("key" TEXT PRIMARY KEY,"body" TEXT,tag TEXT DEFAULT '');`, `INSERT INTO "facts with space" VALUES ('kept','kept:quoted');`, `SELECT body FROM "facts with space"`},
		{"unicode_names", `CREATE TABLE facts(id TEXT PRIMARY KEY,"Α" TEXT,"α" TEXT);`, `CREATE TABLE facts(id TEXT PRIMARY KEY,"Α" TEXT,"α" TEXT,tag TEXT DEFAULT '');`, `INSERT INTO facts VALUES ('kept','upper','lower');`, `SELECT "Α"||':'||"α" FROM facts`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := driftDB(t, tc.before, tc.seed)
			defer s.db.Close()
			if _, err := s.reconcileSchema(tc.after); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := s.db.QueryRow(tc.query).Scan(&got); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"without_rowid": "kept:00FF:", "descending_integer_pk": "kept:71:9", "generated_to_stored": "kept:derived", "stored_to_generated": "kept:derived", "generated_expression": "kept:new", "removed_generated": "kept", "quoted_name": "kept:quoted", "unicode_names": "upper:lower"}[tc.name]
			if got != want {
				t.Fatalf("preserved values %q, want %q", got, want)
			}
			var before, after int
			if err := s.db.QueryRow("PRAGMA schema_version").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := s.reconcileSchema(tc.after); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("PRAGMA schema_version").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("unchanged schema generated DDL: %d -> %d", before, after)
			}
		})
	}
}

func TestSchemaComparisonPreservesQuotedValues(t *testing.T) {
	const tables = `CREATE TABLE facts(id TEXT PRIMARY KEY); CREATE TABLE audit(value TEXT);`
	const before = tables + `CREATE TRIGGER fact_added AFTER INSERT ON facts BEGIN INSERT INTO audit VALUES("VALUE--same"); END;`
	const after = tables + `CREATE TRIGGER fact_added AFTER INSERT ON facts BEGIN INSERT INTO audit VALUES("value--same"); END;`
	s, _ := driftDB(t, before)
	defer s.db.Close()
	if _, err := s.reconcileSchema(after); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO facts VALUES ('row')"); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := s.db.QueryRow("SELECT value FROM audit").Scan(&value); err != nil || value != "value--same" {
		t.Fatalf("quoted value did not change: %q %v", value, err)
	}
	mustConverge(t, s, after)
}

// Process exit at the EventSource seam skips every rollback defer. The next
// open must still see the old whole operation or the committed whole one.
func TestSchemaUpgradeCrashPublication(t *testing.T) {
	const checkpoint = 83
	if path := os.Getenv("AII_SCHEMA_CRASH_CHILD_PATH"); path != "" {
		stage := os.Getenv("AII_SCHEMA_CRASH_CHILD_STAGE")
		event := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{"id": "candidate", "statement": "complete candidate", "ring": 3, "confidence": 0.7})
		st, err := OpenAndReplay(t.Context(), path, MirrorHead{Seq: event.Seq, Hash: event.EntryHash()}, func(yield func(*ledger.Event) error) error {
			if stage == "schema" {
				os.Exit(checkpoint)
			}
			if err := yield(&event); err != nil {
				return err
			}
			if stage == "replay" {
				os.Exit(checkpoint)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if stage != "commit" {
			st.Close()
			t.Fatalf("unknown stage %q", stage)
		}
		os.Exit(checkpoint) // after commit, without Close or process cleanup
	}
	if !hostcap.Can(hostcap.Subprocess).Available {
		t.Skip("process-kill proof needs subprocesses")
	}
	for _, format := range []string{"sqlite", "zstd"} {
		for _, stage := range []string{"schema", "replay", "commit"} {
			t.Run(format+"/"+stage, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "identity.db")
				st, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				converted, err := st.ActivateFormatAtStartup(t.Context(), path, format)
				if err != nil {
					st.Close()
					t.Fatal(err)
				}
				st = converted.Opened
				if err := st.AddConversationTurn("operator", "retained runtime data"); err != nil {
					t.Fatal(err)
				}
				if _, err := st.db.Exec("ALTER TABLE work_sessions DROP COLUMN plan"); err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				before := readUpgradeProofState(t, path)
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSchemaUpgradeCrashPublication$")
				cmd.Env = append(os.Environ(), "AII_SCHEMA_CRASH_CHILD_PATH="+path, "AII_SCHEMA_CRASH_CHILD_STAGE="+stage)
				out, err := cmd.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != checkpoint {
					t.Fatalf("checkpoint not reached: %v\n%s", err, out)
				}
				after := readUpgradeProofState(t, path)
				if stage != "commit" {
					if before != after {
						t.Fatal("interruption published a partial upgrade")
					}
				} else {
					if !after.hasPlan {
						t.Fatal("committed target column missing")
					}
					ro, err := OpenReadOnly(path)
					if err != nil {
						t.Fatal(err)
					}
					defer ro.Close()
					var got string
					if err := ro.db.QueryRow("SELECT statement FROM beliefs WHERE id='candidate'").Scan(&got); err != nil || got != "complete candidate" {
						t.Fatalf("committed replay missing: %q %v", got, err)
					}
					if err := ro.db.QueryRow("SELECT content FROM conversations WHERE role='operator'").Scan(&got); err != nil || got != "retained runtime data" {
						t.Fatalf("runtime data lost: %q %v", got, err)
					}
				}
			})
		}
	}
}

func TestSchemaGeneratedColumnCannotDiscardAnExistingValue(t *testing.T) {
	const before = `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT);`
	const after = `CREATE TABLE facts(id TEXT PRIMARY KEY,v TEXT GENERATED ALWAYS AS (id||':derived') STORED);`
	s, _ := driftDB(t, before, `INSERT INTO facts VALUES ('kept','recorded');`)
	defer s.db.Close()
	_, err := s.reconcileSchema(after)
	var changed *CopyValueError
	if !errors.As(err, &changed) {
		t.Fatalf("changed generated value was not refused: %v", err)
	}
	var got string
	if err := s.db.QueryRow("SELECT v FROM facts").Scan(&got); err != nil || got != "recorded" {
		t.Fatalf("source lost: %q %v", got, err)
	}
}

func TestSchemaLiteralTextCannotDeclareAMapping(t *testing.T) {
	const text = `CREATE TABLE facts (
	 id TEXT PRIMARY KEY,
	 b TEXT DEFAULT '-- replaces-derived-table: user_data',
	 c TEXT DEFAULT '-- renamed-from: important',
	 d TEXT DEFAULT '-- retired-column: c'
	);`
	if got := declaredDerivedPredecessors(text); len(got) != 0 {
		t.Fatalf("literal became predecessor: %v", got)
	}
	if got := declaredRenames(text); len(got) != 0 {
		t.Fatalf("literal became rename: %v", got)
	}
	if got := declaredRetiredColumns(text); len(got) != 0 {
		t.Fatalf("literal became retirement: %v", got)
	}
}

func TestSchemaCatalogDoesNotHideUserNamesWithSQLitePrefix(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if _, err := s.db.Exec("CREATE TABLE sqlitex(payload TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := s.auditSchema(); err == nil {
		t.Fatal("a user table was mistaken for an internal SQLite table")
	}
}

func TestSchemaCatalogNamesRemainIdentifiers(t *testing.T) {
	const ddl = `CREATE TABLE facts(id TEXT PRIMARY KEY);`
	s, _ := driftDB(t, ddl, `INSERT INTO facts VALUES ('kept');`, `CREATE INDEX "IF EXISTS absent; DELETE FROM facts" ON facts(id);`)
	defer s.db.Close()
	if _, err := s.reconcileSchema(ddl); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow("SELECT id FROM facts").Scan(&id); err != nil || id != "kept" {
		t.Fatalf("catalog name executed as SQL: %q %v", id, err)
	}
}

func TestSchemaAmbiguousPredecessorsCannotEraseData(t *testing.T) {
	const target = `CREATE TABLE a (
	 -- provenance: derived, clear-order 1
	 -- replaces-derived-table: prior
	 id TEXT PRIMARY KEY
	);
	CREATE TABLE b (
	 -- provenance: derived, clear-order 2
	 -- replaces-derived-table: prior
	 id TEXT PRIMARY KEY
	);`
	s, _ := driftDB(t, `CREATE TABLE prior(id TEXT PRIMARY KEY);`, `INSERT INTO prior VALUES ('kept');`)
	defer s.db.Close()
	_, err := s.reconcileSchema(target)
	var refused *SchemaError
	if !errors.As(err, &refused) || refused.Phase != "target" {
		t.Fatalf("ambiguous lineage not refused before apply: %v", err)
	}
	var id string
	if err := s.db.QueryRow("SELECT id FROM prior").Scan(&id); err != nil || id != "kept" {
		t.Fatalf("source changed: %q %v", id, err)
	}
}

func TestSchemaInvalidTargetAndBrokenViewPreserveTheSource(t *testing.T) {
	for _, target := range []string{
		`CREATE TABLE facts(id TEXT PRIMARY KEY,tag TEXT); CREATE TRIGGER bad AFTER INSERT ON facts BEGIN SELECT CASE WHEN 1 THEN 1 END;`,
		`CREATE TABLE facts(id TEXT PRIMARY KEY,tag TEXT); CREATE VIEW current_facts AS SELECT missing FROM facts;`,
		`CREATE TABLE facts(id TEXT PRIMARY KEY,tag TEXT); CREATE TRIGGER bad AFTER INSERT ON facts BEGIN SELECT new.missing; END;`,
		`CREATE TABLE facts(id TEXT PRIMARY KEY,other TEXT REFERENCES missing_table(id));`,
	} {
		s, _ := driftDB(t, `CREATE TABLE facts(id TEXT PRIMARY KEY); INSERT INTO facts VALUES ('kept');`)
		before, _, err := s.liveObjectSQL("table", "facts")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.reconcileSchema(target); err == nil {
			s.db.Close()
			t.Fatal("invalid target accepted")
		}
		after, _, err := s.liveObjectSQL("table", "facts")
		if err != nil || after != before {
			t.Fatalf("failed operation changed source: %v", err)
		}
		s.db.Close()
	}
}

func TestSchemaCancellationRollsBackUpgradeAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE work_sessions DROP COLUMN plan"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := readUpgradeProofState(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	called := false
	opened, err := OpenAndReplay(ctx, path, MirrorHead{}, func(func(*ledger.Event) error) error { called = true; cancel(); return ctx.Err() })
	if opened != nil {
		opened.Close()
		t.Fatal("cancelled operation published handle")
	}
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation seam not reached: %t %v", called, err)
	}
	after := readUpgradeProofState(t, path)
	if before != after {
		t.Fatal("cancelled operation changed logical schema/data")
	}
	// A later attempt uses the ordinary owner; the failed handle cannot poison it.
	opened, err = OpenAndReplay(t.Context(), path, MirrorHead{}, EventSlice(nil))
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
}

func TestSchemaReplayChecksTheVerifiedBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	before := readUpgradeProofState(t, path)
	event := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{"id": "b", "statement": "recorded", "ring": 3, "confidence": 0.7})
	for _, head := range []MirrorHead{{Seq: 1, Hash: "wrong"}, {Seq: 2, Hash: event.EntryHash()}} {
		opened, err := OpenAndReplay(t.Context(), path, head, EventSlice([]ledger.Event{event}))
		if opened != nil {
			opened.Close()
			t.Fatal("wrong boundary published handle")
		}
		if err == nil {
			t.Fatal("wrong verified boundary accepted")
		}
		if after := readUpgradeProofState(t, path); before != after {
			t.Fatal("boundary refusal changed source")
		}
	}
	second := probeEvent(2, ledger.EventBeliefUpsert, 3, map[string]any{"id": "second", "statement": "second", "ring": 3, "confidence": 0.7})
	third := probeEvent(3, ledger.EventBeliefUpsert, 3, map[string]any{"id": "third", "statement": "third", "ring": 3, "confidence": 0.7})
	opened, err := OpenAndReplay(t.Context(), path, MirrorHead{Seq: third.Seq, Hash: third.EntryHash()}, EventSlice([]ledger.Event{second, event, third}))
	if opened != nil {
		opened.Close()
		t.Fatal("out-of-order source published a store despite a matching last record")
	}
	if err == nil {
		t.Fatal("out-of-order verified source accepted")
	}
	if after := readUpgradeProofState(t, path); before != after {
		t.Fatal("source-order refusal changed the database")
	}
}

func TestSchemaUpgradeHistoricalReflectionIDReplacement(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := probeEvent(1, ledger.EventSelfModelSynthesize, 3, map[string]any{"id": "prior", "content": "first recorded portrait", "source_entity_refs": []string{"old-reference"}})
	second := probeEvent(2, ledger.EventSelfModelSynthesize, 3, map[string]any{"id": "prior", "content": "replacement recorded portrait", "source_entity_refs": []string{"old-reference"}})
	if err := s.ReplayAll(EventSlice([]ledger.Event{first, second})); err != nil {
		t.Fatal(err)
	}
	current, err := s.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "prior" || current.CreatedSeq != 2 || current.SynthesisText != "replacement recorded portrait" {
		t.Fatalf("historical replacement changed meaning: %+v %v", current, err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ledger").Scan(&count); err != nil || count != 2 {
		t.Fatalf("recorded history lost: %d %v", count, err)
	}
}

// A restore proof's own conversion waives exactly the change it made. A
// database whose lived clock is absent (from before it was recorded) has
// it initialized from the recorded birth when the record is replayed over
// it: one row where there was none, verified by the replay that made it.
func TestSchemaUpgradeRestoreValidatesDeclaredConversion(t *testing.T) {
	path, record, kp := carryFixture(t)
	verified := carryHead(t, record, kp)
	s, err := OpenAndReplay(t.Context(), path, verified, func(yield func(*ledger.Event) error) error { return ledger.Stream(record, yield) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM identity_lifetime`); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, captured, err := InspectCopy(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, restored, err := ProveRestore(t.Context(), path, record, filepath.Join(t.TempDir(), "proof"), verified)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Ephemeral["identity_lifetime"] != 0 || restored.Ephemeral["identity_lifetime"] != 1 {
		t.Fatalf("fixture did not convert: %v -> %v", captured, restored)
	}
	if diff := captured.DiffersAfterRestore(restored); diff != "" {
		t.Fatal(diff)
	}
	if diff := captured.Differs(restored.RuntimeFacts); diff == "" {
		t.Fatal("ordinary capture comparison incorrectly waived changed rows")
	}
	encoded, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	var fromReceipt RestoredRuntimeFacts
	if err := json.Unmarshal(encoded, &fromReceipt); err != nil {
		t.Fatal(err)
	}
	if diff := captured.DiffersAfterRestore(fromReceipt); diff == "" {
		t.Fatal("a serialized receipt supplied conversion authority instead of a fresh proof")
	}
	// A conversion of one table cannot excuse a loss elsewhere.
	restored.Ephemeral["conversations"]--
	if diff := captured.DiffersAfterRestore(restored); diff == "" {
		t.Fatal("conversion excused unrelated transcript loss")
	}
}

func TestSchemaRestoreComparisonKeepsHistoricalRuntimeNames(t *testing.T) {
	want := RuntimeFacts{Ephemeral: map[string]int64{"formerly_recorded_table": 7}}
	got := RuntimeFacts{Ephemeral: map[string]int64{}}
	if diff := want.Differs(got); diff == "" {
		t.Fatal("a name absent from today's catalog hid recorded runtime data")
	}
}

func TestSchemaUpgradeCarryReportsBirthInitialization(t *testing.T) {
	path, record, kp := carryFixture(t)
	raw := openRawForTest(t, path)
	if _, err := raw.Exec("DELETE FROM identity_lifetime"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	if _, err := ledger.Rewrap(record, kp, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	report, err := CarryAcross(path, record, carryHead(t, record, kp))
	if err != nil {
		t.Fatal(err)
	}
	change, ok := report.Converted["identity_lifetime"]
	if !ok || change.Before != 0 || change.After != 1 || change.Reason == "" {
		t.Fatalf("birth initialization was not reported: %+v", report)
	}
	if _, unchanged := report.Ephemeral["identity_lifetime"]; unchanged {
		t.Fatal("initialized clock reported unchanged")
	}
	if report.Converted["conversations"].Before != 2 || report.Converted["conversations"].After != 2 {
		t.Fatal("birth initialization lost conversation rows")
	}
}

func TestSchemaUpgradePreservesLivedClockDuringReplay(t *testing.T) {
	path, record, kp := carryFixture(t) // includes a recorded genesis event
	raw := openRawForTest(t, path)
	if _, err := raw.Exec("UPDATE identity_lifetime SET lifetime_ticks=71 WHERE singleton_id='current'"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	st, err := OpenAndReplay(t.Context(), path, carryHead(t, record, kp), func(yield func(*ledger.Event) error) error { return ledger.Stream(record, yield) })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var ticks int64
	if err := st.db.QueryRow("SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id='current'").Scan(&ticks); err != nil || ticks != 71 {
		t.Fatalf("replayed birth reset lived time: %d %v", ticks, err)
	}
}

func TestSchemaAcknowledgmentUsesDeclaredAliasBeforeMutation(t *testing.T) {
	const target = `CREATE TABLE ledger (
	 seq INTEGER PRIMARY KEY, -- renamed-from: ordinal
	 payload TEXT
	);`
	s, _ := driftDB(t, `CREATE TABLE ledger(ordinal INTEGER PRIMARY KEY,payload TEXT);`, `INSERT INTO ledger VALUES (9,'recorded');`)
	defer s.db.Close()
	seq, err := s.acknowledgedSequence(target)
	if err != nil || seq != 9 {
		t.Fatalf("declared source acknowledgment unreadable: %d %v", seq, err)
	}
	var old int
	if err := s.db.QueryRow("SELECT ordinal FROM ledger").Scan(&old); err != nil || old != 9 {
		t.Fatalf("acknowledgment check mutated the source: %d %v", old, err)
	}
}

func TestSchemaAcknowledgmentCannotUseLexicalMaximum(t *testing.T) {
	s, _ := driftDB(t, `CREATE TABLE ledger(seq,payload);`, `INSERT INTO ledger VALUES (9,'recorded'),('1','ambiguous');`)
	defer s.db.Close()
	_, err := s.acknowledgedSequence(`CREATE TABLE ledger(seq INTEGER PRIMARY KEY,payload TEXT);`)
	var unreadable *MirrorReadError
	if !errors.As(err, &unreadable) {
		t.Fatalf("mixed storage classes hid the acknowledged ordinal: %v", err)
	}
}
