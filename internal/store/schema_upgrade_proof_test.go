package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// Counts alone cannot validate a shadow copy: SQLite may coerce a value on
// INSERT. Ordinary copying preserves text spelling and exact numeric values;
// an intended semantic conversion needs its existing owner's explicit rule.
func TestSchemaUpgradeCopyPreservesTypedValues(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, literal, wantType, wantValue string
		refuse                                       bool
	}{
		{"text_additive_control", "TEXT", "TEXT", "'0012'", "text", "'0012'", false},
		{"text_is_not_a_number_mapping", "TEXT", "INTEGER", "'0012'", "text", "'0012'", true},
		{"exact_numeric_promotion", "INTEGER", "REAL", "42", "real", "42.0", false},
		{"numeric_precision_loss", "INTEGER", "REAL", "9007199254740993", "integer", "9007199254740993", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforeDDL := fmt.Sprintf(`CREATE TABLE facts (id TEXT PRIMARY KEY, value %s) STRICT;`, tc.from)
			afterDDL := fmt.Sprintf(`CREATE TABLE facts (id TEXT PRIMARY KEY, value %s, tag TEXT NOT NULL DEFAULT '') STRICT;`, tc.to)
			s, _ := driftDB(t, beforeDDL, "INSERT INTO facts VALUES ('kept',"+tc.literal+")")
			defer s.DB().Close()
			before, _, err := s.liveObjectSQL("table", "facts")
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.reconcileSchema(afterDDL)
			if tc.refuse && err == nil {
				t.Error("ordinary copy accepted a value-changing coercion without a declared mapping")
			} else if !tc.refuse && err != nil {
				t.Fatalf("value-preserving control refused: %v", err)
			}
			var kind, value string
			if err := s.DB().QueryRow(`SELECT typeof(value),quote(value) FROM facts WHERE id='kept'`).Scan(&kind, &value); err != nil {
				t.Fatal(err)
			}
			if kind != tc.wantType || value != tc.wantValue {
				t.Errorf("recorded value changed: (%s,%s), want (%s,%s)", kind, value, tc.wantType, tc.wantValue)
			}
			var rows int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("row count changed: count=%d err=%v", rows, err)
			}
			if tc.refuse {
				after, _, err := s.liveObjectSQL("table", "facts")
				if err != nil || before != after {
					t.Errorf("refused copy failed to preserve the original declaration: %v", err)
				}
			} else if err := s.DB().QueryRow(`SELECT COUNT(*) FROM facts WHERE tag=''`).Scan(&rows); err != nil || rows != 1 {
				t.Errorf("new column did not take only its declared default: count=%d err=%v", rows, err)
			}
		})
	}
}

