package witness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// anchor_pass_test.go — the anchor pass works from the witness status its
// caller's health probe already read (T66): one status request per pass;
// and the caller's lifetime gates the start of the bookmark, never its
// flight.

func (fw *fakeWitness) statusAsks() int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.statuses
}

// A status in hand is the one the cadence gate reads, and the pass does
// not ask the witness for another.
func TestAnAnchorPassReadsTheStatusItIsHandedAndAsksNoOther(t *testing.T) {
	t.Run("a floor above the record holds the pass", func(t *testing.T) {
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 3)
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
		if err := a.AnchorPass(context.Background(), &WitnessStatus{MinPeriodicCadence: 100}); err != nil {
			t.Fatal(err)
		}
		if a.LastAnchoredSeq() != 0 {
			t.Fatal("the status in hand set a floor of 100 events and the record holds 3: the pass should not have bookmarked")
		}
		if got := fw.statusAsks(); got != 0 {
			t.Fatalf("the pass asked the witness for its status %d times with one in hand", got)
		}
	})
	t.Run("no floor lets it anchor", func(t *testing.T) {
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 3)
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
		if err := a.AnchorPass(context.Background(), &WitnessStatus{}); err != nil {
			t.Fatal(err)
		}
		if a.LastAnchoredSeq() != 3 {
			t.Fatalf("the pass should have anchored record 3, the anchoring point is %d", a.LastAnchoredSeq())
		}
		if got := fw.statusAsks(); got != 0 {
			t.Fatalf("the pass asked the witness for its status %d times with one in hand", got)
		}
	})
	t.Run("without one it asks once", func(t *testing.T) {
		fw := newFakeWitness(t)
		lg, kp := testLedger(t, 3)
		a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatal(err)
		}
		if got := fw.statusAsks(); got != 1 {
			t.Fatalf("a pass with no status in hand asked %d times, want 1", got)
		}
	})
}

// A pass whose lifetime has ended before its bookmark asks the witness
// nothing and pauses nothing.
func TestAnEndedLifetimeStartsNoBookmark(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	log := &passLog{dir: filepath.Dir(lg.Path())}
	fw.slowly(func() { log.add("bookmark") })
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, loggedMinter{testMinter{lg, kp}, log}, 1, "")
	a.SetAppendPause(log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.AnchorPass(ctx, &WitnessStatus{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a pass whose lifetime ended should say so, got %v", err)
	}
	if got := log.got(); len(got) != 0 {
		t.Fatalf("THE PASS WENT ON PAST ITS ENDED LIFETIME: %v", got)
	}
	if a.LastAnchoredSeq() != 0 || lg.LastSeq() != 3 {
		t.Fatalf("nothing should have moved: anchored at %d, the record at %d", a.LastAnchoredSeq(), lg.LastSeq())
	}
}

// THE HAZARD THE GATE MUST NOT CREATE: a lifetime that ends while the
// witness answers does not cut the bookmark short. The witness holds the
// receipt from its answer; a pass that dropped it would leave the next
// receipt chaining from one the record never carries — a FORK latched,
// and SAFE (plan of record T66).
func TestALifetimeEndingDuringTheBookmarkDoesNotCutIt(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	log := &passLog{dir: filepath.Dir(lg.Path())}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fw.slowly(func() {
		log.add("bookmark")
		cancel() // Shutdown begins while the witness answers
	})
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, loggedMinter{testMinter{lg, kp}, log}, 1, "")
	a.SetAppendPause(log)
	if err := a.AnchorPass(ctx, &WitnessStatus{}); err != nil {
		t.Fatalf("the pass whose bookmark was out should run to its end: %v", err)
	}
	if want := []string{"pause", "bookmark", "mint", "release"}; !reflect.DeepEqual(log.got(), want) {
		t.Fatalf("the pass did %v, want %v", log.got(), want)
	}
	if a.LastAnchoredSeq() != 3 || lg.LastSeq() != 4 {
		t.Fatalf("the head should be in the record: anchored at %d, the record at %d", a.LastAnchoredSeq(), lg.LastSeq())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(lg.Path()), PendingReceiptFileName)); !os.IsNotExist(err) {
		t.Fatalf("the receipt the record carries should not still be kept beside it: %v", err)
	}
}
