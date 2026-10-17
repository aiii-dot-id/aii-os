package compressvfs

import (
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

const (
	readPathRows    = 30_000 // about 1 KiB each: the live store's 31 MB image
	readPathBodyLen = 1000
)

func readPathURI(path string, compressed bool, cacheKiB int) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{"vfs": {Name}, "_pragma": {"busy_timeout(5000)", "journal_mode(WAL)"}}
	if cacheKiB != 0 {
		q.Add("_pragma", fmt.Sprintf("cache_size(%d)", -cacheKiB))
	}
	if compressed {
		q.Set(CreateParameter, "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// readPathImage writes the image: rows of about 1 KiB of words and hex,
// five kinds, an index on (kind, ts), checkpointed into the file.
func readPathImage(b *testing.B, path string, compressed bool) {
	b.Helper()
	db, err := sql.Open("sqlite", readPathURI(path, compressed, 0))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		"CREATE TABLE events(id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL)",
		"CREATE INDEX events_kind_ts ON events(kind, ts)",
	} {
		if _, err := db.Exec(q); err != nil {
			b.Fatal(err)
		}
	}
	words := strings.Fields("the identity recalls a conversation about memory ledger voice plugin dream intake replay maintenance window turn operator settings speech model cache page")
	kinds := []string{"turn", "tool", "dream", "voice", "note"}
	r := rand.New(rand.NewSource(61))
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= readPathRows; i++ {
		var body strings.Builder
		for body.Len() < readPathBodyLen {
			body.WriteString(words[r.Intn(len(words))])
			body.WriteByte(' ')
			if r.Intn(7) == 0 {
				fmt.Fprintf(&body, "%x ", r.Uint64())
			}
		}
		if _, err := tx.Exec("INSERT INTO events VALUES(?,?,?,?)", i, 1_700_000_000+i, kinds[r.Intn(len(kinds))], body.String()[:readPathBodyLen]); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		b.Fatal(err)
	}
}

// readPathTurn is the read work of a turn's prompt: 200 point lookups
// spread over the image (recall), the newest 200 rows (the conversation
// window) and an index range of about 6,000 rows (an intake), each its
// own read transaction, as the store's reads are.
func readPathTurn(b *testing.B, db *sql.DB, r *rand.Rand) {
	var s string
	for i := 0; i < 200; i++ {
		if err := db.QueryRow("SELECT body FROM events WHERE id=?", 1+r.Intn(readPathRows)).Scan(&s); err != nil {
			b.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT body FROM events ORDER BY id DESC LIMIT 200")
	if err != nil {
		b.Fatal(err)
	}
	for rows.Next() {
		if err := rows.Scan(&s); err != nil {
			b.Fatal(err)
		}
	}
	rows.Close()
	var n, total int64
	if err := db.QueryRow("SELECT count(*), sum(length(body)) FROM events WHERE kind='dream'").Scan(&n, &total); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkTheReadPath(b *testing.B) {
	if err := Register(); err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	for _, compressed := range []bool{false, true} {
		readPathImage(b, filepath.Join(dir, fmt.Sprint(compressed)+".db"), compressed)
	}
	for _, format := range []struct {
		name       string
		compressed bool
	}{{"plain", false}, {"zstd", true}} {
		for _, cacheKiB := range []int{0, 16 << 10, 64 << 10} {
			for _, live := range []bool{false, true} {
				cache := "default"
				if cacheKiB != 0 {
					cache = fmt.Sprintf("%dMiB", cacheKiB>>10)
				}
				load := "quiet"
				if live {
					load = "live"
				}
				b.Run(format.name+"/"+cache+"/"+load, func(b *testing.B) {
					path := filepath.Join(dir, fmt.Sprint(format.compressed)+".db")
					reader, err := sql.Open("sqlite", readPathURI(path, format.compressed, cacheKiB))
					if err != nil {
						b.Fatal(err)
					}
					defer reader.Close()
					reader.SetMaxOpenConns(1)
					writer, err := sql.Open("sqlite", readPathURI(path, format.compressed, cacheKiB))
					if err != nil {
						b.Fatal(err)
					}
					defer writer.Close()
					writer.SetMaxOpenConns(1)
					r := rand.New(rand.NewSource(1))
					readPathTurn(b, reader, r) // the reader's cache warm, as a running identity's is
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if live {
							b.StopTimer()
							if _, err := writer.Exec("INSERT INTO events(ts, kind, body) VALUES(?, 'turn', 'a turn')", i); err != nil {
								b.Fatal(err)
							}
							b.StartTimer()
						}
						readPathTurn(b, reader, r)
					}
				})
			}
		}
	}
}
