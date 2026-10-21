package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// rewrap_sealed_test.go — a re-wrap reads the record a boot reads, the
// way a boot reads it: the sealed segments through the container and then
// the tail, inside the one verifying walk. It read the tail file alone
// with a reader of its own, so an identity with a sealed segment was told
// its record was not its own, and whatever a tail held was re-signed with
// its links and proofs unread.

// sealedIdentity writes an identity's record of n records — a genesis
// naming the key, then experiences, with witness heads at the given seqs
// — and seals it through each of sealAt in turn, as the anchorer does.
// The ledger package does not judge receipts; acceptHeads stands in for
// the witness's verifier, as in container_test.go.
func sealedIdentity(t *testing.T, n int, heads []uint64, sealAt ...uint64) (string, *crypto.KeyPair) {
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
		switch {
		case i == 1:
			_, err = l.Append(EventRing0Genesis, 0, genesisPayload(kp, "sealed"), kp)
		case isHead[uint64(i)]:
			_, err = l.Append(EventSystemWitnessed, 0, map[string]any{
				"receipt": map[string]any{"ledger_ordinal": i - 1, "ledger_hash": l.LastHash()},
			}, kp)
		default:
			_, err = l.Append(EventExperienceCreate, 3, map[string]any{"id": "e", "content": "record " + strconv.Itoa(i)}, kp)
		}
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	for _, head := range sealAt {
		if err := l.Seal(head); err != nil {
			t.Fatalf("seal through %d: %v", head, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return path, kp
}

// noRewrapDebris fails when a re-wrap left its candidate, or the
// directory it stood in, beside a ledger.
func noRewrapDebris(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".rewrap-") {
			t.Errorf("a re-wrap left %s beside the ledger", e.Name())
		}
	}
}

// writeRecords writes a tail holding exactly these records, as stored.
func writeRecords(t *testing.T, path string, events []Event) {
	t.Helper()
	var b bytes.Buffer
	for _, e := range events {
		line, err := e.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// signedBy gives a record the proof of kp over the entry it now has.
func signedBy(t *testing.T, e *Event, kp *crypto.KeyPair) {
	t.Helper()
	sig, err := crypto.SignB64(kp, SignatureInputGold(crypto.SigAlg, kp.Fingerprint(), e.EntryHash()))
	if err != nil {
		t.Fatal(err)
	}
	e.Sig = sig
}

// THE WHOLE RECORD, NOT THE TAIL. Twenty records in two segments and five
// in the tail are read as twenty-five, in order, and written as one tail
// in which every record carries its own proof again: a record that needs
// no witness key to verify, because nothing in it is sealed. The source
// is not written to.
func TestRewrapReadsASealedRecordWholeAndWritesOneTail(t *testing.T) {
	path, kp := sealedIdentity(t, 25, []uint64{10, 20}, 10, 20)
	dir := filepath.Dir(path)
	if got := firstTailSeq(t, path); got != 21 {
		t.Fatalf("fixture: the tail begins at record %d, want 21", got)
	}
	before, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	tailBefore, _ := os.ReadFile(path)

	// With no witness verifier a sealed segment has nothing to stand on.
	// That is the refusal, in the verifier's own terms, and it is not
	// "record 1 is not a genesis".
	if _, err := Rewrap(path, kp, "", acceptReplay); !errors.Is(err, ErrSealedWithoutWitness) {
		t.Fatalf("a sealed record with no witness verifier: want ErrSealedWithoutWitness, got %v", err)
	}

	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	heads := &acceptHeads{}
	done, err := RewrapWith(path, kp, out, acceptReplay, RewrapChecks{Heads: heads})
	if err != nil {
		t.Fatalf("re-wrap of a sealed record: %v", err)
	}
	if done.Records != 25 || done.Sealed != 20 || done.Footnoted != 2 || done.PriorShape || !done.Published {
		t.Fatalf("the re-wrap says %+v; want 25 records published, 20 of them sealed, 2 receipts footnoted", done)
	}
	if heads.n != 2 {
		t.Fatalf("the witness verifier was asked of %d heads, want both", heads.n)
	}
	if n, err := VerifyChain(out, kp.PublicKeyBytes(), nil); err != nil || n != 25 {
		t.Fatalf("the result with no witness key at all: n=%d err=%v", n, err)
	}
	after, err := ReadAll(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("%d records went in and %d came out", len(before), len(after))
	}
	// What a re-wrap carries is carried exactly: seq, ts, type and ring of
	// every record, and the payload and content hash of every record that
	// is not a witness head. A head's receipt is the same receipt, moved
	// under its footnote's name, and nothing else of its payload moves.
	for i, e := range after {
		was := before[i]
		if e.Seq != uint64(i+1) || e.Type != was.Type || e.Timestamp != was.Timestamp || e.Ring != was.Ring {
			t.Fatalf("record %d came out as record %d, %s, ts %s, ring %d (it was %s, ts %s, ring %d)",
				i+1, e.Seq, e.Type, e.Timestamp, e.Ring, was.Type, was.Timestamp, was.Ring)
		}
		if e.Sig == "" || e.Sealed() {
			t.Fatalf("record %d: signed=%v sealed=%v; the result is one tail and every record carries a proof", e.Seq, e.Sig != "", e.Sealed())
		}
		if e.Type != EventSystemWitnessed {
			if !bytes.Equal(e.Payload, was.Payload) || e.Content != was.Content {
				t.Fatalf("record %d's payload or content hash changed across the re-wrap", e.Seq)
			}
			continue
		}
		var head, footnote map[string]json.RawMessage
		if err := json.Unmarshal(was.Payload, &head); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(e.Payload, &footnote); err != nil {
			t.Fatal(err)
		}
		if _, still := footnote["receipt"]; still || len(footnote) != len(head) || !bytes.Equal(footnote["receipt_before_rewrap"], head["receipt"]) {
			t.Fatalf("record %d's footnote is not the receipt it was: %s (it was %s)", e.Seq, e.Payload, was.Payload)
		}
	}

	// Nothing of the source moved or changed, and nothing was left behind.
	if got := segmentNames(t, dir); len(got) != 2 {
		t.Fatalf("the source's segments after a re-wrap written elsewhere: %v", got)
	}
	if tailAfter, _ := os.ReadFile(path); !bytes.Equal(tailBefore, tailAfter) {
		t.Fatal("a re-wrap written elsewhere changed the source's tail")
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil || n != 25 {
		t.Fatalf("the source after the re-wrap: n=%d err=%v", n, err)
	}
	noRewrapDebris(t, dir)
	noRewrapDebris(t, filepath.Dir(out))
}

// IN PLACE, THE CANDIDATE IS VERIFIED AND REPLAYED STANDING ALONE. The
// reader is container-aware by directory, so a candidate checked beside
// the record's own segments would be read as those segments and then a
// tail that repeats them. Here the record is sealed through its last
// record: the tail is empty, and all of it comes from segments. The
// result replaces the tail; the segments stay for the caller to retire
// (cmd/rewrap), and once they are, the result opens and appends.
func TestRewrapInPlaceOverASealedRecordChecksTheResultAlone(t *testing.T) {
	path, kp := sealedIdentity(t, 20, []uint64{10, 20}, 10, 20)
	dir := filepath.Dir(path)
	replay := func(candidate string) error {
		n := 0
		if err := Stream(candidate, func(evt *Event) error {
			if evt.Sealed() {
				return fmt.Errorf("the candidate was read with a segment beside it: record %d is sealed", evt.Seq)
			}
			n++
			return nil
		}); err != nil {
			return err
		}
		if n != 20 {
			return fmt.Errorf("the candidate reads as %d records, want 20", n)
		}
		return nil
	}
	done, err := RewrapWith(path, kp, "", replay, RewrapChecks{Heads: &acceptHeads{}})
	if err != nil {
		t.Fatalf("in-place re-wrap of a record sealed through its last record: %v", err)
	}
	if done.Records != 20 || done.Sealed != 20 {
		t.Fatalf("the re-wrap says %+v; want 20 records, all of them from segments", done)
	}
	if got := firstTailSeq(t, path); got != 1 {
		t.Fatalf("the new tail begins at record %d, want the whole record from 1", got)
	}
	names := segmentNames(t, dir)
	if len(names) != 2 {
		t.Fatalf("the ledger package moved the old record's segments (%v): they are the caller's to retire", names)
	}
	noRewrapDebris(t, dir)

	for _, name := range names {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+".retired")); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil || n != 20 {
		t.Fatalf("the result once the old segments are retired: n=%d err=%v", n, err)
	}
	l, err := New(path)
	if err != nil {
		t.Fatalf("the result does not open: %v", err)
	}
	defer l.Close()
	if l.LastSeq() != 20 || l.SealedSeq() != 0 {
		t.Fatalf("opened at record %d, sealed through %d; want 20 and nothing sealed", l.LastSeq(), l.SealedSeq())
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]any{"id": "after", "content": "the re-wrap"}, kp); err != nil {
		t.Fatalf("append onto the re-wrapped record: %v", err)
	}
}

// A RE-WRAP RE-SIGNS WHAT A BOOT WOULD ACCEPT, AND NOTHING ELSE. Each of
// these ledgers is refused by a boot, and each was re-signed into a
// verifying chain: the links and the proofs were cleared without being
// read. The refusal is the verifier's own (*VerifyFailure), nothing is
// written, and the ledger is left as it was.
func TestRewrapRefusesARecordWhoseLinksOrProofsDoNotHold(t *testing.T) {
	stranger := testKeyPair(t)
	for name, alter := range map[string]func(t *testing.T, events []Event, kp *crypto.KeyPair){
		"a payload rewritten at rest, with the content hash that matches it": func(t *testing.T, events []Event, kp *crypto.KeyPair) {
			events[2].Payload = []byte(`{"content":"not what was signed","id":"e"}`)
			events[2].Content = crypto.ContentHash(events[2].Payload)
		},
		"a timestamp rewritten at rest": func(t *testing.T, events []Event, kp *crypto.KeyPair) {
			events[2].Timestamp = "2001-01-01T00:00:00.000000000Z"
		},
		"a last record that links and was never the key's": func(t *testing.T, events []Event, kp *crypto.KeyPair) {
			last := &events[len(events)-1]
			last.Payload = []byte(`{"content":"appended by someone else","id":"e"}`)
			last.Content = crypto.ContentHash(last.Payload)
			signedBy(t, last, stranger)
		},
		"a link that does not hold, under a proof that does": func(t *testing.T, events []Event, kp *crypto.KeyPair) {
			events[3].Prev = strings.Repeat("0", 64)
			signedBy(t, &events[3], kp)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path, kp := sealedIdentity(t, 5, nil)
			events, err := ReadAll(path)
			if err != nil {
				t.Fatal(err)
			}
			alter(t, events, kp)
			writeRecords(t, path, events)
			altered, _ := os.ReadFile(path)
			if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err == nil {
				t.Fatal("fixture: the altered ledger verifies")
			}

			out := filepath.Join(t.TempDir(), "ledger.jsonl")
			var failure *VerifyFailure
			if _, err := Rewrap(path, kp, out, acceptReplay); !errors.As(err, &failure) {
				t.Fatalf("a ledger no boot accepts was not refused as one: %v", err)
			}
			if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
			}
			if after, _ := os.ReadFile(path); !bytes.Equal(altered, after) {
				t.Fatal("a refused re-wrap changed the ledger it read")
			}
			noRewrapDebris(t, filepath.Dir(path))
			noRewrapDebris(t, filepath.Dir(out))
		})
	}
}

