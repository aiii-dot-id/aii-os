package ledger

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// container_test.go — sealing and the containers it produces: a segment
// is published before the tail changes, the reader takes overlap and
// nothing else, opening finishes what a crash interrupted, and the
// structural rules (a segment closes under a head; a record without a
// proof lives only inside a segment) refuse everything else.

// sealedFixture writes n signed records with witness heads at the given
// seqs: system.witnessed records whose payload has the receipt shape.
// The ledger package does not judge receipts; the witness package's
// verifier does, and here a stub stands in for it.
func sealedFixture(t *testing.T, n int, heads ...uint64) (string, *crypto.KeyPair) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	isHead := map[uint64]bool{}
	for _, h := range heads {
		isHead[h] = true
	}
	for i := 1; i <= n; i++ {
		if isHead[uint64(i)] {
			_, err = l.Append(EventSystemWitnessed, 0, map[string]any{
				"receipt": map[string]any{"ledger_ordinal": i - 1, "ledger_hash": l.LastHash()},
			}, kp)
		} else {
			_, err = l.Append(EventExperienceCreate, 3, map[string]any{
				"id": "e", "content": strings.Repeat("x", 200),
			}, kp)
		}
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return path, kp
}

// acceptHeads is the stub head verifier: every head passes, counted, as
// a head attesting the record before it.
type acceptHeads struct{ n int }

func (a *acceptHeads) VerifyHead(evt *Event, _ func(uint64) (string, bool)) (uint64, error) {
	a.n++
	return evt.Seq - 1, nil
}

func entryHashes(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	if err := Stream(path, func(evt *Event) error {
		out = append(out, evt.EntryHash())
		return nil
	}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	return out
}

func firstTailSeq(t *testing.T, path string) uint64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := bytes.Cut(raw, []byte{'\n'})
	if len(bytes.TrimSpace(line)) == 0 {
		return 0
	}
	evt, err := decodeEvent(bytes.TrimSpace(line))
	if err != nil {
		t.Fatalf("tail's first line: %v", err)
	}
	return evt.Seq
}

func segmentNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if _, _, ok := parseSegmentName(e.Name()); ok {
			out = append(out, e.Name())
		}
	}
	return out
}

func tempNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}

func gunzipLines(t *testing.T, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for _, l := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(l)) > 0 {
			lines = append(lines, l)
		}
	}
	return lines
}

