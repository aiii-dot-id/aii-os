package ledger

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .

// .
// .
type refusingHeads struct {
	at  uint64
	err error
}

func (r *refusingHeads) VerifyHead(evt *Event) error {
	if evt.Seq == r.at {
		return fmt.Errorf("record %d: %w", evt.Seq, r.err)
	}
	return nil
}

// .
// .
func twoSegmentsAndATail(t *testing.T) (path string, pub []byte) {
	t.Helper()
	path, kp := sealedFixture(t, 30, 10, 20)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range []uint64{10, 20} {
		if err := l.Seal(head); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return path, kp.PublicKeyBytes()
}

func tailLines(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimRight(raw, "\n"), []byte{'\n'})
}

func writeTail(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	if err := os.WriteFile(path, append(bytes.Join(lines, []byte{'\n'}), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
func tamperPayload(line []byte) []byte {
	return bytes.Replace(line, []byte(`"content":"xxxx`), []byte(`"content":"yxxx`), 1)
}

func TestARefusalNamesTheRecordAndWhatWasProved(t *testing.T) {
	const tail, seg1, seg2 = "ledger.jsonl", "segment-1-10.jsonl.gz", "segment-11-20.jsonl.gz"
	anyHeads := func() HeadVerifier { return &acceptHeads{} }

	for name, tc := range map[string]struct {
		damage                  func(t *testing.T, path string)
		heads                   func() HeadVerifier
		is                      error
		seq                     uint64
		container               string
		proved, traversed, next uint64
		says, neverSays         []string
	}{
		"content mismatch in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[4] = tamperPayload(l[4])
				writeTail(t, path, l)
			},
			seq: 25, container: tail, proved: 24, traversed: 24,
			says: []string{"record 25 (ledger.jsonl): content mismatch", "expected ", "found ", "proved through record 24"},
		},
		// .
		// .
		// .
		// .
		"content mismatch inside the second segment": {
			damage: func(t *testing.T, path string) {
				seg := filepath.Join(filepath.Dir(path), seg2)
				l := gunzipLines(t, seg)
				l[4] = tamperPayload(l[4])
				gzipLines(t, seg, l)
			},
			seq: 15, container: seg2, proved: 10, traversed: 14,
			says:      []string{"record 15 (segment-11-20.jsonl.gz)", "proved through record 10", "links checked through record 14, which no verified proof covers yet"},
			neverSays: []string{"proved through record 14"},
		},
		"content mismatch inside the first segment": {
			damage: func(t *testing.T, path string) {
				seg := filepath.Join(filepath.Dir(path), seg1)
				l := gunzipLines(t, seg)
				l[4] = tamperPayload(l[4])
				gzipLines(t, seg, l)
			},
			seq: 5, container: seg1, proved: 0, traversed: 4,
			says: []string{"nothing proved; links checked through record 4"},
		},
		// .
		// .
		// .
		// .
		// .
		"a record numbered by the tamperer": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[4] = bytes.Replace(l[4], []byte(`"seq":25,`), []byte(`"seq":999999,`), 1)
				writeTail(t, path, l)
			},
			seq: 25, container: tail, proved: 24, traversed: 24,
			says:      []string{"record 25 (ledger.jsonl): seq mismatch", "expected record 25", "found a record numbered 999999", "proved through record 24"},
			neverSays: []string{"record 999999 (ledger.jsonl)"},
		},
		"a broken link in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				i := bytes.Index(l[5], []byte(`"prev":"`)) + len(`"prev":"`)
				flipped := bytes.Clone(l[5])
				if flipped[i] == 'a' {
					flipped[i] = 'b'
				} else {
					flipped[i] = 'a'
				}
				l[5] = flipped
				writeTail(t, path, l)
			},
			seq: 26, container: tail, proved: 25, traversed: 25,
			says: []string{"record 26 (ledger.jsonl): prev mismatch", "expected ", "found "},
		},
		// .
		// .
		"a proof that does not verify": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				i := bytes.Index(l[6], []byte(`"sig":"`)) + len(`"sig":"`) + 40
				flipped := bytes.Clone(l[6])
				if flipped[i] == 'A' {
					flipped[i] = 'B'
				} else {
					flipped[i] = 'A'
				}
				l[6] = flipped
				writeTail(t, path, l)
			},
			seq: 27, container: tail, proved: 26, traversed: 27,
			says: []string{"record 27 (ledger.jsonl): signature verification failed", "proved through record 26", "links checked through record 27"},
		},
		"a record with no proof in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				i := bytes.Index(l[7], []byte(`"sig":"`))
				l[7] = append(bytes.Clone(l[7][:i]), []byte(`"sig":""}`)...)
				writeTail(t, path, l)
			},
			is: ErrUnsignedRecord, seq: 28, container: tail, proved: 27, traversed: 28,
		},
		"a sealed segment and no witness keys": {
			damage: func(*testing.T, string) {},
			heads:  func() HeadVerifier { return nil },
			is:     ErrSealedWithoutWitness, seq: 1, container: seg1, proved: 0, traversed: 1,
		},
		// .
		// .
		// .
		"a line that does not parse, in a segment": {
			damage: func(t *testing.T, path string) {
				seg := filepath.Join(filepath.Dir(path), seg2)
				l := gunzipLines(t, seg)
				l[6] = []byte(`{"entry":`)
				gzipLines(t, seg, l)
			},
			seq: 0, proved: 10, traversed: 16, next: 17,
			says:      []string{"segment-11-20.jsonl.gz line 7: malformed line", "the walk expected record 17 next", "proved through record 10"},
			neverSays: []string{"record 7"},
		},
		// .
		// .
		"a line that does not parse, after blank lines in a segment": {
			damage: func(t *testing.T, path string) {
				seg := filepath.Join(filepath.Dir(path), seg2)
				l := gunzipLines(t, seg)
				l[6] = []byte(`{"entry":`)
				gzipLines(t, seg, append([][]byte{nil, nil}, l...))
			},
			seq: 0, proved: 10, traversed: 16, next: 17,
			says:      []string{"segment-11-20.jsonl.gz line 9: malformed line", "the walk expected record 17 next", "proved through record 10"},
			neverSays: []string{"line 7", "record 7"},
		},
		"a line that does not parse, in a tail that follows a sealing": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[2] = []byte(`{"entry":`)
				writeTail(t, path, l)
			},
			seq: 0, proved: 22, traversed: 22, next: 23,
			says:      []string{"ledger.jsonl line 3: malformed line", "the walk expected record 23 next", "proved through record 22"},
			neverSays: []string{"record 3"},
		},
		// .
		// .
		// .
		// .
		// .
		// .
		"a line that does not parse, after blank lines in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[2] = []byte(`{"entry":`)
				writeTail(t, path, append([][]byte{nil, nil}, l...))
			},
			seq: 0, proved: 22, traversed: 22, next: 23,
			says:      []string{"ledger.jsonl line 5: malformed line", "the walk expected record 23 next", "proved through record 22"},
			neverSays: []string{"line 3", "record 3"},
		},
		"a segment set that is not one run": {
			damage: func(t *testing.T, path string) {
				if err := os.Remove(filepath.Join(filepath.Dir(path), seg1)); err != nil {
					t.Fatal(err)
				}
			},
			is: ErrSegmentSet, seq: 0, proved: 0, traversed: 0, next: 1,
			says: []string{"nothing was established"},
		},
		"a tail that conflicts with its segment": {
			damage: func(t *testing.T, path string) {
				sealed := gunzipLines(t, filepath.Join(filepath.Dir(path), seg2))
				l := tailLines(t, path)
				writeTail(t, path, [][]byte{l[0], sealed[4]})
			},
			is: ErrTailConflict, seq: 0, proved: 21, traversed: 21, next: 22,
		},
		// .
		// .
		// .
		// .
		"a witness head refused inside a segment": {
			damage: func(*testing.T, string) {},
			heads: func() HeadVerifier {
				return &refusingHeads{at: 20, err: errors.New("receipt attests hash aa, the record before the head is bb")}
			},
			seq: 20, container: seg2, proved: 20, traversed: 20,
			says:      []string{"record 20 (segment-11-20.jsonl.gz): witness head: receipt attests hash"},
			neverSays: []string{"record 20: record 20", "witness head: record 20"},
		},
		"a witness key not held, inside a segment": {
			damage: func(*testing.T, string) {},
			heads:  func() HeadVerifier { return &refusingHeads{at: 10, err: ErrWitnessKeyUnknown} },
			is:     ErrWitnessKeyUnknown, seq: 10, container: seg1, proved: 10, traversed: 10,
		},
	} {
		t.Run(name, func(t *testing.T) {
			path, pub := twoSegmentsAndATail(t)
			tc.damage(t, path)
			heads := anyHeads
			if tc.heads != nil {
				heads = tc.heads
			}
			n, err := VerifyChain(path, pub, heads())
			if err == nil {
				t.Fatalf("THE DAMAGED CHAIN VERIFIED (%d records)", n)
			}
			if n != 0 {
				t.Fatalf("a refusal returned a count of %d: no count survives a refusal, the boundary is in the error", n)
			}
			var f *VerifyFailure
			if !errors.As(err, &f) {
				t.Fatalf("the refusal is not a *VerifyFailure: %T %v", err, err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("THE REFUSAL LOST ITS IDENTITY: errors.Is(%v) is false for %v", tc.is, err)
			}
			if f.Seq != tc.seq || f.Container != tc.container {
				t.Fatalf("names record %d in %q, want record %d in %q: %v", f.Seq, f.Container, tc.seq, tc.container, err)
			}
			if f.Proved.Seq != tc.proved || f.Traversed.Seq != tc.traversed {
				t.Fatalf("proved through %d, links checked through %d; want %d and %d: %v",
					f.Proved.Seq, f.Traversed.Seq, tc.proved, tc.traversed, err)
			}
			if tc.seq == 0 && f.NextSeq != tc.next {
				t.Fatalf("the walk expected record %d next, want %d: %v", f.NextSeq, tc.next, err)
			}
			if (f.Proved.Seq == 0) != (f.Proved.Hash == "") || (f.Traversed.Seq == 0) != (f.Traversed.Hash == "") {
				t.Fatalf("a boundary names a record without its hash, or a hash without a record: %+v %+v", f.Proved, f.Traversed)
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message does not say %q: %v", want, err)
				}
			}
			for _, never := range tc.neverSays {
				if strings.Contains(err.Error(), never) {
					t.Errorf("the message says %q: %v", never, err)
				}
			}
		})
	}
}

// .
// .
func TestAContentMismatchNamesBothHashes(t *testing.T) {
	path, pub := twoSegmentsAndATail(t)
	l := tailLines(t, path)
	before, err := decodeEvent(l[4])
	if err != nil {
		t.Fatal(err)
	}
	l[4] = tamperPayload(l[4])
	writeTail(t, path, l)
	_, err = VerifyChain(path, pub, &acceptHeads{})
	var f *VerifyFailure
	if !errors.As(err, &f) {
		t.Fatal(err)
	}
	if f.Expected != before.Content || f.Observed == "" || f.Observed == f.Expected || len(f.Observed) != 64 {
		t.Fatalf("expected %q (the entry declares %q), observed %q", f.Expected, before.Content, f.Observed)
	}
}

// .
// .
func TestAnUnheldKeyInTheTailIsStillAccepted(t *testing.T) {
	path, kp := sealedFixture(t, 12, 10)
	n, err := VerifyChain(path, kp.PublicKeyBytes(), &refusingHeads{at: 10, err: ErrWitnessKeyUnknown})
	if err != nil || n != 12 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// .
func TestTheReaderTagsEachRecordWithItsContainer(t *testing.T) {
	path, _ := twoSegmentsAndATail(t)
	want := map[uint64]string{1: "segment-1-10.jsonl.gz", 10: "segment-1-10.jsonl.gz", 11: "segment-11-20.jsonl.gz", 21: "ledger.jsonl", 30: "ledger.jsonl"}
	if err := Stream(path, func(evt *Event) error {
		if c, ok := want[evt.Seq]; ok && evt.Container() != c {
			t.Errorf("record %d came from %q, want %q", evt.Seq, evt.Container(), c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
