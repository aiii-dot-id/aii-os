package ledger

import (
	"reflect"
	"testing"
)

// TestTheReaderMarksTheRecordASegmentClosesUnder: Closing is the segment's
// last record by its name, and nothing else — not an interior head, not a
// tail record.
func TestTheReaderMarksTheRecordASegmentClosesUnder(t *testing.T) {
	path, _ := twoSegmentsAndATail(t)
	n := 0
	if err := Stream(path, func(evt *Event) error {
		n++
		if want := evt.Seq == 10 || evt.Seq == 20; evt.Closing() != want {
			t.Errorf("record %d: closing %v, want %v", evt.Seq, evt.Closing(), want)
		}
		if want := evt.Seq <= 20; evt.Sealed() != want {
			t.Errorf("record %d: sealed %v, want %v", evt.Seq, evt.Sealed(), want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 30 {
		t.Fatalf("streamed %d records", n)
	}
}

// askingHeads asks, at every head, for every record from 0 to one past the
// head, keeps what the walk answered, and moves the receipt chain to the
// record a script says the head's receipt attests.
type askingHeads struct {
	attests  map[uint64]uint64   // head → the record its receipt attests
	answered map[uint64][]uint64 // head → the records the walk answered for
	hashes   map[uint64]string   // record → the hash the walk gave
}

func (a *askingHeads) VerifyHead(evt *Event, hashAt func(uint64) (string, bool)) (uint64, error) {
	for seq := uint64(0); seq <= evt.Seq+1; seq++ {
		if hash, ok := hashAt(seq); ok {
			a.answered[evt.Seq] = append(a.answered[evt.Seq], seq)
			a.hashes[seq] = hash
		}
	}
	return a.attests[evt.Seq], nil
}

// TestTheWalkHoldsTheRecordsALateHeadMayAttest: at each head the walk
// answers for the records after the one the receipt chain last attested
// and before the head — across segments and the tail alike — with their
// entry hashes, and for no other: what each head's receipt attests is
// forgotten, so memory is the records between two receipts.
func TestTheWalkHoldsTheRecordsALateHeadMayAttest(t *testing.T) {
	path, kp := sealedFixture(t, 14, 4, 9, 13)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, head := range []uint64{4, 9} {
		if err := l.Seal(head); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	heads := &askingHeads{
		// 4 late (attests 2), 9 late (attests 6), 13 on time (attests 12).
		attests:  map[uint64]uint64{4: 2, 9: 6, 13: 12},
		answered: map[uint64][]uint64{},
		hashes:   map[uint64]string{},
	}
	if n, err := VerifyChain(path, kp.PublicKeyBytes(), heads); err != nil || n != 14 {
		t.Fatalf("verify: n=%d err=%v", n, err)
	}
	want := map[uint64][]uint64{
		4:  {1, 2, 3},          // nothing attested yet: every record before the head
		9:  {3, 4, 5, 6, 7, 8}, // after 2, through the late head at 4
		13: {7, 8, 9, 10, 11, 12},
	}
	if !reflect.DeepEqual(heads.answered, want) {
		t.Fatalf("the walk answered %v, want %v", heads.answered, want)
	}
	hashes := entryHashes(t, path)
	for seq, hash := range heads.hashes {
		if hash != hashes[seq-1] {
			t.Fatalf("record %d: the walk gave %s, its entry hash is %s", seq, hash, hashes[seq-1])
		}
	}
}
