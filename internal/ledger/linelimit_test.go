package ledger

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The writer had no line limit and the reader stopped at 1 MiB. An event
// over that appended, signed and fsynced perfectly, and then every
// subsequent boot died in ReadAll on bufio.ErrTooLong — chain
// verification and replay both go through it. A legitimate long note or
// DREAM output could make an identity permanently unbootable, and the
// damage is undoable by design: events are not removable.

// The regression itself: an event bigger than the OLD reader cap must
// append AND read back.
func TestAnEventLargerThanTheOldReaderCapRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("a", 2<<20) // 2 MiB — twice the old 1 MiB cap
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"content": big}, kp); err != nil {
		t.Fatalf("a 2 MiB event was refused: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	events, err := ReadAll(path)
	if err != nil {
		t.Fatalf("the ledger became unreadable — this is the bricked identity: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("read %d events, want 1", len(events))
	}
	if !strings.Contains(string(events[0].Payload), big) {
		t.Fatal("the payload did not survive the round trip intact")
	}
	// And chain verification — the OTHER ReadAll caller, and the one
	// that runs at every boot — still gets through it.
	if err := VerifyChain(path, map[string][]byte{kp.Fingerprint(): kp.PublicKey}); err != nil {
		t.Fatalf("chain verification failed over a large event: %v", err)
	}
}

// Past the shared limit, the door refuses — before anything is written,
// so the chain is untouched and the ledger keeps working.
func TestAnUnreadableEventIsRefusedAtTheDoor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"content": "the first thought"}, kp); err != nil {
		t.Fatal(err)
	}

	huge := strings.Repeat("b", MaxEventLineBytes+1)
	_, err = l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"content": huge}, kp)
	if err == nil {
		t.Fatal("an event past the readable limit was appended — the next boot would die in ReadAll")
	}
	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("the refusal is not ErrEventTooLarge, so callers cannot tell it from corruption: %v", err)
	}

	// Refusal is not a freeze: the ledger is fine, the caller asked for
	// too much, and the next ordinary append must still work.
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"content": "the next thought"}, kp); err != nil {
		t.Fatalf("an oversized refusal poisoned the ledger: %v", err)
	}
	if l.LastSeq() != 2 {
		t.Fatalf("the refused event consumed a sequence number: last seq %d", l.LastSeq())
	}
	events, err := ReadAll(path)
	if err != nil || len(events) != 2 {
		t.Fatalf("the ledger holds %d readable events after a refusal: %v", len(events), err)
	}
}
