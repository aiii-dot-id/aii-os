package ledger

import (
	"errors"
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

// .
// .
func TestAnEventLargerThanTheOldReaderCapRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("a", 2<<20)
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
	// .
	// .
	if _, err := VerifyChain(path, kp.PublicKey, nil); err != nil {
		t.Fatalf("chain verification failed over a large event: %v", err)
	}
}

// .
// .
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

	// .
	// .
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