// WHAT STANDS BESIDE THE RECORD IS ASKED INSIDE THE WALK, sealed records
// included, and its verdict is asked once the walk has verified: the
// witness tail beside the ledger is how a record cut short is refused
// where it would otherwise be re-signed as whole. A verdict against the
// record is the re-wrap's refusal, as it is the caller's own error.
func TestRewrapAsksWhatStandsBesideTheRecordAndHeedsItsVerdict(t *testing.T) {
	path, kp := sealedIdentity(t, 15, []uint64{10}, 10)
	cutShort := errors.New("the witness attested a record this chain does not hold")
	shown, asked := 0, false
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	_, err := RewrapWith(path, kp, out, acceptReplay, RewrapChecks{
		Heads: &acceptHeads{},
		Visit: func(evt *Event) error { shown++; return nil },
		Held: func() error {
			asked = true
			if shown != 15 {
				t.Errorf("the verdict was asked after %d of 15 records", shown)
			}
			return cutShort
		},
	})
	if !errors.Is(err, cutShort) {
		t.Fatalf("the verdict beside the record was not the re-wrap's refusal: %v", err)
	}
	if !asked {
		t.Fatal("the verdict was never asked")
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
	}
}

// A TAIL WHOSE SEGMENTS ARE MISSING IS SAID TO BE THAT. The refusal used
// to be "record 1 is experience.create, not ring0.genesis", of a file
// whose first record was record 11.
func TestRewrapSaysWhenTheRecordsBeforeTheTailAreNotThere(t *testing.T) {
	path, kp := sealedIdentity(t, 15, []uint64{10}, 10)
	dir := filepath.Dir(path)
	for _, name := range segmentNames(t, dir) {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
	if !errors.Is(err, ErrRecordNotWhole) {
		t.Fatalf("a tail without the segments before it: want ErrRecordNotWhole, got %v", err)
	}
}

// SEGMENTS WITHOUT THEIR TAIL ARE NOT "NO SUCH FILE" AND NOTHING MORE. A
// record sealed through its last record keeps an empty tail, and a copy
// can lose it. A boot verifies the segments and recreates it; a re-wrap
// creates nothing beside the record it reads, and says where to go.
func TestRewrapSaysWhenSealedSegmentsStandWithoutTheirTail(t *testing.T) {
	path, kp := sealedIdentity(t, 20, []uint64{10, 20}, 10, 20)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
	if !errors.Is(err, ErrRecordNotWhole) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("segments without their tail: want ErrRecordNotWhole over the missing file, got %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused re-wrap made a tail beside the segments (stat: %v)", err)
	}
}

