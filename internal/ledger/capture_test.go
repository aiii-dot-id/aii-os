package ledger

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
func assembleCapture(t *testing.T, c Capture, tail string) string {
	t.Helper()
	dir := filepath.Dir(tail)
	for _, seg := range c.Segments {
		in, err := os.Open(seg)
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.Create(filepath.Join(dir, filepath.Base(seg)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(out, in); err != nil {
			t.Fatal(err)
		}
		in.Close()
		out.Close()
	}
	return tail
}

// .
// .
// .
// .
// .
// .
func TestACaptureIsTheRecordThroughItsLastRecordWhateverHappensNext(t *testing.T) {
	path, kp := sealedFixture(t, 25, 10, 20)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	want := entryHashes(t, path)

	tail := filepath.Join(t.TempDir(), "ledger.jsonl")
	c, err := l.Capture(tail)
	if err != nil {
		t.Fatal(err)
	}
	if c.LastSeq != 25 || c.SealedSeq != 10 || len(c.Segments) != 1 || c.LastHash != l.LastHash() {
		t.Fatalf("captured %+v, want last 25, sealed 10, one segment, the ledger's own last hash", c)
	}

	// .
	// .
	if err := l.Seal(20); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "after", "content": "after the capture"}, kp); err != nil {
		t.Fatal(err)
	}

	got := entryHashes(t, assembleCapture(t, c, tail))
	if len(got) != 25 {
		t.Fatalf("THE ASSEMBLED CAPTURE HOLDS %d RECORDS, captured at 25 — a sealing or an append after the capture reached the copy", len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("record %d of the capture is not the record that was captured", i+1)
		}
	}
	if got[24] != c.LastHash {
		t.Fatal("the capture's last record is not the hash it reported")
	}
	if n, err := VerifyChain(tail, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 25 {
		t.Fatalf("the assembled capture does not verify on its own: n=%d err=%v", n, err)
	}
}

// .
// .
func TestACaptureWithAnEmptyTailIsTheSealedRecord(t *testing.T) {
	path, kp := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	tail := filepath.Join(t.TempDir(), "ledger.jsonl")
	c, err := l.Capture(tail)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(tail); err != nil || st.Size() != 0 {
		t.Fatalf("the tail copy should be empty after a sealing through the last record: %v", err)
	}
	if n, err := VerifyChain(assembleCapture(t, c, tail), kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// .
// .
// .
// .
func TestACaptureRefusesATailThatIsNotTheRecordItHolds(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, path string){
		"a torn line": func(t *testing.T, path string) {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(`{"entry":{"seq":999,"torn`); err != nil {
				t.Fatal(err)
			}
		},
		"an emptied file": func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"a shortened file": func(t *testing.T, path string) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cut := strings.LastIndex(strings.TrimRight(string(raw), "\n"), "\n") + 1
			if err := os.WriteFile(path, raw[:cut], 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path, _ := sealedFixture(t, 6)
			l, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			damage(t, path)
			tail := filepath.Join(t.TempDir(), "ledger.jsonl")
			_, err = l.Capture(tail)
			if !errors.Is(err, ErrCaptureRefused) {
				t.Fatalf("a damaged tail was captured: err=%v", err)
			}
			if _, serr := os.Stat(tail); !os.IsNotExist(serr) {
				t.Fatal("the refused capture left its copy behind")
			}
		})
	}
}

// .
// .
// .
func TestACaptureNeverOverwrites(t *testing.T) {
	path, _ := sealedFixture(t, 3)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tail := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(tail, []byte("someone else's bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Capture(tail); err == nil {
		t.Fatal("a capture overwrote an existing file")
	}
	if raw, _ := os.ReadFile(tail); string(raw) != "someone else's bytes\n" {
		t.Fatal("the existing file was changed")
	}
}

// .
// .
// .
func TestACaptureRefusesForeignBytesOnAnEmptyTail(t *testing.T) {
	path, _ := sealedFixture(t, 10, 10)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"entry":{"seq":11,"torn`), 0o600); err != nil {
		t.Fatal(err)
	}
	tail := filepath.Join(t.TempDir(), "ledger.jsonl")
	if _, err := l.Capture(tail); !errors.Is(err, ErrCaptureRefused) {
		t.Fatalf("foreign bytes on an empty tail were captured as the record: err=%v", err)
	}
	if _, serr := os.Stat(tail); !os.IsNotExist(serr) {
		t.Fatal("the refused capture left its copy behind")
	}
}
