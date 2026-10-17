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

// verify_failure_test.go — a refusal says which record, by its seq and
// its container; what disagreed; and what the walk had proved and what it
// had only read. The falsifier throughout: a boundary that names a record
// whose proof was not verified.

// refusingHeads accepts every head but one, and answers for that one as
// the witness verifier does — naming the record itself.
type refusingHeads struct {
	at  uint64
	err error
}

func (r *refusingHeads) VerifyHead(evt *Event, _ func(uint64) (string, bool)) (uint64, error) {
	if evt.Seq == r.at {
		return 0, fmt.Errorf("record %d: %w", evt.Seq, r.err)
	}
	return evt.Seq - 1, nil
}

// twoSegmentsAndATail is records 1-10 and 11-20 sealed under their heads
// and 21-30 in the tail.
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

// tamperPayload changes one payload byte and leaves the entry alone: the
// entry still names the old payload's hash.
func tamperPayload(line []byte) []byte {
	return bytes.Replace(line, []byte(`"content":"xxxx`), []byte(`"content":"yxxx`), 1)
}

func TestARefusalNamesTheRecordAndWhatWasProved(t *testing.T) {
	const tail, seg1, seg2 = "ledger.jsonl", "segment-1-10.jsonl.gz", "segment-11-20.jsonl.gz"
	anyHeads := func() HeadVerifier { return &acceptHeads{} }

	for name, tc := range map[string]struct {
		damage                  func(t *testing.T, path string)
		heads                   func() HeadVerifier
		is                      error  // the identity the refusal must keep; nil = none to keep
		seq                     uint64 // 0 = the refusal names no record
		container               string
		proved, traversed, next uint64
		says, neverSays         []string
	}{
		"content mismatch in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[4] = tamperPayload(l[4]) // record 25
				writeTail(t, path, l)
			},
			seq: 25, container: tail, proved: 24, traversed: 24,
			says: []string{"record 25 (ledger.jsonl): content mismatch", "expected ", "found ", "proved through record 24"},
		},
		// THE ONE THE FIRST DESIGN GOT WRONG. Records 11-14 were read and
		// their links hold, and nothing proves them: a sealed record is
		// proved by its segment's closing head, record 20, which the walk
		// never reached. The boundary is the previous head.
		"content mismatch inside the second segment": {
			damage: func(t *testing.T, path string) {
				seg := filepath.Join(filepath.Dir(path), seg2)
				l := gunzipLines(t, seg)
				l[4] = tamperPayload(l[4]) // record 15
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
				l[4] = tamperPayload(l[4]) // record 5
				gzipLines(t, seg, l)
			},
			seq: 5, container: seg1, proved: 0, traversed: 4,
			says: []string{"nothing proved; links checked through record 4"},
		},
		// THE ONE BRANCH WHERE THE NUMBER IN THE RECORD IS THE TAMPERER'S TO
		// CHOOSE. The record that failed is the one at this place in the
		// chain — record 25 — and what it calls itself is the OBSERVED
		// value: a refusal naming "record 999999" sent its reader to a
		// record the file does not hold.
		"a record numbered by the tamperer": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[4] = bytes.Replace(l[4], []byte(`"seq":25,`), []byte(`"seq":999999,`), 1) // record 25
				writeTail(t, path, l)
			},
			seq: 25, container: tail, proved: 24, traversed: 24,
			says:      []string{"record 25 (ledger.jsonl): seq mismatch", "expected record 25", "found a record numbered 999999", "proved through record 24"},
			neverSays: []string{"record 999999 (ledger.jsonl)"},
		},
		"a broken link in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				i := bytes.Index(l[5], []byte(`"prev":"`)) + len(`"prev":"`) // record 26
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
		// The links hold through 27 and its proof does not verify: read,
		// not proved.
		"a proof that does not verify": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				i := bytes.Index(l[6], []byte(`"sig":"`)) + len(`"sig":"`) + 40 // record 27
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
				i := bytes.Index(l[7], []byte(`"sig":"`)) // record 28
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
		// A LINE IS NOT A RECORD. The seventh line of the second segment
		// is where record 17 should be; the message used to call it
		// "record 7".
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
		// The same in a sealed segment, which is read through the same
		// scanner and carried the same wrong count.
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
				l[2] = []byte(`{"entry":`) // where record 23 should be
				writeTail(t, path, l)
			},
			seq: 0, proved: 22, traversed: 22, next: 23,
			says:      []string{"ledger.jsonl line 3: malformed line", "the walk expected record 23 next", "proved through record 22"},
			neverSays: []string{"record 3"},
		},
		// AND A BLANK LINE IS NOT NOTHING. The scanner steps over
		// blank lines without delivering them, so counting what it
		// delivered called the fifth line the third and sent an
		// operator to a line that was perfectly fine. The two cases
		// above cannot catch this: where every line is a record the
		// two counts agree, and the bug hides exactly where they part.
		"a line that does not parse, after blank lines in the tail": {
			damage: func(t *testing.T, path string) {
				l := tailLines(t, path)
				l[2] = []byte(`{"entry":`) // where record 23 should be
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
				writeTail(t, path, [][]byte{l[0], sealed[4]}) // record 21, then sealed record 15 again
			},
			is: ErrTailConflict, seq: 0, proved: 21, traversed: 21, next: 22,
		},
		// The identity's proof on the head verifies — that is a fact, and
		// the prefix is proved by it. What failed is the witness, and the
		// message says so once: the witness verifier names the record
		// itself, and the record is not named twice.
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

// A CONTENT MISMATCH NAMES BOTH HASHES, and they are the right two: what
// the entry declares, and what the payload on disk hashes to.
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

// A HEAD WHOSE WITNESS KEY IS NOT HELD STANDS IN THE TAIL ON THE
// IDENTITY'S PROOF, as it always has: the diagnostics change no verdict.
func TestAnUnheldKeyInTheTailIsStillAccepted(t *testing.T) {
	path, kp := sealedFixture(t, 12, 10)
	n, err := VerifyChain(path, kp.PublicKeyBytes(), &refusingHeads{at: 10, err: ErrWitnessKeyUnknown})
	if err != nil || n != 12 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// THE READER SAYS WHERE A RECORD CAME FROM.
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