func gzipLines(t *testing.T, path string, lines [][]byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for _, l := range lines {
		gz.Write(append(bytes.Clone(l), '\n'))
	}
	gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE SEAL. Records through a head move into a segment without their
// proofs, the head keeps its proof, the tail starts after the head, the
// chain reads back identical, and the ledger keeps appending — on the
// reopened handle, not the file the rename replaced.
func TestSealMovesRecordsIntoASegmentAndTheChainReadsTheSame(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	before := entryHashes(t, path)

	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatalf("seal through 10: %v", err)
	}
	if got := segmentNames(t, dir); len(got) != 1 || got[0] != "segment-1-10.jsonl.gz" {
		t.Fatalf("segments after the first seal: %v", got)
	}
	if got := firstTailSeq(t, path); got != 11 {
		t.Fatalf("tail begins at %d, want 11", got)
	}
	if got := entryHashes(t, path); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatal("THE CHAIN CHANGED UNDER SEALING")
	}
	var sealedCount int
	if err := Stream(path, func(evt *Event) error {
		switch {
		case evt.Seq < 10:
			if !evt.Sealed() || evt.Sig != "" {
				t.Fatalf("record %d: sealed=%v signed=%v; interior records travel without their proof", evt.Seq, evt.Sealed(), evt.Sig != "")
			}
		case evt.Seq == 10:
			if !evt.Sealed() || evt.Sig == "" {
				t.Fatal("the head must keep its proof")
			}
		default:
			if evt.Sealed() || evt.Sig == "" {
				t.Fatalf("record %d: tail records carry their proof and are not sealed", evt.Seq)
			}
		}
		if evt.Sealed() {
			sealedCount++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sealedCount != 10 {
		t.Fatalf("%d sealed records, want 10", sealedCount)
	}
	heads := &acceptHeads{}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), heads); err != nil || n != 30 {
		t.Fatalf("verify with heads: n=%d err=%v", n, err)
	}
	if heads.n != 3 {
		t.Fatalf("head verifier saw %d heads, want 3", heads.n)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); !errors.Is(err, ErrSealedWithoutWitness) {
		t.Fatalf("a sealed segment without a witness verifier must refuse, got %v", err)
	}

	if err := l.Seal(20); err != nil {
		t.Fatalf("seal through 20: %v", err)
	}
	if got := segmentNames(t, dir); len(got) != 2 || got[1] != "segment-11-20.jsonl.gz" {
		t.Fatalf("segments after the second seal: %v", got)
	}
	if l.SealedSeq() != 20 {
		t.Fatalf("sealed through %d, want 20", l.SealedSeq())
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "after", "content": "seal"}, kp); err != nil {
		t.Fatalf("append after sealing: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// The append went to the file on disk under the ledger's name, not
	// to a handle the rename orphaned.
	l2, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if l2.LastSeq() != 31 || l2.SealedSeq() != 20 {
		t.Fatalf("reopened at %d (sealed %d), want 31 (20)", l2.LastSeq(), l2.SealedSeq())
	}
	if _, err := l2.Append(EventExperienceCreate, 3, map[string]any{"id": "again", "content": "x"}, kp); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	l2.Close()
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 32 {
		t.Fatalf("final verify: n=%d err=%v", n, err)
	}
}

func TestASealGoesThroughWhileAReaderWalksTheRecord(t *testing.T) {
	path, _ := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	before := entryHashes(t, path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var seen []uint64
	sealed := false
	if err := Stream(path, func(evt *Event) error {
		seen = append(seen, evt.Seq)
		if evt.Seq == 5 && !sealed {
			sealed = true
			for _, head := range []uint64{10, 20} {
				if err := l.Seal(head); err != nil {
					t.Fatalf("the seal through %d was refused while a reader had the tail open: %v", head, err)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("the reader lost the record at the seal: %v", err)
	}
	if !sealed {
		t.Fatal("rig: the reader never reached record 5")
	}
	if len(seen) != 30 {
		t.Fatalf("the reader read %d records of the 30 it opened: %v", len(seen), seen)
	}
	for i, seq := range seen {
		if seq != uint64(i+1) {
			t.Fatalf("the reader's record %d is %d: %v", i+1, seq, seen)
		}
	}
	if got := segmentNames(t, dir); len(got) != 2 {
		t.Fatalf("segments after two seals under a reader: %v", got)
	}
	if got := firstTailSeq(t, path); got != 21 {
		t.Fatalf("tail begins at %d, want 21", got)
	}
	if got := entryHashes(t, path); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatal("THE CHAIN CHANGED UNDER SEALING BESIDE A READER")
	}
	if got := tempNames(t, dir); len(got) != 0 {
		t.Fatalf("a seal beside a reader left debris: %v", got)
	}
}

func TestSealRefusesWhatIsNotAHead(t *testing.T) {
	path, _ := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	raw, _ := os.ReadFile(path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Seal(15); !errors.Is(err, ErrSegmentWithoutHead) {
		t.Fatalf("sealing through an ordinary record: %v", err)
	}
	if err := l.Seal(31); err == nil {
		t.Fatal("sealing beyond the last record must refuse")
	}
	if got := segmentNames(t, dir); len(got) != 0 {
		t.Fatalf("a refused seal left a segment: %v", got)
	}
	if got := tempNames(t, dir); len(got) != 0 {
		t.Fatalf("a refused seal left debris: %v", got)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(raw, after) {
		t.Fatal("a refused seal changed the tail")
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err == nil {
		t.Fatal("sealing an already sealed record must refuse")
	}
}

// THE ONE INTERMEDIATE STATE. A segment published and the tail still
// holding its records is what a crash between the two steps leaves. The
// reader verifies the overlap and delivers each record once; opening
// the ledger finishes the seal.
func TestOpeningFinishesAnInterruptedSeal(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	before := entryHashes(t, path)
	whole, _ := os.ReadFile(path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	l.Close()
	// Put the pre-seal tail back beside the published segment.
	if err := os.WriteFile(path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 30 {
		t.Fatalf("the overlap state must verify as one chain: n=%d err=%v", n, err)
	}
	l2, err := New(path)
	if err != nil {
		t.Fatalf("opening the overlap state: %v", err)
	}
	defer l2.Close()
	if got := firstTailSeq(t, path); got != 11 {
		t.Fatalf("opening did not finish the seal: tail begins at %d", got)
	}
	if l2.LastSeq() != 30 || l2.SealedSeq() != 10 {
		t.Fatalf("state after finishing: last %d sealed %d", l2.LastSeq(), l2.SealedSeq())
	}
	if got := entryHashes(t, path); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatal("finishing the seal changed the chain")
	}
	if got := tempNames(t, filepath.Dir(path)); len(got) != 0 {
		t.Fatalf("debris after finishing: %v", got)
	}
	if _, err := l2.Append(EventExperienceCreate, 3, map[string]any{"id": "z", "content": "x"}, kp); err != nil {
		t.Fatalf("append after finishing: %v", err)
	}
}

// OVERLAP IS VERIFIED, NOT ASSUMED. A tail whose repeated records differ
// from the segment's is a conflict: the reader refuses, opening refuses,
// and the tail is left exactly as found.
func TestATailConflictingWithItsSegmentRefusesToOpen(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	whole, _ := os.ReadFile(path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	l.Close()
	// Record 5 in the restored tail with a different entry (ring 3 → 4):
	// still canonical, differently hashed.
	lines := bytes.Split(whole, []byte{'\n'})
	lines[4] = bytes.Replace(lines[4], []byte(`"ring":3,"seq":5,`), []byte(`"ring":4,"seq":5,`), 1)
	if !bytes.Contains(lines[4], []byte(`"ring":4,"seq":5,`)) {
		t.Fatal("fixture: record 5 not rewritten")
	}
	conflicting := bytes.Join(lines, []byte{'\n'})
	if err := os.WriteFile(path, conflicting, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrTailConflict) {
		t.Fatalf("the reader must refuse a conflicting overlap, got %v", err)
	}
	if _, err := New(path); !errors.Is(err, ErrTailConflict) {
		t.Fatalf("opening must refuse a conflicting overlap, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, conflicting) {
		t.Fatal("a refused open changed the tail")
	}
}

// A SEGMENT CLOSES UNDER A HEAD. One that ends with an ordinary record
// is not a segment, whatever its name says.
func TestASegmentMustCloseUnderAHead(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	whole, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimSpace(whole), []byte{'\n'})
	gzipLines(t, filepath.Join(dir, "segment-1-5.jsonl.gz"), lines[:5])
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrSegmentWithoutHead) {
		t.Fatalf("want ErrSegmentWithoutHead, got %v", err)
	}
	if _, err := New(path); !errors.Is(err, ErrSegmentWithoutHead) {
		t.Fatalf("opening: want ErrSegmentWithoutHead, got %v", err)
	}
}

// A RECORD WITHOUT A PROOF LIVES ONLY INSIDE A SEGMENT. In the tail it
// is refused, sealed or not around it.
func TestAnUnsignedRecordOutsideASegmentIsRefused(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	whole, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimSpace(whole), []byte{'\n'})
	evt, err := decodeEvent(lines[14])
	if err != nil {
		t.Fatal(err)
	}
	evt.Sig = ""
	stripped, err := evt.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	lines[14] = stripped
	if err := os.WriteFile(path, append(bytes.Join(lines, []byte{'\n'}), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrUnsignedRecord) {
		t.Fatalf("want ErrUnsignedRecord, got %v", err)
	}
}

// INTERIOR RECORDS STAND ON THE CHAIN. Change one byte of a sealed
// record's payload and the chain breaks at that record.
func TestATamperedSealedRecordBreaksTheChain(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	l.Close()
	seg := filepath.Join(dir, "segment-1-10.jsonl.gz")
	lines := gunzipLines(t, seg)
	lines[4] = bytes.Replace(lines[4], []byte(`"content":"xxxx`), []byte(`"content":"yxxx`), 1)
	gzipLines(t, seg, lines)
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err == nil {
		t.Fatal("A TAMPERED SEALED RECORD VERIFIED")
	}
}

// SEGMENTS FORM ONE RUN FROM RECORD 1. A gap or a renamed segment is not
// a state sealing produces.
func TestSegmentsMustFormOneRun(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	dir := filepath.Dir(path)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(20); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := os.Rename(filepath.Join(dir, "segment-11-20.jsonl.gz"), filepath.Join(dir, "segment-12-20.jsonl.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrSegmentSet) {
		t.Fatalf("renamed segment: want ErrSegmentSet, got %v", err)
	}
	os.Rename(filepath.Join(dir, "segment-12-20.jsonl.gz"), filepath.Join(dir, "segment-11-20.jsonl.gz"))
	if err := os.Remove(filepath.Join(dir, "segment-1-10.jsonl.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); !errors.Is(err, ErrSegmentSet) {
		t.Fatalf("missing first segment: want ErrSegmentSet, got %v", err)
	}
}

// A SEAL THROUGH THE LAST RECORD LEAVES AN EMPTY TAIL, and the empty
// tail is a state the ledger opens and appends to.
func TestASealThroughTheLastRecordLeavesAnEmptyTail(t *testing.T) {
	path, kp := sealedFixture(t, 30, 10, 20, 30)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Seal(30); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "e", "content": "x"}, kp); err != nil {
		t.Fatalf("append onto an empty tail: %v", err)
	}
	l.Close()
	if st, _ := os.Stat(path); st == nil {
		t.Fatal("the tail file is gone")
	}
	l2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if l2.LastSeq() != 31 || l2.SealedSeq() != 30 {
		t.Fatalf("reopened at %d (sealed %d)", l2.LastSeq(), l2.SealedSeq())
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 31 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

var sealSteps = []string{"segment-written", "segment-renamed", "segment-published", "tail-written", "tail-renamed", "tail-published"}

func TestSealSurvivesACrashAtEveryStep(t *testing.T) {
	for _, step := range sealSteps {
		t.Run(step, func(t *testing.T) {
			path, kp := sealedFixture(t, 30, 10, 20, 30)
			dir := filepath.Dir(path)
			before := entryHashes(t, path)

			child := exec.Command(os.Args[0], "-test.run=^TestSealCrashChild$", "-test.count=1")
			child.Env = append(os.Environ(), "AII_LEDGER_CRASH_PATH="+path, "AII_LEDGER_CRASH_AT="+step)
			out, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("child did not die at %s (err=%v):\n%s", step, err, out)
			}

			l, err := New(path)
			if err != nil {
				t.Fatalf("opening after a crash at %s: %v", step, err)
			}
			if got := tempNames(t, dir); len(got) != 0 {
				t.Fatalf("debris after a crash at %s: %v", step, got)
			}
			segs := segmentNames(t, dir)
			switch {
			case len(segs) == 0:
				if l.SealedSeq() != 0 || firstTailSeq(t, path) != 1 {
					t.Fatalf("no segment but sealed %d, tail from %d", l.SealedSeq(), firstTailSeq(t, path))
				}
			case len(segs) == 1 && segs[0] == "segment-1-10.jsonl.gz":
				if l.SealedSeq() != 10 || firstTailSeq(t, path) != 11 {
					t.Fatalf("segment published but sealed %d, tail from %d", l.SealedSeq(), firstTailSeq(t, path))
				}
			default:
				t.Fatalf("unexpected segments after a crash at %s: %v", step, segs)
			}
			if got := entryHashes(t, path); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Fatalf("THE CHAIN CHANGED ACROSS A CRASH AT %s", step)
			}
			if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 30 {
				t.Fatalf("verify after a crash at %s: n=%d err=%v", step, n, err)
			}
			// Life goes on: the next seal and the next append work.
			next := uint64(10)
			if l.SealedSeq() == 10 {
				next = 20
			}
			if err := l.Seal(next); err != nil {
				t.Fatalf("seal through %d after a crash at %s: %v", next, step, err)
			}
			if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "e", "content": "x"}, kp); err != nil {
				t.Fatalf("append after a crash at %s: %v", step, err)
			}
			l.Close()
			if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 31 {
				t.Fatalf("final verify after a crash at %s: n=%d err=%v", step, n, err)
			}
		})
	}
}

// TestSealCrashChild is the crash suite's child: it seals through record
// 10 and dies at the step named in the environment. Skipped when run
// directly.
func TestSealCrashChild(t *testing.T) {
	path := os.Getenv("AII_LEDGER_CRASH_PATH")
	if path == "" {
		t.Skip("child of TestSealSurvivesACrashAtEveryStep")
	}
	at := os.Getenv("AII_LEDGER_CRASH_AT")
	sealStep = func(step string) error {
		if step == at {
			os.Exit(7)
		}
		return nil
	}
	l, err := New(path)
	if err != nil {
		t.Fatalf("child open: %v", err)
	}
	if err := l.Seal(10); err != nil {
		t.Fatalf("child seal: %v", err)
	}
	l.Close()
}