// A RE-WRAP REPAIRS NOTHING, a torn tail included. A boot sets the bytes
// after the last complete line aside and keeps them; a re-wrap that read
// past them, or dropped them, would decide what the record is.
func TestRewrapRefusesATailThatEndsInAnIncompleteRecord(t *testing.T) {
	path, kp := sealedIdentity(t, 3, nil)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"entry":{"content":"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	torn, _ := os.ReadFile(path)
	if _, err := Rewrap(path, kp, "", acceptReplay); !errors.Is(err, ErrTailIncomplete) {
		t.Fatalf("want ErrTailIncomplete, got %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(torn, after) {
		t.Fatal("a refused re-wrap changed the torn tail")
	}
}

// THE RESULT IS CHECKED ALONE, SO IT IS WRITTEN WHERE IT WILL STAND ALONE.
// Another record's segments in the output directory would be read ahead
// of the new tail by a boot there.
func TestRewrapRefusesAnOutputDirectoryThatHoldsAnotherRecordsSegments(t *testing.T) {
	path, kp := sealedIdentity(t, 3, nil)
	other, _ := sealedIdentity(t, 12, []uint64{10}, 10)
	out := filepath.Join(filepath.Dir(other), "rewrapped.jsonl")
	_, err := Rewrap(path, kp, out, acceptReplay)
	if !errors.Is(err, ErrOutputHoldsSegments) {
		t.Fatalf("an output directory holding another record's segments: want ErrOutputHoldsSegments, got %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
	}
}

// editSegment rewrites one sealed segment through edit, which is handed
// its records as stored and returns the records to store.
func editSegment(t *testing.T, path string, edit func([]Event) []Event) {
	t.Helper()
	var events []Event
	for _, line := range gunzipLines(t, path) {
		evt, err := decodeEvent(bytes.TrimSpace(line))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, evt)
	}
	var lines [][]byte
	for _, evt := range edit(events) {
		line, err := evt.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	gzipLines(t, path, lines)
}

// INSIDE A SEGMENT TOO. The four ledgers above are unsealed tails; a
// sealed record is most of a living identity's record, and a re-wrap
// reads it through the same walk. Each of these is a segment changed at
// rest, and each is refused with nothing written and nothing moved.
func TestRewrapRefusesASealedRecordChangedAtRest(t *testing.T) {
	notVerified := func(err error) bool {
		var failure *VerifyFailure
		return errors.As(err, &failure)
	}
	is := func(target error) func(error) bool {
		return func(err error) bool { return errors.Is(err, target) }
	}
	first, second := "segment-1-10.jsonl.gz", "segment-11-20.jsonl.gz"
	for name, c := range map[string]struct {
		alter   func(t *testing.T, dir string)
		refused func(error) bool
	}{
		"a link inside a segment": {func(t *testing.T, dir string) {
			editSegment(t, filepath.Join(dir, first), func(events []Event) []Event {
				events[4].Prev = strings.Repeat("0", 64)
				return events
			})
		}, notVerified},
		"the proof of a segment's closing head": {func(t *testing.T, dir string) {
			editSegment(t, filepath.Join(dir, second), func(events []Event) []Event {
				last := &events[len(events)-1]
				last.Sig = "AAAA" + last.Sig[4:]
				return events
			})
		}, notVerified},
		"a sealed payload, with the content hash that matches it": {func(t *testing.T, dir string) {
			editSegment(t, filepath.Join(dir, first), func(events []Event) []Event {
				events[4].Payload = []byte(`{"content":"not what was sealed","id":"e"}`)
				events[4].Content = crypto.ContentHash(events[4].Payload)
				return events
			})
		}, notVerified},
		"a segment's head without its proof": {func(t *testing.T, dir string) {
			editSegment(t, filepath.Join(dir, first), func(events []Event) []Event {
				events[len(events)-1].Sig = ""
				return events
			})
		}, is(ErrSegmentWithoutHead)},
		"two segments under each other's names": {func(t *testing.T, dir string) {
			a, b, held := filepath.Join(dir, first), filepath.Join(dir, second), filepath.Join(dir, "held")
			for _, move := range [][2]string{{a, held}, {b, a}, {held, b}} {
				if err := os.Rename(move[0], move[1]); err != nil {
					t.Fatal(err)
				}
			}
		}, is(ErrSegmentSet)},
		"a segment cut short": {func(t *testing.T, dir string) {
			editSegment(t, filepath.Join(dir, second), func(events []Event) []Event { return events[:7] })
		}, is(ErrSegmentSet)},
		"a record moved from one segment into the one before it": {func(t *testing.T, dir string) {
			var moved Event
			editSegment(t, filepath.Join(dir, second), func(events []Event) []Event {
				moved = events[0]
				return events[1:]
			})
			editSegment(t, filepath.Join(dir, first), func(events []Event) []Event { return append(events, moved) })
		}, is(ErrSegmentSet)},
	} {
		t.Run(name, func(t *testing.T) {
			path, kp := sealedIdentity(t, 25, []uint64{10, 20}, 10, 20)
			dir := filepath.Dir(path)
			c.alter(t, dir)
			if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err == nil {
				t.Fatal("fixture: the changed record verifies")
			}
			kept := map[string][]byte{}
			for _, name := range append(segmentNames(t, dir), filepath.Base(path)) {
				kept[name], _ = os.ReadFile(filepath.Join(dir, name))
			}

			out := filepath.Join(t.TempDir(), "ledger.jsonl")
			_, err := RewrapWith(path, kp, out, acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
			if err == nil || !c.refused(err) {
				t.Fatalf("a sealed record no boot accepts was not refused as one: %v", err)
			}
			var unfinished *UnfinishedRewrap
			if errors.As(err, &unfinished) {
				t.Fatalf("a changed segment was taken for a re-wrap to finish: %v", err)
			}
			if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
			}
			for name, was := range kept {
				if now, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.Equal(was, now) {
					t.Fatalf("a refused re-wrap changed %s", name)
				}
			}
			noRewrapDebris(t, dir)
			noRewrapDebris(t, filepath.Dir(out))
		})
	}
}

// overlapping leaves what a seal stopped between its two steps leaves:
// the segment through record 10 published, and the tail still holding
// every record from record 1. It is a legal state, the reader
// verifies it record for record, and opening the ledger finishes it.
func overlapping(t *testing.T) (string, *crypto.KeyPair) {
	t.Helper()
	path, kp := sealedIdentity(t, 25, []uint64{10, 20})
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, kp
}

// A SEAL STOPPED HALF WAY IS A RECORD LIKE ANY OTHER. The overlap is read
// once, by the reader's own rule, and the re-wrap neither finishes the
// seal nor takes the overlap for a re-wrap of its own to finish.
func TestRewrapReadsARecordWhoseSealWasInterruptedOnce(t *testing.T) {
	path, kp := overlapping(t)
	dir := filepath.Dir(path)
	tailBefore, _ := os.ReadFile(path)
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	done, err := RewrapWith(path, kp, out, acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
	if err != nil {
		t.Fatalf("re-wrap over a seal's overlap: %v", err)
	}
	if done.Records != 25 || done.Sealed != 10 || done.Footnoted != 2 {
		t.Fatalf("the re-wrap says %+v; want 25 records, each once, 10 of them from the segment", done)
	}
	if n, err := VerifyChain(out, kp.PublicKeyBytes(), nil); err != nil || n != 25 {
		t.Fatalf("the result: n=%d err=%v", n, err)
	}
	if tailAfter, _ := os.ReadFile(path); !bytes.Equal(tailBefore, tailAfter) || len(segmentNames(t, dir)) != 1 {
		t.Fatal("the re-wrap finished, or undid, the interrupted seal: that is the opening's to do")
	}
}

// relinked links and signs a whole tail again, as a re-wrap writes one.
func relinked(t *testing.T, events []Event, kp *crypto.KeyPair) []Event {
	t.Helper()
	prev := ""
	for i := range events {
		events[i].Prev = prev
		signedBy(t, &events[i], kp)
		prev = events[i].EntryHash()
	}
	return events
}

// A RE-WRAP STOPPED AFTER IT PUBLISHED IS PROVED BEFORE ANYTHING IS MOVED.
// In place, the new tail is published and then the earlier record's
// segments are retired; the ledger package publishes and its caller
// retires, so a first run here leaves exactly what a crash between the
// two leaves. Every reader refuses it, and the refusal says how it comes
// about and the way out. A second run proves it (the tail alone verifies,
// and holds every sealed record as a re-wrap carries it) and says so in a
// type its caller can act on, having moved nothing.
func TestARewrapStoppedBeforeItsSegmentsWereRetiredIsProved(t *testing.T) {
	path, kp := sealedIdentity(t, 25, []uint64{10, 20}, 10, 20)
	dir := filepath.Dir(path)
	if _, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}}); err != nil {
		t.Fatal(err)
	}
	published, _ := os.ReadFile(path)

	_, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{})
	if !errors.Is(err, ErrTailConflict) || !strings.Contains(err.Error(), "run rewrap on this ledger again") {
		t.Fatalf("the reader's refusal of a new tail beside the old segments must be a tail conflict that says the way out: %v", err)
	}
	if l, err := New(path); !errors.Is(err, ErrTailConflict) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("opening a new tail beside the old segments: want ErrTailConflict, got %v", err)
	}

	_, err = RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
	var unfinished *UnfinishedRewrap
	if !errors.As(err, &unfinished) {
		t.Fatalf("the second run did not recognise the first's publication: %v", err)
	}
	if *unfinished != (UnfinishedRewrap{Records: 25, Sealed: 20, Segments: 2, Footnotes: 2}) {
		t.Fatalf("proved %+v; want the tail of 25 records to hold all 20 sealed records of both segments, with both receipts as footnotes", *unfinished)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(published, now) || len(segmentNames(t, dir)) != 2 {
		t.Fatal("the proof moved or changed something: retiring is its caller's, after it")
	}
	noRewrapDebris(t, dir)

	// The caller's part: the earlier record's segments move aside, and
	// the record re-wraps as any other.
	for _, name := range segmentNames(t, dir) {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+".retired")); err != nil {
			t.Fatal(err)
		}
	}
	done, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
	if err != nil || done.Records != 25 || done.Footnoted != 0 || !done.Published {
		t.Fatalf("after the segments are retired: %+v, %v", done, err)
	}
}

