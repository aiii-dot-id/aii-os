package ledger

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A LEDGER THAT GROWS WHILE IT IS READ IS READ AS IT STOOD WHEN THE READING
// BEGAN. The host appends a record and starts writing the next while the
// operator's reading is walking: the reading ends at the length it opened
// with, whole and verified, and does not trip on the line being written. A
// reading begun after sees the record, and stops at the last complete line
// before the one still being written, which it counts as cut.
func TestVerifyChainReadingReadsTheLedgerAsItStoodWhenItBegan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 3; i++ {
		if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "e", "content": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	const partial = `{"entry":{"content":"9f`
	key := func(first *Event) ([]byte, error) { return kp.PublicKeyBytes(), nil }
	none := func(string, int, []byte, error) error {
		t.Fatal("a reading that verified was handed a rest")
		return nil
	}

	var lines [][]byte
	grew := false
	n, cut, err := VerifyChainReading(path, key, nil, func(evt *Event, line []byte) error {
		lines = append(lines, bytes.Clone(line))
		if grew {
			return nil
		}
		grew = true
		if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "e", "content": "appended while read"}, kp); err != nil {
			return err
		}
		w, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return err
		}
		defer w.Close()
		_, err = w.WriteString(partial)
		return err
	}, none)
	if err != nil || n != 3 || cut != 0 {
		t.Fatalf("the reading during growth: n=%d cut=%d err=%v; want the three records it opened with, nothing cut", n, cut, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file := bytes.Split(raw, []byte{'\n'})
	for i, line := range lines {
		if !bytes.Equal(line, file[i]) {
			t.Fatalf("record %d was handed %q, the file holds %q", i+1, line, file[i])
		}
	}

	n, cut, err = VerifyChainReading(path, key, nil, func(*Event, []byte) error { return nil }, none)
	if err != nil || n != 4 || cut != int64(len(partial)) {
		t.Fatalf("the reading after: n=%d cut=%d err=%v; want 4 records and %d bytes cut", n, cut, err, len(partial))
	}
}
