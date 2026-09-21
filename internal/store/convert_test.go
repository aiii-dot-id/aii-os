package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

func TestConvertDatabasePreservesRowsTypesRowidsAndSource(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "source #%.db")
	db, err := sql.Open("sqlite", databaseURI(plain))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE content(name TEXT PRIMARY KEY, body TEXT, payload BLOB, score REAL) STRICT;
CREATE VIRTUAL TABLE search USING fts5(body,content='content',content_rowid='rowid',tokenize='trigram');
INSERT INTO content(rowid,name,body,payload,score) VALUES(7,'one','searchable alpha',x'000100ff',1.25),(91,'two','searchable beta',x'',NULL);
INSERT INTO search(rowid,body) SELECT rowid,body FROM content;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	encoded := filepath.Join(dir, "compressed.db")
	if published, err := ConvertDatabase(context.Background(), plain, encoded, true); err != nil || !published {
		t.Fatalf("compress: %v %v", published, err)
	}
	after, err := os.ReadFile(plain)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("source changed: %v", err)
	}
	exported := filepath.Join(dir, "export.db")
	if published, err := ConvertDatabase(context.Background(), encoded, exported, false); err != nil || !published {
		t.Fatalf("export: %v %v", published, err)
	}
	for _, path := range []string{encoded, exported} {
		db, err := sql.Open("sqlite", databaseURI(path)+"?vfs="+compressvfs.Name+"&mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var rowid int64
		var payload []byte
		var score float64
		if err := db.QueryRow("SELECT rowid,payload,score FROM content WHERE name='one'").Scan(&rowid, &payload, &score); err != nil || rowid != 7 || !bytes.Equal(payload, []byte{0, 1, 0, 255}) || score != 1.25 {
			t.Fatalf("values: %d %x %f %v", rowid, payload, score, err)
		}
		if err := db.QueryRow("SELECT rowid FROM content WHERE name='two' AND score IS NULL").Scan(&rowid); err != nil || rowid != 91 {
			t.Fatalf("NULL/rowid: %d %v", rowid, err)
		}
		var found int
		if err := db.QueryRow("SELECT count(*) FROM search WHERE search MATCH 'searchable'").Scan(&found); err != nil || found != 2 {
			t.Fatalf("FTS: %d %v", found, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(exported)
	if err != nil || !bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		t.Fatalf("export not ordinary SQLite: %v", err)
	}
}

func TestConvertedStoreBootAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "source.db")
	s, err := New(plain)
	if err != nil {
		t.Fatal(err)
	}
	addPluginMemory(t, s, "saved", "lighthouse keeper")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(dir, "compressed.db")
	if _, err := ConvertDatabase(context.Background(), plain, compressed, true); err != nil {
		t.Fatal(err)
	}
	s, err = New(compressed)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := s.DB().Exec("UPDATE plugin_memories SET text=? WHERE id='saved'", strings.Repeat("lighthouse keeper ", 100)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			t.Fatal(err)
		}
	}
	// .
	busy, err := s.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pinned int
	if err := busy.QueryRowContext(context.Background(), "SELECT count(*) FROM plugin_memories").Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	var another int
	if err := s.DB().QueryRow("SELECT count(*) FROM plugin_memories").Scan(&another); err != nil {
		t.Fatal(err)
	}
	busy.Close()
	report, err := s.Housekeep()
	if err != nil || !strings.Contains(report, "compressed-container reclaimed") {
		t.Fatalf("compressed housekeeping: %q %v", report, err)
	}
	var found int
	if err := s.DB().QueryRow(pmTri, "light").Scan(&found); err != nil || found != 1 {
		t.Fatalf("converted store FTS: %d %v", found, err)
	}
	var similarity float64
	if err := s.DB().QueryRow("SELECT trigram_similarity('same','same')").Scan(&similarity); err != nil || similarity != 1 {
		t.Fatalf("SQL functions lost: %f %v", similarity, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(compressed)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := ro.Housekeep(); err != nil || !strings.Contains(report, "read-only") {
		t.Fatalf("read-only housekeeping: %s %v", report, err)
	}
	if _, err := ro.DB().Exec("PRAGMA " + compressvfs.CompactPragma); err == nil {
		t.Fatal("read-only file accepted physical compaction")
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(compressed)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only mount changed database: %v", err)
	}
}

func TestConversionRefusesOverwriteCancellationAndBadInput(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(source, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if published, err := ConvertDatabase(context.Background(), source, destination, true); err == nil || published {
		t.Fatalf("overwrite accepted: %v %v", published, err)
	}
	if b, _ := os.ReadFile(destination); string(b) != "keep" {
		t.Fatal("existing file changed")
	}
	destination = filepath.Join(dir, "new.db")
	if published, err := ConvertDatabase(context.Background(), source, destination, true); err == nil || published {
		t.Fatalf("bad input accepted: %v %v", published, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if published, err := ConvertDatabase(ctx, source, destination, true); !errors.Is(err, context.Canceled) || published {
		t.Fatalf("cancel: %v %v", published, err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed output survived: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".db-convert-") {
			t.Fatal("private conversion working set survived")
		}
	}
}
