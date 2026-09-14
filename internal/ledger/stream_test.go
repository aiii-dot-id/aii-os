package ledger

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// .
// .
func writeSignedLedger(t *testing.T, n, size int) (string, *crypto.KeyPair) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("x", size)
	for i := 0; i < n; i++ {
		if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "e", "content": filler}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return path, kp
}

// .
// .
// .
// .
// .
func TestStreamMemoryIsBoundedByOneRecord(t *testing.T) {
	path, _ := writeSignedLedger(t, 48, 96<<10)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() < 4<<20 {
		t.Fatalf("fixture is only %d bytes", st.Size())
	}
	// .
	// .
	// .
	runtime.GC()
	var before, during runtime.MemStats
	runtime.ReadMemStats(&before)
	n := 0
	if err := Stream(path, func(*Event) error {
		n++
		if n == 48 {
			runtime.GC()
			runtime.ReadMemStats(&during)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 48 {
		t.Fatalf("streamed %d records, want 48", n)
	}
	grew := int64(during.HeapAlloc) - int64(before.HeapAlloc)
	if grew > 3<<19 {
		t.Fatalf("live heap grew by %d bytes at the last record of a %d-byte file — the reader is holding records", grew, st.Size())
	}
}

// .
// .
func TestStreamStopsEarly(t *testing.T) {
	path, _ := writeSignedLedger(t, 2, 16)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("{garbage\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	seen := 0
	err = Stream(path, func(*Event) error {
		seen++
		return ErrStop
	})
	if err != nil || seen != 1 {
		t.Fatalf("stop after the first record: seen=%d err=%v", seen, err)
	}
	// .
	if err := Stream(path, func(*Event) error { return nil }); err == nil {
		t.Fatal("a malformed record was streamed without error")
	}
	// .
	boom := errors.New("boom")
	if err := Stream(path, func(*Event) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("callback error lost: %v", err)
	}
}

// .
// .
func TestVerifyChainCountsWhatItVerified(t *testing.T) {
	path, kp := writeSignedLedger(t, 5, 16)
	n, err := VerifyChain(path, kp.PublicKey, nil)
	if err != nil || n != 5 {
		t.Fatalf("verified %d records (%v), want 5", n, err)
	}
}

// .
// .
// .
// .
func TestTheRuntimeNeverLoadsTheWholeLedger(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if strings.HasPrefix(rel, filepath.Join("internal", "ledger")+string(filepath.Separator)) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), "ledger.ReadAll(") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) != 0 {
		t.Fatalf("the runtime loads the whole ledger in: %v — stream it (ledger.Stream) instead", offenders)
	}
}
