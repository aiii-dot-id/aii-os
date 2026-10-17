package app

import (
	"sync/atomic"
	"testing"
)

// THE ONE ACCOUNT OF A SPOKEN UTTERANCE, held to itself: the ledger finds
// the words from their delivery until they settle and not after, and the
// session's work they hold goes back exactly once however many roads ask.
func TestSpokenWordsAreFoundUntilTheySettleAndGiveTheirWorkBackOnce(t *testing.T) {
	a := newVoiceApp(t)
	var given atomic.Int32
	w := a.deliverSpoken(heardUtterance{SessionID: "vs-ledger", Sequence: 3, Text: "words"}, func() { given.Add(1) })
	key := voiceRefKey("vs-ledger", 3)
	if !a.wordsWaiting(key) || !a.wordsQueued(key) {
		t.Fatal("delivered words are found by their reference, and count as queued for a turn")
	}
	// The session's claim ends first: the work goes back, the words stay.
	w.giveBack()
	w.giveBack()
	if given.Load() != 1 || !a.wordsWaiting(key) {
		t.Fatalf("the session's work goes back once and the words keep their place: given=%d waiting=%v", given.Load(), a.wordsWaiting(key))
	}
	a.settleSpoken(w)
	if a.wordsWaiting(key) || a.wordsQueued(key) || given.Load() != 1 {
		t.Fatalf("settled words are found nowhere and gave their work back once: waiting=%v given=%d", a.wordsWaiting(key), given.Load())
	}

	// Words that settle without the session's claim ending first give the
	// work back at the settling.
	var late atomic.Int32
	v := a.deliverSpoken(heardUtterance{SessionID: "vs-ledger", Sequence: 4}, func() { late.Add(1) })
	a.settleSpoken(v)
	if late.Load() != 1 {
		t.Fatalf("settling gives the work back: %d", late.Load())
	}

	// A successor with the same reference is its own account: settling the
	// earlier one does not take the later one out of the ledger.
	first := a.deliverSpoken(heardUtterance{SessionID: "vs-ledger", Sequence: 5}, nil)
	second := a.deliverSpoken(heardUtterance{SessionID: "vs-ledger", Sequence: 5}, nil)
	a.settleSpoken(first)
	if !a.wordsWaiting(voiceRefKey("vs-ledger", 5)) {
		t.Fatal("settling one account removed another's")
	}
	a.settleSpoken(second)
	if a.wordsWaiting(voiceRefKey("vs-ledger", 5)) {
		t.Fatal("the later account was not closed by its own settling")
	}

	// Words no session delivered hold nothing, and a final that names no
	// sequence is found by no reference: every road that asks is safe.
	var none *spokenWords
	none.giveBack()
	a.settleSpoken(none)
	nameless := a.deliverSpoken(heardUtterance{SessionID: "vs-ledger"}, nil)
	if nameless.ref != "" || a.wordsWaiting(voiceRefKey("vs-ledger", 0)) {
		t.Fatal("a final that names no sequence is not found by a reference")
	}
	a.settleSpoken(nameless)
}
