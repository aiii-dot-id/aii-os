package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// tailPayload is belief-shaped and about 1.7 KB, so a stored line is
// about 8 KB, as a live record is (6.2 KB of it the proof).
func tailPayload(i int) map[string]any {
	return map[string]any{
		"id":        fmt.Sprintf("belief-%06d", i),
		"statement": strings.Repeat("The operator prefers concise answers with evidence cited inline. ", 12),
		"context":   map[string]any{"turn": i, "note": strings.Repeat("context text ", 20)},
	}
}

func tailLedger(tb testing.TB, n int) (*Ledger, *crypto.KeyPair) {
	tb.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		tb.Fatal(err)
	}
	l, err := New(filepath.Join(tb.TempDir(), "ledger.jsonl"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { l.Close() })
	for i := 0; i < n; i++ {
		if _, err := l.Append(EventBeliefPromote, 2, tailPayload(i), kp); err != nil {
			tb.Fatal(err)
		}
	}
	return l, kp
}

func TestTheTailCheckReadsItsDesignedDepth(t *testing.T) {
	l, _ := tailLedger(t, 40)
	data, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	for _, k := range []int{0, 1, tailCheckDepth} {
		got, err := readTailEvents(l.file, k)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != k+1 {
			t.Fatalf("k=%d: %d records, want %d", k, len(got), k+1)
		}
		for i, e := range got {
			want, err := decodeEvent(lines[len(lines)-k-1+i])
			if err != nil {
				t.Fatal(err)
			}
			if e.EntryHash() != want.EntryHash() || e.Sig != want.Sig || !bytes.Equal(e.Payload, want.Payload) {
				t.Fatalf("k=%d: record %d is not the file's", k, i)
			}
		}
	}

	damaged := func(at int) error {
		t.Helper()
		bad := append([][]byte(nil), lines...)
		bad[at] = []byte(`{"entry":`)
		path := filepath.Join(t.TempDir(), "damaged.jsonl")
		if err := os.WriteFile(path, append(bytes.Join(bad, []byte{'\n'}), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		_, err = readTailEvents(f, tailCheckDepth)
		return err
	}
	if err := damaged(len(lines) - 1 - tailCheckDepth); err == nil {
		t.Fatal("a damaged line inside the check's depth was accepted")
	}
	if err := damaged(len(lines) - 2 - tailCheckDepth); err != nil {
		t.Fatalf("a line before the check's depth was read: %v", err)
	}
}

// BenchmarkTheTailCheck is the mint's tail read with the check's depth,
// over 128 records of live size.
func BenchmarkTheTailCheck(b *testing.B) {
	l, _ := tailLedger(b, 128)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := readTailEvents(l.file, tailCheckDepth); err != nil {
			b.Fatal(err)
		}
	}
}

func decodeEventTwice(raw []byte) (Event, error) {
	if _, err := canonicaljson.CanonicalizeV1(raw); err != nil {
		return Event{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var evt *Event
	if err := dec.Decode(&evt); err != nil {
		return Event{}, err
	}
	if evt == nil {
		return Event{}, fmt.Errorf("event must be a JSON object")
	}
	return *evt, nil
}

func TestARecordDecodesOnceAsItDidTwice(t *testing.T) {
	l, _ := tailLedger(t, 1)
	data, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	good := bytes.TrimSpace(data)
	inputs := [][]byte{
		good,
		[]byte("null"), []byte("{}"), []byte("[]"), []byte(`"x"`), []byte("1"),
		append(append([]byte{}, good...), []byte(" {}")...),
		append(append([]byte{}, good...), []byte("x")...),
		bytes.Replace(good, []byte(`{"entry":`), []byte(`{ "entry" :`), 1),
		bytes.Replace(good, []byte(`{"entry":`), []byte(`{"extra":1,"entry":`), 1),
		bytes.Replace(good, []byte(`{"entry":`), []byte(`{"sig":"AA","entry":`), 1),
		bytes.Replace(good, []byte(`,"payload":`), []byte(`,"payload":{"a":1},"payload":`), 1),
		bytes.Replace(good, []byte(`"sig":`), []byte(`"sig" : `), 1),
	}
	vectors, _ := filepath.Glob("testdata/*")
	for _, path := range vectors {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, ln := range bytes.Split(raw, []byte{'\n'}) {
			if ln = bytes.TrimSpace(ln); len(ln) > 0 {
				inputs = append(inputs, ln)
			}
		}
	}
	if len(inputs) < 100 {
		t.Fatalf("%d inputs: the gold vectors were not read", len(inputs))
	}
	accepted := 0
	for i, in := range inputs {
		once, errOnce := decodeEvent(in)
		twice, errTwice := decodeEventTwice(in)
		if (errOnce == nil) != (errTwice == nil) {
			t.Errorf("input %d (%.60q): once %v, twice %v", i, in, errOnce, errTwice)
			continue
		}
		if errOnce == nil {
			accepted++
			if once.EntryHash() != twice.EntryHash() || once.Sig != twice.Sig || !bytes.Equal(once.Payload, twice.Payload) {
				t.Errorf("input %d decoded differently", i)
			}
		}
	}
	if accepted == 0 || accepted == len(inputs) {
		t.Fatalf("%d of %d accepted: the inputs decide nothing", accepted, len(inputs))
	}
}

// BenchmarkDecodeEvent is one record line of live size, once against twice.
func BenchmarkDecodeEvent(b *testing.B) {
	l, _ := tailLedger(b, 1)
	data, err := os.ReadFile(l.path)
	if err != nil {
		b.Fatal(err)
	}
	line := bytes.TrimSpace(data)
	for name, decode := range map[string]func([]byte) (Event, error){"once": decodeEvent, "twice": decodeEventTwice} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := decode(line); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