// Compose the tool record's retirement with real CarryAcross. A database
// from before 0.1.10 holds a completed read and a started write in
// tool_events alone; the table is retired, the write is carried into the
// log as its call, and that is the one verified change to the runtime:
// the conversation rows grow by exactly it, and nothing else moves.
func TestSchemaUpgradeCarryValidatesTheToolRecordCarry(t *testing.T) {
	dbPath, ledgerPath, kp := carryFixture(t)
	raw := openRawForTest(t, dbPath)
	for _, statement := range []string{
		`CREATE TABLE tool_events (execution_id TEXT PRIMARY KEY, turn_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
		 actor TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', provider_call_id TEXT NOT NULL DEFAULT '', tool TEXT NOT NULL,
		 args_record TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('started','done','abandoned')),
		 failed INTEGER NOT NULL DEFAULT 0, truncated INTEGER NOT NULL DEFAULT 0, result_record TEXT NOT NULL DEFAULT '',
		 started_ms INTEGER NOT NULL, finished_ms INTEGER NOT NULL DEFAULT 0, UNIQUE (turn_id, ordinal)) STRICT`,
		`INSERT INTO tool_events (execution_id,turn_id,ordinal,actor,model,provider_call_id,tool,args_record,state,started_ms) VALUES
		 ('` + toolExecutionID("t1", 1) + `','t1',1,'main','m','c1','read','[2 chars, sha256=00]','done',1000),
		 ('` + toolExecutionID("t2", 1) + `','t2',1,'main','m','c2','write','` + metaRecord(`{"file_path":"/x"}`) + `','started',1002)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Rewrap(ledgerPath, kp, "", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	history, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := CarryAcross(dbPath, ledgerPath, carryHead(t, ledgerPath, kp))
	if err != nil {
		t.Fatalf("carry rejected the tool record's retirement instead of validating its result: %v", err)
	}
	if _, listed := report.Ephemeral["tool_events"]; listed {
		t.Error("the retired table is still reported as runtime state")
	}
	if c := report.Converted["conversations"]; c.Before != 2 || c.After != 3 || !strings.Contains(c.Reason, "tool_events") {
		t.Errorf("the carried call is not the verified conversion: %+v", report.Converted)
	}
	if report.Ephemeral["runtime_meta"] != 1 {
		t.Errorf("unconverted runtime facts changed: %+v", report.Ephemeral)
	}
	if after, err := os.ReadFile(ledgerPath); err != nil || sha256.Sum256(after) != sha256.Sum256(history) {
		t.Errorf("database carry changed its already rewrapped record: %v", err)
	}
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var role, content, project string
	var turn int64
	if err := s.DB().QueryRow(`SELECT role,content,turn_seq FROM conversations WHERE id='c1'`).Scan(&role, &content, &turn); err != nil || role != "operator" || content != "hello" || turn != 2 {
		t.Fatalf("carry changed the retained conversation: role=%q content=%q turn=%d err=%v", role, content, turn, err)
	}
	if err := s.DB().QueryRow(`SELECT value FROM runtime_meta WHERE key='active_project'`).Scan(&project); err != nil || project != "p" {
		t.Fatalf("carry changed the retained project focus: %q %v", project, err)
	}
	var tool, args string
	if err := s.DB().QueryRow(`SELECT json_extract(details,'$.tool'), json_extract(details,'$.args_record') FROM conversations WHERE kind='tool_call'`).Scan(&tool, &args); err != nil || tool != "write" {
		t.Fatalf("the carry did not write the actual unfinished execution: tool=%q err=%v", tool, err)
	}
	wantArgs := fmt.Sprintf("[18 chars, sha256=%x]", sha256.Sum256([]byte(`{"file_path":"/x"}`)))
	if args != wantArgs {
		t.Errorf("carried arguments record=%q, want %q", args, wantArgs)
	}
	names, err := s.AbandonUnfinishedToolCalls()
	if err != nil || len(names) != 1 || names[0] != "write" {
		t.Fatalf("the next boot lost its side-effect warning: %v %v", names, err)
	}
}

// SQLite already knows where a trigger ends. A CASE expression's END;
// is not the trigger body's END; the target catalog must retain both.
func TestSchemaUpgradeCatalogKeepsCompleteTrigger(t *testing.T) {
	const tables = `CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT);
CREATE TABLE audit (note_id TEXT, body TEXT);`
	const trigger = `CREATE TRIGGER note_audit AFTER INSERT ON notes BEGIN
SELECT CASE WHEN NEW.body IS NULL THEN RAISE(ABORT, 'body required') ELSE 1 END;
INSERT INTO audit(note_id,body) VALUES (NEW.id,NEW.body);
END;`
	target := tables + "\n" + trigger
	// The same target is valid as a whole. Do not mistake a parser failure
	// in reconciliation for a declaration SQLite itself rejects.
	reference, _ := driftDB(t, target)
	defer reference.DB().Close()
	if _, err := reference.DB().Exec(`INSERT INTO notes VALUES ('control','kept')`); err != nil {
		t.Fatal(err)
	}
	var bodies int
	if err := reference.DB().QueryRow(`SELECT COUNT(*) FROM audit WHERE body='kept'`).Scan(&bodies); err != nil || bodies != 1 {
		t.Fatalf("complete target trigger did not execute: count=%d err=%v", bodies, err)
	}
	s, _ := driftDB(t, tables)
	defer s.DB().Close()
	if _, err := s.reconcileSchema(target); err != nil {
		t.Fatalf("reconciliation lost part of a valid target trigger: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT INTO notes VALUES ('upgraded','kept')`); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM audit WHERE body='kept'`).Scan(&bodies); err != nil || bodies != 1 {
		t.Fatalf("reconciled trigger lost its second statement: count=%d err=%v", bodies, err)
	}
}

// Pin the shipped driver's commit-error contract before assigning cleanup
// ownership in the new schema transaction. A failed deferred FK check rolls
// back the candidate; database/sql's subsequent ErrTxDone is not a second
// storage failure. I/O errors and process crashes are separate proof cases.
func TestSchemaUpgradeCommitFailureDoesNotPublishCandidate(t *testing.T) {
	for _, format := range []string{compressvfs.FormatSQLite, compressvfs.FormatZstd} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "commit.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			activation, err := s.ActivateFormatAtStartup(t.Context(), path, format)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			s = activation.Opened
			defer s.Close()
			s.DB().SetMaxOpenConns(1)
			if got, err := s.DatabaseFormat(t.Context()); err != nil || got != format {
				t.Fatalf("fixture format=%q, want %q: %v", got, format, err)
			}
			tx, err := s.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO work_sessions(id,description,created_seq) VALUES ('candidate','Must roll back.',1)`); err != nil {
				t.Fatalf("failure must occur at commit, not insertion: %v", err)
			}
			if err := tx.Commit(); err == nil {
				t.Fatal("commit accepted a reference to a missing ledger record")
			}
			if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
				t.Fatalf("database/sql commit-error cleanup contract changed: %v", err)
			}
			var rows int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE id='candidate'`).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("failed commit exposed its candidate: count=%d err=%v", rows, err)
			}
			// A subsequent transaction must be independent, not a commit of
			// the supposedly rolled-back candidate on a dirty pooled handle.
			next, err := s.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Rollback()
			if _, err := next.Exec(`INSERT INTO work_sessions(id,description) VALUES ('next','Independent valid work.')`); err != nil {
				t.Fatal(err)
			}
			if err := next.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			ro, err := OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Close()
			var id string
			if err := ro.DB().QueryRow(`SELECT id FROM work_sessions`).Scan(&id); err != nil || id != "next" {
				t.Fatalf("independent transaction did not persist: id=%q err=%v", id, err)
			}
			if err := ro.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("failed commit leaked across reopen: count=%d err=%v", rows, err)
			}
		})
	}
}

// Schema failure itself must roll back earlier schema work, before replay
// is even reached. The populated extra column is deterministic fault
// injection, not a claimed historical schema or a proposed new refusal rule.
// The successful case also guards the runtime data no ledger can rebuild.
func TestSchemaUpgradeRuntimePreservation(t *testing.T) {
	for _, format := range []string{compressvfs.FormatSQLite, compressvfs.FormatZstd} {
		t.Run(format, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				upgrade bool
				refuse  bool
			}{
				{name: "refusal_without_prior_change", refuse: true},
				{name: "lossless_upgrade", upgrade: true},
				{name: "refusal_after_earlier_rebuild", upgrade: true, refuse: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "runtime.db")
					s, err := New(path)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					anchor := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{
						"id": "runtime_anchor", "statement": "Keep the recorded anchor.", "ring": 3, "confidence": 0.8,
					})
					if err := s.ReplayAll(EventSlice([]ledger.Event{anchor})); err != nil {
						t.Fatal(err)
					}
					for _, statement := range []string{
						`INSERT INTO conversations (rowid,id,session_id,role,content,turn_seq,created_at)
						 VALUES (41,'turn_a','session','operator','preserved alpha',0,'2026-08-16T00:00:00Z'),
						        (903,'turn_b','session','resident','preserved beta',1,'2026-08-16T00:00:01Z')`,
						`INSERT INTO work_sessions (rowid,id,description,status,state,created_seq,updated_seq,result,harvested_ms,delivered_at)
						 VALUES (17,'work_null','Keep unknown values.','active',NULL,1,NULL,NULL,NULL,NULL),
						        (209,'work_zero','Keep explicit empty and zero values.','delivered','',1,1,'',0,0)`,
						`INSERT INTO alarms (alarm_id,owner_name,clock,deadline,repeat_every,payload)
						 VALUES ('alarm_null','proof','wall',0,NULL,NULL), ('alarm_empty','proof','life',71,9,'')`,
						`INSERT INTO work_queue (id,kind,payload,dedup_key,state,priority,scheduled_ms,lease_ms,max_retries,error_msg)
						 VALUES ('queue_null','proof','{"kept":true}',NULL,'PENDING',0,0,0,0,NULL),
						        ('queue_empty','proof','{"kept":false}','','CLAIMED',5,71,300000,3,'')`,
					} {
						if _, err := s.DB().Exec(statement); err != nil {
							t.Fatal(err)
						}
					}
					if tc.upgrade {
						// Both fields were absent in the checked-in historical
						// fixture. Isolate those additive transitions here.
						for _, statement := range []string{
							`ALTER TABLE conversations DROP COLUMN project_id`,
							`ALTER TABLE work_sessions DROP COLUMN plan`,
						} {
							if _, err := s.DB().Exec(statement); err != nil {
								t.Fatal(err)
							}
						}
					}
					if tc.refuse {
						if _, err := s.DB().Exec(`ALTER TABLE work_sessions ADD COLUMN proof_unmapped TEXT`); err != nil {
							t.Fatal(err)
						}
						if _, err := s.DB().Exec(`UPDATE work_sessions SET proof_unmapped='Do not discard this runtime fact.'`); err != nil {
							t.Fatal(err)
						}
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					if format == compressvfs.FormatZstd {
						encoded := filepath.Join(filepath.Dir(path), "compressed.db")
						if published, err := ConvertDatabase(t.Context(), path, encoded, true); err != nil || !published {
							t.Fatalf("prepare compressed fixture: published=%t err=%v", published, err)
						}
						path = encoded
					}
					before := readUpgradeProofState(t, path)
					beforeRuntime := readRuntimeUpgradeProof(t, path, format)
					opened, err := New(path)
					if tc.refuse {
						if opened != nil {
							opened.Close()
							t.Fatal("schema refusal published a store")
						}
						if err == nil {
							t.Fatal("unmapped populated runtime column did not refuse the upgrade")
						}
						ro, err := OpenReadOnly(path)
						if err != nil {
							t.Fatal(err)
						}
						var kept int
						err = ro.DB().QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE proof_unmapped='Do not discard this runtime fact.'`).Scan(&kept)
						ro.Close()
						if err != nil || kept != 2 {
							t.Errorf("refusal lost the unmapped runtime values: count=%d err=%v", kept, err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						defer opened.Close()
						var defaults, foreignKeys int
						if err := opened.DB().QueryRow(`SELECT
							(SELECT COUNT(*) FROM conversations WHERE project_id='') +
							(SELECT COUNT(*) FROM work_sessions WHERE plan='')`).Scan(&defaults); err != nil || defaults != 4 {
							t.Errorf("absent fields did not acquire only their declared defaults: count=%d err=%v", defaults, err)
						}
						if err := opened.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
							t.Errorf("upgrade left foreign keys disabled: value=%d err=%v", foreignKeys, err)
						}
						if failed := checkSidecarsOn(opened.db); len(failed) != 0 {
							t.Errorf("upgrade broke search sidecars: %v", failed)
						}
						if err := opened.Close(); err != nil {
							t.Fatal(err)
						}
					}
					after := readUpgradeProofState(t, path)
					if after.rows != before.rows {
						t.Errorf("upgrade changed prior projection/runtime rows: before=%s after=%s", before.rows, after.rows)
					}
					if got := readRuntimeUpgradeProof(t, path, format); got != beforeRuntime {
						t.Errorf("upgrade changed runtime values or rowids: before=%s after=%s", beforeRuntime, got)
					}
					if tc.refuse && after.schema != before.schema {
						t.Errorf("schema refusal committed earlier schema changes: before=%x after=%x", before.schema, after.schema)
					}
				})
			}
		})
	}
}

