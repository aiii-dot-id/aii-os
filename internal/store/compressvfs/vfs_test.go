package compressvfs

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func openTestDB(t *testing.T, path string, compressed bool) *sql.DB {
	t.Helper()
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.IsAbs(path) && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"vfs": {Name}, "_pragma": {"busy_timeout(2000)", "foreign_keys(1)"}}
	if compressed {
		q.Set(CreateParameter, "1")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func execTest(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestVFSSQLRoundtrip(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "compressed.db")
			db := openTestDB(t, path, true)
			execTest(t, db, "PRAGMA journal_mode="+mode)
			execTest(t, db, "CREATE TABLE content(id INTEGER PRIMARY KEY, body TEXT NOT NULL, payload BLOB) STRICT")
			execTest(t, db, "CREATE VIRTUAL TABLE search USING fts5(body, tokenize='trigram')")
			body := strings.Repeat("unicode αβγ and searchable content ", 180)
			for i := 0; i < 50; i++ {
				execTest(t, db, "INSERT INTO content VALUES(?,?,?)", i, body, []byte{0, 1, 0, 255})
			}
			execTest(t, db, "INSERT INTO search(rowid,body) SELECT id,body FROM content")
			var found int
			if err := db.QueryRow("SELECT count(*) FROM search WHERE search MATCH 'searchable'").Scan(&found); err != nil || found != 50 {
				t.Fatalf("trigram: %d %v", found, err)
			}
			execTest(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			header, err := os.ReadFile(path)
			if err != nil || !strings.HasPrefix(string(header), fileMagic) {
				t.Fatalf("not compressed: %v", err)
			}
			db = openTestDB(t, path, false)
			var got string
			var payload []byte
			if err := db.QueryRow("SELECT body,payload FROM content WHERE id=29").Scan(&got, &payload); err != nil || got != body || fmt.Sprint(payload) != "[0 1 0 255]" {
				t.Fatalf("roundtrip: %v", err)
			}
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&got); err != nil || got != "ok" {
				t.Fatalf("integrity: %s %v", got, err)
			}
			if _, err := db.Exec("INSERT INTO content VALUES(80, 'bad', 'not a blob')"); err == nil {
				t.Fatal("STRICT constraint not enforced")
			}
		})
	}
}

func TestVFSNativeDatabaseUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")
	db := openTestDB(t, path, false)
	execTest(t, db, "CREATE TABLE ordinary(value TEXT)")
	execTest(t, db, "INSERT INTO ordinary VALUES('plain')")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(b), "SQLite format 3\x00") {
		t.Fatalf("plain file was converted: %v", err)
	}
}

func TestVFSPageSizesVacuumAndRollback(t *testing.T) {
	for _, pageSize := range []int{512, 1024, 8192, 65536} {
		t.Run(fmt.Sprint(pageSize), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pages.db")
			db := openTestDB(t, path, true)
			execTest(t, db, fmt.Sprintf("PRAGMA page_size=%d", pageSize))
			execTest(t, db, "CREATE TABLE content(id INTEGER PRIMARY KEY,body TEXT)")
			body := strings.Repeat("a multiblock UTF-8 value αβγ ", 3000)
			for i := 0; i < 8; i++ {
				execTest(t, db, "INSERT INTO content VALUES(?,?)", i, body)
			}
			execTest(t, db, "BEGIN")
			execTest(t, db, "UPDATE content SET body='must not survive'")
			execTest(t, db, "ROLLBACK")
			execTest(t, db, "DELETE FROM content WHERE id>2")
			execTest(t, db, "VACUUM")
			var got string
			if err := db.QueryRow("SELECT body FROM content WHERE id=2").Scan(&got); err != nil || got != body {
				t.Fatalf("vacuum/rollback changed content: %v", err)
			}
			var actual int
			if err := db.QueryRow("PRAGMA page_size").Scan(&actual); err != nil || actual != pageSize {
				t.Fatalf("page size: %d %v", actual, err)
			}
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&got); err != nil || got != "ok" {
				t.Fatalf("integrity: %s %v", got, err)
			}
		})
	}
}

func TestVFSOnlineBackupExportsOrdinarySQLite(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, filepath.Join(dir, "source.db"), true)
	execTest(t, db, "PRAGMA journal_mode=WAL")
	execTest(t, db, "CREATE TABLE history(value TEXT)")
	execTest(t, db, "INSERT INTO history VALUES('pinned conversation')")
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	destination := filepath.Join(dir, "export.db")
	err = conn.Raw(func(driver any) error {
		backup, err := driver.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(destination)
		if err != nil {
			return err
		}
		for {
			more, err := backup.Step(16)
			if err != nil {
				backup.Finish()
				return err
			}
			if !more {
				break
			}
		}
		return backup.Finish()
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(destination)
	if err != nil || !strings.HasPrefix(string(b), "SQLite format 3\x00") {
		t.Fatalf("export is not ordinary SQLite: %v", err)
	}
	native, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	var got string
	if err := native.QueryRow("SELECT value FROM history").Scan(&got); err != nil || got != "pinned conversation" {
		t.Fatalf("export data: %s %v", got, err)
	}
}

func TestVFSCompactionUsesNativeLockAndPreservesSQL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compact.db")
	db := openTestDB(t, path, true)
	execTest(t, db, "CREATE TABLE history(id INTEGER PRIMARY KEY,body TEXT)")
	for i := 0; i < 30; i++ {
		execTest(t, db, "INSERT OR REPLACE INTO history VALUES(1,?)", strings.Repeat(fmt.Sprint(i), 5000))
	}
	other := openTestDB(t, path, false)
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var want string
	if err := tx.QueryRow("SELECT body FROM history").Scan(&want); err != nil {
		t.Fatal(err)
	}
	var saved int64
	if err := db.QueryRow("PRAGMA " + CompactPragma).Scan(&saved); err == nil {
		t.Fatal("compaction bypassed a native reader lock")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA " + CompactPragma).Scan(&saved); err != nil || saved <= 0 {
		t.Fatalf("compact: %d %v", saved, err)
	}
	var got string
	if err := other.QueryRow("SELECT body FROM history").Scan(&got); err != nil || got != want {
		t.Fatalf("old connection after compaction: %v", err)
	}
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&got); err != nil || got != "ok" {
		t.Fatalf("compacted integrity: %s %v", got, err)
	}
}
