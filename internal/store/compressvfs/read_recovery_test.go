package compressvfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	driver "modernc.org/sqlite"
	sqlite "modernc.org/sqlite/lib"
)

// The wrapper steps the existing physical-file seam, not the scheduler.
// The second handle retires an incomplete tail after the reader's size
// snapshot (or after its record header) but before its native read.
type steppedReadFile struct {
	physical
	afterSize  func()
	beforeRead func(int64, int)
}

func (f *steppedReadFile) size() (int64, error) {
	n, err := f.physical.size()
	if f.afterSize != nil {
		f.afterSize()
	}
	return n, err
}

func (f *steppedReadFile) read(p []byte, off int64) error {
	if f.beforeRead != nil {
		f.beforeRead(off, len(p))
	}
	return f.physical.read(p, off)
}

func TestReadSurvivesConcurrentTornTailRetirement(t *testing.T) {
	for _, at := range []string{"size", "record"} {
		t.Run(at, func(t *testing.T) {
			file := &memoryFile{}
			writer := testContainer(t, file)
			want := bytes.Repeat([]byte("acknowledged bytes"), 400)
			if err := writer.write(want, 0); err != nil {
				t.Fatal(err)
			}
			if err := writer.sync(0); err != nil {
				t.Fatal(err)
			}
			boundary := writer.end
			// A complete-sized but torn record: coherent header, bad checksum.
			torn := make([]byte, recordHeaderSize+128+recordChecksumSize)
			copy(torn, recordMagic)
			binary.LittleEndian.PutUint64(torn[8:], writer.epoch)
			binary.LittleEndian.PutUint64(torn[24:], uint64(len(want)))
			binary.LittleEndian.PutUint32(torn[32:], 128)
			binary.LittleEndian.PutUint32(torn[36:], codecZstd)
			if err := file.write(torn, boundary); err != nil {
				t.Fatal(err)
			}
			stepped := &steppedReadFile{physical: file}
			reader := testContainer(t, stepped)
			retired := false
			retire := func() {
				if retired {
					return
				}
				retired = true
				if err := writer.prepareWrite(); err != nil {
					t.Fatal(err)
				}
				if int64(len(file.data)) != boundary {
					t.Fatal("fixture failed to retire only the torn tail")
				}
			}
			if at == "size" {
				reads := 0
				stepped.afterSize = func() {
					reads++
					if reads == 2 {
						retire()
					}
				}
			} else {
				stepped.beforeRead = func(off int64, n int) {
					if off == boundary && n > recordHeaderSize {
						retire()
					}
				}
			}
			got := bytes.Repeat([]byte{0xcc}, len(want))
			err := reader.read(got, 0)
			if !retired {
				t.Fatal("retirement step was not reached")
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("acknowledged read after retirement: error=%v sqlite=%d bytes-preserved=%v", err, result(err, sqlite.SQLITE_IOERR_READ), bytes.Equal(got, want))
			}
			if err := writer.write([]byte("new"), 0); err != nil {
				t.Fatal(err)
			}
			if err := writer.sync(0); err != nil {
				t.Fatal(err)
			}
			copy(want, "new")
			if err := reader.read(got, 0); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("same reader did not resume normal use: %v", err)
			}
			reopened := testContainer(t, file)
			if err := reopened.read(got, 0); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("reopen lost the surviving data: %v", err)
			}
		})
	}
}

func TestPhysicalEOFIsNotLogicalSQLiteEOF(t *testing.T) {
	file := &memoryFile{}
	w := testContainer(t, file)
	if err := w.write([]byte("valid"), 0); err != nil {
		t.Fatal(err)
	}
	if err := w.sync(0); err != nil {
		t.Fatal(err)
	}
	broken := &shortPhysicalFile{physical: file}
	r := testContainer(t, broken)
	broken.fail = true
	err := r.read(make([]byte, 5), 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("physical failure lost its cause: %v", err)
	}
	if broken.reads != 2 {
		t.Fatalf("physical EOF must get exactly one resnapshot, got %d reads", broken.reads)
	}
	if rc := result(err, sqlite.SQLITE_IOERR_READ); rc != sqlite.SQLITE_IOERR_READ {
		t.Fatalf("physical read failure exposed as SQLite result %d, want IOERR_READ", rc)
	}
	broken.reads, broken.cause = 0, errInjected
	if err := r.read(make([]byte, 5), 0); !errors.Is(err, errInjected) || broken.reads != 1 {
		t.Fatalf("ordinary I/O errors must propagate without a retry: %v reads=%d", err, broken.reads)
	}
	broken.fail = false
	p := bytes.Repeat([]byte{0xcc}, 8)
	err = r.read(p, 0)
	if rc := result(err, sqlite.SQLITE_IOERR_READ); rc != sqlite.SQLITE_IOERR_SHORT_READ || !bytes.Equal(p, []byte{'v', 'a', 'l', 'i', 'd', 0, 0, 0}) {
		t.Fatalf("logical EOF must preserve bytes and zero-fill its suffix: %v rc=%d bytes=%x", err, rc, p)
	}
}

type shortPhysicalFile struct {
	physical
	fail  bool
	reads int
	cause error
}

func (f *shortPhysicalFile) read(p []byte, off int64) error {
	if f.fail {
		f.reads++
		if f.reads > 2 {
			return errInjected // a retry-loop mutation fails promptly, not by timeout
		}
		if f.cause != nil {
			return f.cause
		}
		return io.ErrUnexpectedEOF
	}
	return f.physical.read(p, off)
}

