package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedOpeningPreservesUnprovedOverlap(t *testing.T) {
	path, kp := sealedFixture(t, 12, 10)
	whole := openingBytes(t, path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	openingWrite(t, path, whole)
	seg := filepath.Join(filepath.Dir(path), "segment-1-10.jsonl.gz")
	goodSegment := openingBytes(t, seg)
	lines := gunzipLines(t, seg)
	var head Event
	if err := json.Unmarshal(lines[len(lines)-1], &head); err != nil {
		t.Fatal(err)
	}
	head.Sig = "AAAA"
	lines[len(lines)-1], err = json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	gzipLines(t, seg, lines)
	badSegment := openingBytes(t, seg)
	opened, _, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
	if opened != nil {
		opened.Close()
		t.Fatal("unproved segment became writable")
	}
	var refusal *VerifyFailure
	if !errors.As(err, &refusal) {
		t.Fatalf("want signature refusal: %v", err)
	}
	if !bytes.Equal(whole, openingBytes(t, path)) || !bytes.Equal(badSegment, openingBytes(t, seg)) {
		t.Fatal("verification refusal removed original evidence")
	}
	openingWrite(t, seg, goodSegment)
	visits := 0
	opened, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, func(*Event) error {
		visits++
		second, err := New(path)
		if second != nil {
			second.Close()
			t.Fatal("verification lost directory ownership")
		}
		if !errors.Is(err, ErrLedgerInUse) {
			t.Fatalf("second opener: %v", err)
		}
		return nil
	}, func(through uint64) error {
		if through != 12 || !bytes.Equal(whole, openingBytes(t, path)) {
			t.Fatal("cleanup preceded completeness check")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if n != 12 || visits != 12 || opened.LastSeq() != 12 || firstTailSeq(t, path) != 11 {
		t.Fatalf("boundary/walk mismatch: count=%d visits=%d last=%d", n, visits, opened.LastSeq())
	}
}

func TestVerifiedOpeningFramesBeforeRecovery(t *testing.T) {
	for _, kind := range []string{"partial", "signed-no-newline", "complete-malformed", "whitespace", "crlf", "sealed-first-partial"} {
		t.Run(kind, func(t *testing.T) {
			path, kp := sealedFixture(t, 3, 3)
			whole := openingBytes(t, path)
			prefix, suffix := whole, []byte(nil)
			want := 3
			switch kind {
			case "partial":
				suffix = []byte(`{"seq":4`)
			case "signed-no-newline":
				end := bytes.LastIndexByte(whole[:len(whole)-1], '\n') + 1
				prefix, suffix, want = whole[:end], whole[end:len(whole)-1], 2
			case "complete-malformed":
				suffix = []byte("not-json\n")
			case "whitespace":
				prefix = append(prefix, []byte(" \r\t\u2003")...)
			case "crlf":
				prefix = bytes.ReplaceAll(prefix, []byte("\n"), []byte("\r\n"))
			case "sealed-first-partial":
				l, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := l.Seal(3); err != nil {
					t.Fatal(err)
				}
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				prefix, suffix = nil, []byte(`{"seq":4`)
			}
			input := append(append([]byte(nil), prefix...), suffix...)
			openingWrite(t, path, input)
			l, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
			if kind == "complete-malformed" {
				if l != nil {
					l.Close()
					t.Fatal("accepted malformed complete record")
				}
				if !errors.Is(err, ErrRecordUnreadable) || !bytes.Equal(input, openingBytes(t, path)) {
					t.Fatalf("refusal/preservation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n != want || l.LastSeq() != uint64(want) {
				t.Fatalf("count=%d last=%d want=%d", n, l.LastSeq(), want)
			}
			if !bytes.Equal(prefix, openingBytes(t, path)) {
				t.Fatal("writable bytes differ from verified framing")
			}
			if len(suffix) > 0 {
				files, err := filepath.Glob(path + ".torn-*")
				if err != nil {
					t.Fatal(err)
				}
				if len(files) != 1 || !bytes.Equal(suffix, openingBytes(t, files[0])) {
					t.Fatal("quarantine is not the exact excluded suffix")
				}
			}
			if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "after", "content": "after recovery"}, kp); err != nil {
				t.Fatal(err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			l, n, err = OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if n != want+1 {
				t.Fatalf("restart count=%d", n)
			}
		})
	}
}

func TestVerifiedOpeningCheckRefusalPreservesSuffixAndReleasesLock(t *testing.T) {
	path, kp := sealedFixture(t, 2)
	input := append(openingBytes(t, path), []byte(`{"seq":3`)...)
	openingWrite(t, path, input)
	cause := fmt.Errorf("test completeness refusal")
	l, _, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, func(uint64) error { return cause })
	if l != nil || !errors.Is(err, cause) {
		t.Fatalf("refusal: %v", err)
	}
	if !bytes.Equal(input, openingBytes(t, path)) {
		t.Fatal("refusal removed suffix")
	}
	l, _, err = OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil)
	if err != nil {
		t.Fatalf("refusal leaked ownership: %v", err)
	}
	l.Close()
}

func openingBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifiedOpeningRetriesAfterQuarantineWithoutLosingOrDuplicatingBytes(t *testing.T) {
	path, kp := sealedFixture(t, 2)
	prefix := openingBytes(t, path)
	suffix := bytes.Repeat([]byte("x"), 3*64*1024+17)
	input := append(append([]byte(nil), prefix...), suffix...)
	openingWrite(t, path, input)
	fault := &os.PathError{Op: "truncate", Path: path, Err: os.ErrPermission}
	for range 2 {
		l, _, err := openVerified(path, kp.PublicKeyBytes(), nil, nil, nil, func() error { return fault })
		if l != nil || !errors.Is(err, fault) {
			t.Fatalf("injected failure: %v", err)
		}
		if !bytes.Equal(input, openingBytes(t, path)) {
			t.Fatal("failure changed the original tail")
		}
		files, err := filepath.Glob(path + ".torn-*")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || !bytes.Equal(suffix, openingBytes(t, files[0])) {
			t.Fatal("retry duplicated or changed quarantine bytes")
		}
	}
	l, _, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !bytes.Equal(prefix, openingBytes(t, path)) {
		t.Fatal("recovery did not retain the verified prefix")
	}
	files := tempNames(t, filepath.Dir(path))
	if len(files) != 0 {
		t.Fatalf("temporary files leaked: %v", files)
	}
}

func TestVerifiedOpeningNeverRebirthsAnEmptyCandidate(t *testing.T) {
	path, kp := sealedFixture(t, 1)
	input := bytes.TrimSuffix(openingBytes(t, path), []byte("\n"))
	openingWrite(t, path, input)
	l, _, err := OpenVerified(path, kp.PublicKeyBytes(), nil, nil, nil)
	if l != nil || !errors.Is(err, ErrRecordUnreadable) {
		t.Fatalf("empty candidate: %v", err)
	}
	if !bytes.Equal(input, openingBytes(t, path)) {
		t.Fatal("empty-candidate refusal changed history")
	}
}

func TestVerifiedOpeningRecreatesOnlyAProvedEmptyTail(t *testing.T) {
	path, kp := sealedFixture(t, 3, 3)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(3); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	l, n, err := OpenVerified(path, kp.PublicKeyBytes(), &acceptHeads{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if n != 3 || l.LastSeq() != 3 || len(openingBytes(t, path)) != 0 {
		t.Fatal("sealed-only identity did not resume at its proved boundary")
	}
}
func openingWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