// Exact values, not just counts: SQL NULL, empty strings, explicit zero,
// noncontiguous rowids, references, timer deadlines and queue state survive.
// The added fields are checked separately, since they do not exist before.
func readRuntimeUpgradeProof(t *testing.T, path, format string) string {
	t.Helper()
	s, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.DatabaseFormat(t.Context()); err != nil || got != format {
		t.Fatalf("fixture format=%q, want %q: %v", got, format, err)
	}
	var result string
	if err := s.DB().QueryRow(`SELECT json_array(
		(SELECT json_group_array(json_array(rowid,id,session_id,role,content,turn_seq,created_at) ORDER BY id) FROM conversations),
		(SELECT json_group_array(json_array(rowid,id,description,status,state,created_seq,updated_seq,result,harvested_ms,delivered_at) ORDER BY id) FROM work_sessions),
		(SELECT json_group_array(json_array(rowid,alarm_id,owner_name,clock,deadline,repeat_every,payload) ORDER BY alarm_id) FROM alarms),
		(SELECT json_group_array(json_array(rowid,id,kind,payload,dedup_key,state,priority,scheduled_ms,claimed_at,lease_ms,done_at,retry_count,max_retries,error_msg,created_ms) ORDER BY id) FROM work_queue)
	)`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

// A read-only snapshot inspection must not demand that an older snapshot
// already contains a runtime table introduced by a later binary. Absence is
// zero rows, not an unreadable table; inspecting it must not perform an upgrade.
func TestSchemaUpgradeInspectionAcceptsAbsentNewRuntimeTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "older-snapshot.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddConversationTurn("operator", "Keep the snapshot conversation."); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().Exec(`DROP TABLE curiosity_cue`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, facts, inspectErr := InspectCopy(t.Context(), path)
	if inspectErr != nil {
		t.Errorf("read-only inspection rejected an absent new runtime table: %v", inspectErr)
	} else if facts.Ephemeral["curiosity_cue"] != 0 || facts.Ephemeral["conversations"] != 1 || facts.LastTurnSeq != 1 {
		t.Errorf("inspection invented rows or lost retained runtime facts: %+v", facts)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(before) {
		t.Errorf("inspection upgraded or modified the source snapshot: %v", err)
	}
}

// New has no record source. It may open an unchanged database or make
// lossless schema changes, but cannot commit an empty mirror on the promise
// that some future caller will replay. The old acknowledged head must survive
// an owner ending after open and before replay, including a subsequent open.
// No process kill is needed to expose this: closing the first owner loses its
// process-local mirrorHead just as process exit would.
func TestSchemaUpgradeMirrorAcknowledgmentSurvivesUnfinishedOpen(t *testing.T) {
	for _, historical := range []bool{false, true} {
		name := "current_schema_control"
		if historical {
			name = "historical_mirror"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "projection.db")
			initial, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			events := []ledger.Event{
				probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{
					"id": "first", "statement": "Keep the first accepted record.", "ring": 3, "confidence": 0.8}),
				probeEvent(2, ledger.EventBeliefUpsert, 3, map[string]any{
					"id": "last", "statement": "Keep the acknowledged boundary.", "ring": 3, "confidence": 0.8}),
			}
			if err := initial.ReplayAll(EventSlice(events)); err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			if historical {
				installHistoricalMirror(t, path, events)
			}
			readHead := func() uint64 {
				t.Helper()
				ro, err := OpenReadOnly(path)
				if err != nil {
					t.Fatal(err)
				}
				defer ro.Close()
				var head uint64
				if err := ro.DB().QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM ledger`).Scan(&head); err != nil {
					t.Fatal(err)
				}
				return head
			}
			if got := readHead(); got != 2 {
				t.Fatalf("fixture acknowledged %d, want 2", got)
			}
			openWithoutReplay := func() {
				t.Helper()
				s, err := New(path)
				if err != nil {
					if !historical {
						t.Fatalf("unchanged current database must open: %v", err)
					}
					// A future implementation may require the record to do
					// this upgrade. Refusal is acceptable for THIS no-source
					// API only if the prior data remains. The normal-boot
					// reflection proof separately requires successful upgrade.
					t.Logf("no-source open refused: %v", err)
					return
				}
				if got, err := s.MaxLedgerSeq(); err != nil || got != 2 {
					t.Errorf("open forgot the acknowledged head: got %d, want 2; %v", got, err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			openWithoutReplay()
			if got := readHead(); got != 2 {
				t.Errorf("open without replay committed loss of acknowledgment: durable head=%d, want 2", got)
			}
			openWithoutReplay()
			if got := readHead(); got != 2 {
				t.Errorf("subsequent open cannot recover acknowledgment: durable head=%d, want 2", got)
			}
		})
	}
}

// Only the mirror uses its real historical declaration; all other tables
// stay current. This isolates the acknowledgment boundary from the known
// reflections-table failure. The synthetic rows are not signature proof.
func installHistoricalMirror(t *testing.T, path string, events []ledger.Event) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "schema-pre-work-queue.sql"))
	if err != nil {
		t.Fatal(err)
	}
	declaration := declaredTableSQL(string(raw))["ledger"]
	if declaration == "" {
		t.Fatal("historical schema fixture has no ledger declaration")
	}
	db, err := sql.Open("sqlite", databaseURI(path)+"?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DROP TABLE ledger`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(declaration); err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO ledger
			(seq,prev_hash,timestamp,type,author,ring,payload,content_hash,signature,sig_key_id)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, e.Seq, e.Prev, e.Timestamp, e.Type, "synthetic",
			e.Ring, string(e.Payload), e.Content, e.Sig, "synthetic"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// These acceptance probes deliberately fail on the pre-repair implementation.
// They exercise the database half of startLive (New, then ReplayAll), not a
// second reconciler implemented in a test. When boot adopts the combined
// operation, this call site must adopt that same production entry point.
// The source error is the existing EventSource seam; no sleeps or races.
func TestSchemaUpgradeReplayFailureRestoresSchemaAndData(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "current_schema_control"
		if upgrade {
			name = "schema_change_before_replay_failure"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "projection.db")
			initial, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			prior := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{
				"id": "prior", "statement": "Keep the prior recorded belief.", "ring": 3, "confidence": 0.8,
			})
			if err := initial.ReplayAll(EventSlice([]ledger.Event{prior})); err != nil {
				t.Fatal(err)
			}
			if err := initial.AddConversationTurn("operator", "Keep this synthetic conversation."); err != nil {
				t.Fatal(err)
			}
			if _, err := initial.DB().Exec(`INSERT INTO work_sessions (id, description, status)
				VALUES ('kept_work', 'Keep this synthetic task.', 'delivered')`); err != nil {
				t.Fatal(err)
			}
			if upgrade {

				if _, err := initial.DB().Exec(`ALTER TABLE work_sessions DROP COLUMN plan`); err != nil {
					t.Fatal(err)
				}
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			before := readUpgradeProofState(t, path)
			if before.hasPlan == upgrade {
				t.Fatal("fixture does not have the intended old/current schema")
			}

			interrupted := errors.New("synthetic source stopped after one materialized event")
			yielded := false
			opened, err := OpenAndReplay(t.Context(), path, MirrorHead{Seq: prior.Seq, Hash: prior.EntryHash()}, func(yield func(*ledger.Event) error) error {
				replacement := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]any{
					"id": "candidate", "statement": "This candidate must not survive.", "ring": 3, "confidence": 0.6,
				})
				if err := yield(&replacement); err != nil {
					return err
				}
				yielded = true
				return interrupted
			})
			if opened != nil {
				opened.Close()
				t.Fatal("failed upgrade published a store")
			}
			if !yielded || !errors.Is(err, interrupted) {
				t.Fatalf("failure did not occur after materialization: yielded=%t err=%v", yielded, err)
			}

			after := readUpgradeProofState(t, path)
			// Report both halves even when one fails: current ReplayAll
			// protects rows, but cannot undo New's earlier schema commit.
			if after.rows != before.rows {
				t.Errorf("failed upgrade changed recorded/runtime rows: before=%s after=%s", before.rows, after.rows)
			}
			if after.schema != before.schema {
				t.Errorf("failed upgrade committed schema changes: plan present before=%t after=%t; schema %x -> %x",
					before.hasPlan, after.hasPlan, before.schema, after.schema)
			}
		})
	}
}

