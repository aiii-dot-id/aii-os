package store

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcknowledgedPrefixReadsHistoricalShapeWithoutUpgrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ledger(seq INTEGER PRIMARY KEY); INSERT INTO ledger VALUES(9)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ahead *MirrorAheadError
	if err := CheckAcknowledgedPrefix(path, 8); !errors.As(err, &ahead) || ahead.Mirror != 9 || ahead.Record != 8 {
		t.Fatalf("ahead: %v", err)
	}
	if err := CheckAcknowledgedPrefix(path, 9); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only preflight upgraded the old database")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := CheckAcknowledgedPrefix(missing, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight created a database: %v", err)
	}
}