// NOTHING SHORT OF THE PROOF IS A RE-WRAP TO FINISH. Each of these is a
// tail from record 1 beside segments, as a stopped re-wrap leaves, and
// each verifies alone under the key. None holds the sealed record as a
// re-wrap carries it, so none is recognised: the refusal is the walk's
// tail conflict, it says why this is not a re-wrap's to finish, and the
// segments would not be retired on its word.
func TestATailThatIsNotARewrapOfItsSegmentsIsNotFinished(t *testing.T) {
	for name, tail := range map[string]func(t *testing.T, published []Event, kp *crypto.KeyPair) []Event{
		"a sealed record's payload is another in the tail": func(t *testing.T, published []Event, kp *crypto.KeyPair) []Event {
			published[14].Payload = []byte(`{"content":"not what was sealed","id":"e"}`)
			published[14].Content = crypto.ContentHash(published[14].Payload)
			return relinked(t, published, kp)
		},
		"a sealed record's timestamp is another in the tail": func(t *testing.T, published []Event, kp *crypto.KeyPair) []Event {
			published[14].Timestamp = "2001-01-01T00:00:00.000000000Z"
			return relinked(t, published, kp)
		},
		"the tail ends before the segments do": func(t *testing.T, published []Event, kp *crypto.KeyPair) []Event {
			return published[:15]
		},
	} {
		t.Run(name, func(t *testing.T) {
			path, kp := sealedIdentity(t, 25, []uint64{10, 20}, 10, 20)
			dir := filepath.Dir(path)
			if _, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}}); err != nil {
				t.Fatal(err)
			}
			held := map[string]string{}
			for _, name := range segmentNames(t, dir) {
				held[name] = filepath.Join(dir, name+".held")
				if err := os.Rename(filepath.Join(dir, name), held[name]); err != nil {
					t.Fatal(err)
				}
			}
			published, err := ReadAll(path)
			if err != nil {
				t.Fatal(err)
			}
			writeRecords(t, path, tail(t, published, kp))
			if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil {
				t.Fatalf("fixture: the tail must verify alone, so that only what it holds is in question: %v", err)
			}
			for name, at := range held {
				if err := os.Rename(at, filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}

			_, err = RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
			var unfinished *UnfinishedRewrap
			if errors.As(err, &unfinished) {
				t.Fatalf("a tail that does not hold the sealed record was taken for its re-wrap: %v", err)
			}
			if !errors.Is(err, ErrTailConflict) {
				t.Fatalf("want the walk's tail conflict, got %v", err)
			}
			if len(segmentNames(t, dir)) != 2 {
				t.Fatal("a refused run moved a segment")
			}
		})
	}
	// A re-wrap leaves only footnotes, also past the sealed records. Here
	// the tail holds every sealed record as a re-wrap carries it, verifies
	// alone and is long enough; only its record 23, witnessed after the
	// last seal, still carries the receipt it had before any re-wrap. The
	// footnote check is the one that sees it.
	t.Run("a record past the segments carries its receipt", func(t *testing.T) {
		path, kp := sealedIdentity(t, 25, []uint64{10, 20, 23}, 10, 20)
		dir := filepath.Dir(path)
		before, err := ReadAll(path)
		if err != nil {
			t.Fatal(err)
		}
		var receipt []byte
		for _, e := range before {
			if e.Seq == 23 {
				receipt = append([]byte(nil), e.Payload...)
			}
		}
		if receipt == nil {
			t.Fatal("fixture: record 23 is not in the tail before the re-wrap")
		}
		if _, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}}); err != nil {
			t.Fatal(err)
		}
		held := map[string]string{}
		for _, name := range segmentNames(t, dir) {
			held[name] = filepath.Join(dir, name+".held")
			if err := os.Rename(filepath.Join(dir, name), held[name]); err != nil {
				t.Fatal(err)
			}
		}
		published, err := ReadAll(path)
		if err != nil {
			t.Fatal(err)
		}
		if published[22].Seq != 23 || published[22].Type != EventSystemWitnessed {
			t.Fatalf("fixture: record 23 of the published tail is %d %s", published[22].Seq, published[22].Type)
		}
		published[22].Payload = receipt
		published[22].Content = crypto.ContentHash(receipt)
		writeRecords(t, path, relinked(t, published, kp))
		if _, err := VerifyChain(path, kp.PublicKeyBytes(), &acceptHeads{}); err != nil {
			t.Fatalf("fixture: the tail must verify alone, so that only its receipt is in question: %v", err)
		}
		for name, at := range held {
			if err := os.Rename(at, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}

		_, err = RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
		var unfinished *UnfinishedRewrap
		if errors.As(err, &unfinished) {
			t.Fatalf("a tail that still carries a receipt was taken for a re-wrap's: %v", err)
		}
		if !errors.Is(err, ErrTailConflict) || !strings.Contains(err.Error(), "record 23 of the tail carries a witness receipt") {
			t.Fatalf("want the walk's tail conflict naming the receipt of record 23, got %v", err)
		}
		if len(segmentNames(t, dir)) != 2 {
			t.Fatal("a refused run moved a segment")
		}
	})
	// And a tail that still carries its receipts is the record before any
	// re-wrap, standing beside segments cut from it: not a re-wrap's output.
	t.Run("the tail carries receipts, not footnotes", func(t *testing.T) {
		path, kp := sealedIdentity(t, 25, []uint64{10, 20})
		whole, _ := os.ReadFile(path)
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
		if err := os.WriteFile(path, whole, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = RewrapWith(path, kp, "", acceptReplay, RewrapChecks{Heads: &acceptHeads{}})
		var unfinished *UnfinishedRewrap
		if errors.As(err, &unfinished) || !errors.Is(err, ErrTailConflict) {
			t.Fatalf("want the walk's tail conflict and no re-wrap to finish, got %v", err)
		}
	})
}