func TestResnapshotDoesNotForgiveMissingAcknowledgedBytes(t *testing.T) {
	f := &memoryFile{}
	w := testContainer(t, f)
	if err := w.write([]byte("acknowledged"), 0); err != nil {
		t.Fatal(err)
	}
	if err := w.sync(0); err != nil {
		t.Fatal(err)
	}
	f.data = append(f.data, bytes.Repeat([]byte{0xff}, 100)...)
	stepped := &steppedReadFile{physical: f}
	r := testContainer(t, stepped)
	reads := 0
	stepped.afterSize = func() {
		reads++
		if reads == 2 {
			if err := f.truncate(w.durableEnd - 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	err := r.read(make([]byte, 12), 0)
	if !errors.Is(err, errCorrupt) || result(err, sqlite.SQLITE_IOERR_READ) != sqlite.SQLITE_CORRUPT {
		t.Fatalf("missing acknowledged bytes must remain an integrity failure: %v", err)
	}
}

// Test-only wrapper for the one idle SQLite connection this test opens. It
// retains the real native file, its descriptor and its locks. No extra native
// descriptor is opened/closed while SQLite holds the database open.
func soleCompressedHandle(t *testing.T) *vfsFile {
	t.Helper()
	var found *vfsFile
	n := 0
	files.Range(func(_, value any) bool {
		f := value.(*vfsFile)
		if f.compressed != nil {
			n++
			found = f
		}
		return true
	})
	if n != 1 {
		t.Fatalf("test requires exactly one compressed handle, got %d", n)
	}
	return found
}

func TestSQLiteReadRecoversWhenTheNativeTailIsRetired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	db := openTestDB(t, path, true)
	db.SetMaxOpenConns(1)
	execTest(t, db, "PRAGMA journal_mode=WAL")
	execTest(t, db, "CREATE TABLE saved(id INTEGER PRIMARY KEY, body TEXT NOT NULL) STRICT")
	body := strings.Repeat("durable αβγ content ", 180)
	for i := 0; i < 30; i++ {
		execTest(t, db, "INSERT INTO saved VALUES(?,?)", i, fmt.Sprintf("%d:%s", i, body))
	}
	execTest(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a stopped writer's incomplete physical record. All SQLite
	// handles are CLOSED while this fixture is prepared.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.Write(bytes.Repeat([]byte{0xff}, 100))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("prepare torn tail: write=%v close=%v", writeErr, closeErr)
	}
	db = openTestDB(t, path, false)
	db.SetMaxOpenConns(1)
	execTest(t, db, "PRAGMA cache_size=1")
	handle := soleCompressedHandle(t)
	original := handle.compressed.file
	step := &steppedReadFile{physical: original}
	handle.compressed.file = step
	t.Cleanup(func() { handle.compressed.file = original })
	sizes := 0
	retired := false
	step.afterSize = func() {
		sizes++
		if sizes == 2 {
			retired = true
			// Native effect of the writer retirement proved above, injected
			// inside SQLite's callback with its valid TLS and native descriptor.
			if err := original.truncate(info.Size()); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 30; i++ {
		var got string
		if err := db.QueryRow("SELECT body FROM saved WHERE id=?", i).Scan(&got); err != nil || got != fmt.Sprintf("%d:%s", i, body) {
			t.Fatalf("SQL row %d after native retirement: err=%v bytes-preserved=%v", i, err, got == fmt.Sprintf("%d:%s", i, body))
		}
	}
	if !retired {
		t.Fatal("SQLite did not reach the retirement step")
	}
	// No restart or operator repair: the same SQL handle keeps working.
	execTest(t, db, "INSERT INTO saved VALUES(30,'after recovery')")
	execTest(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity after recovery: %q %v", integrity, err)
	}
	handle.compressed.file = original
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestDB(t, path, false)
	var count int
	if err := reopened.QueryRow("SELECT count(*) FROM saved").Scan(&count); err != nil || count != 31 {
		t.Fatalf("reopened count=%d err=%v", count, err)
	}
}

type shortPayloadFile struct{ physical }

func (f shortPayloadFile) read(p []byte, offset int64) error {
	if offset >= headerSize {
		return io.ErrUnexpectedEOF
	}
	return f.physical.read(p, offset)
}

func TestSQLiteNeverAcceptsAPhysicallyUnreadablePageAsZeros(t *testing.T) {
	db := openTestDB(t, filepath.Join(t.TempDir(), "unreadable.db"), true)
	db.SetMaxOpenConns(1)
	execTest(t, db, "CREATE TABLE saved(id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
	execTest(t, db, "INSERT INTO saved VALUES(1,?)", strings.Repeat("payload", 1000))
	execTest(t, db, "PRAGMA shrink_memory")
	handle := soleCompressedHandle(t)
	original := handle.compressed.file
	handle.compressed.file = shortPayloadFile{original}
	t.Cleanup(func() { handle.compressed.file = original })
	var body string
	err := db.QueryRow("SELECT body FROM saved WHERE id=1").Scan(&body)
	var sqlErr *driver.Error
	if !errors.As(err, &sqlErr) || sqlErr.Code() != int(sqlite.SQLITE_IOERR_READ) {
		t.Fatalf("physical page failure must be IOERR_READ, not empty data or malformed SQLite: %v", err)
	}
	handle.compressed.file = original
	if err := db.QueryRow("SELECT body FROM saved WHERE id=1").Scan(&body); err != nil || body != strings.Repeat("payload", 1000) {
		t.Fatalf("same connection after read availability recovers: %v", err)
	}
}
