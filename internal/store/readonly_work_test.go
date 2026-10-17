package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoricalReadOnlyWorkUsesRecordedFieldsWithoutUpgrading(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			raw := openRawForTest(t, path)
			ddl, err := os.ReadFile("testdata/schema-pre-work-queue.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(string(ddl)); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(`INSERT INTO work_sessions(id,description,state) VALUES('main','historical task','recorded state')`); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			if compressed {
				target := filepath.Join(t.TempDir(), "compressed.db")
				if ok, err := ConvertDatabase(t.Context(), path, target, true); err != nil || !ok {
					t.Fatalf("compress fixture: %v %v", ok, err)
				}
				path = target
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ro, err := OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, read := range []func() (*WorkSession, error){ro.ActiveWorkSession, func() (*WorkSession, error) { return ro.WorkSessionByID("main") }} {
				ws, err := read()
				if err != nil || ws == nil || ws.ID != "main" || ws.Description != "historical task" || ws.State != "recorded state" {
					t.Errorf("historical work was not readable: %+v %v", ws, err)
					continue
				}
				if ws.Project != "" || ws.Plan != "" || ws.ExpectedEvidence != "" || ws.Evidence != "" || ws.CreatedSeq != 0 {
					t.Errorf("absent authored fields were invented: %+v", ws)
				}
			}
			if err := ro.UpdateWorkState(t.Context(), "main", "must not write"); err == nil {
				t.Fatal("historical read-only view admitted a write")
			}
			if err := ro.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatalf("diagnostic reads changed source bytes: %v", err)
			}
		})
	}
}

func TestReadOnlyWorkDoesNotInventRequiredFieldsOrRelaxNormalReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incomplete.db")
	raw := openRawForTest(t, path)
	if _, err := raw.Exec(`CREATE TABLE work_sessions(id TEXT PRIMARY KEY, description TEXT);
INSERT INTO work_sessions VALUES('kept','recorded task');`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	_, err = ro.ActiveWorkSession()
	var unavailable *SchemaError
	if !errors.As(err, &unavailable) || unavailable.Phase != "read-only work" {
		t.Fatalf("missing recorded status was not refused with a typed reason: %v", err)
	}
	// A writable caller that skipped normal opening must not silently gain
	// this diagnostic mapping. Normal opening's reconciler owns upgrades.
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	db := openRawForTest(t, legacy)
	defer db.Close()
	ddl, err := os.ReadFile("testdata/schema-pre-work-queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	if _, err := s.WorkSessionByID("anything"); err == nil {
		t.Fatal("normal reader accepted an unreconciled legacy table")
	}
}