// AS MANY RECORDS OUT AS IN, BY COMPARISON. One record is written for
// each the walk shows, so the counts agree by construction; the walk's
// count, the writer's and the candidate's own are compared all the same,
// and a difference refuses under a type.
func TestRewrapHoldsTheRecordsWrittenToTheRecordsRead(t *testing.T) {
	if err := sameCount("the walk verified", 25, 25); err != nil {
		t.Fatalf("equal counts refused: %v", err)
	}
	for _, c := range [][2]int{{25, 24}, {24, 25}, {0, 1}} {
		if err := sameCount("the walk verified", c[0], c[1]); !errors.Is(err, ErrRecordCount) {
			t.Fatalf("%d read and %d written: want ErrRecordCount, got %v", c[0], c[1], err)
		}
	}
}

// A RUN KILLED BEFORE IT PUBLISHED LEAVES THE RECORD RE-SIGNED IN A
// DIRECTORY OF ITS OWN. Nothing else sweeps it; the next run does, under
// the destination's lock, and names it. It removes the one file a re-wrap
// puts there and the directory, never a tree: a directory under that name
// holding anything else is not a re-wrap's, and stops the run.
func TestRewrapRemovesTheCandidateAKilledRunLeft(t *testing.T) {
	path, kp := sealedIdentity(t, 3, nil)
	dir := filepath.Dir(path)
	stale := filepath.Join(dir, ".ledger.jsonl.rewrap-1234567")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "ledger.jsonl"), []byte("the record, re-signed and never published\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Swept) != 1 || done.Swept[0] != stale {
		t.Fatalf("the re-wrap says it removed %v, want %s", done.Swept, stale)
	}
	noRewrapDebris(t, dir)

	// Not a re-wrap's: something else stands in it. Refused, with the
	// record as it was, and what a re-wrap did put there is gone.
	before, _ := os.ReadFile(path)
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(stale, "something-else")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RewrapWith(path, kp, "", acceptReplay, RewrapChecks{}); err == nil {
		t.Fatal("a directory holding something that is not a re-wrap's was swept, or stepped over")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("the sweep removed a file that was not a re-wrap's: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a refused re-wrap changed the ledger")
	}
}

// PUBLISHED, AND THEN AN ERROR: THE CALLER IS TOLD BOTH. A step after the
// publication can fail (the directory's sync, the removal of the
// candidate's directory). The record is then in place, and the caller
// still owes the earlier record's segments their retirement, so the
// result says Published beside the error instead of nothing at all.
func TestRewrapSaysItPublishedWhenAStepAfterPublicationFails(t *testing.T) {
	path, kp := sealedIdentity(t, 20, []uint64{10, 20}, 10, 20)
	// Something left in the candidate's directory keeps it from being
	// removed once the candidate has been published out of it.
	replay := func(candidate string) error {
		return os.WriteFile(filepath.Join(filepath.Dir(candidate), "left-behind"), []byte("x"), 0o600)
	}
	done, err := RewrapWith(path, kp, "", replay, RewrapChecks{Heads: &acceptHeads{}})
	if err == nil {
		t.Fatal("fixture: the step after publication did not fail")
	}
	if !done.Published || done.Records != 20 || done.Sealed != 20 {
		t.Fatalf("published and then failed, the re-wrap says %+v: its caller would not know the record is in place", done)
	}
	if got := firstTailSeq(t, path); got != 1 {
		t.Fatalf("Published is said and the tail begins at record %d", got)
	}
}