type upgradeProofState struct {
	schema  [32]byte
	rows    string
	hasPlan bool
}

func readUpgradeProofState(t *testing.T, path string) upgradeProofState {
	t.Helper()
	s, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.DB().Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_schema
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	var objects [][3]string
	for rows.Next() {
		var object [3]string
		if err := rows.Scan(&object[0], &object[1], &object[2]); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		objects = append(objects, object)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read schema: %v; close: %v", err, closeErr)
	}
	raw, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}
	out := upgradeProofState{schema: sha256.Sum256(raw)}
	if err := s.DB().QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('work_sessions') WHERE name='plan')`).Scan(&out.hasPlan); err != nil {
		t.Fatal(err)
	}
	// Exact fixture values, not just counts. The mirror/identity rows
	// and the two runtime-only surfaces must all retain their contents.
	if err := s.DB().QueryRow(`SELECT json_array(
		(SELECT json_group_array(json_array(id, statement, ring, confidence, first_seq, last_seq)) FROM beliefs),
		(SELECT json_group_array(json_array(seq, payload, content)) FROM ledger),
		(SELECT json_group_array(json_array(role, content, turn_seq, created_at)) FROM conversations),
		(SELECT json_group_array(json_array(id, description, status)) FROM work_sessions))`).Scan(&out.rows); err != nil {
		t.Fatal(err)
	}
	return out
}
