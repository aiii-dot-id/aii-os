package identity

import "testing"

// A TIMER'S SHORT ID IS EIGHT RANDOM DIGITS. Two timers set one after the
// other must not share an id, and the id is cut from the front of a UUID,
// which is random there only in version 4. Sixty-four in a row are all
// different (a chance collision among them is about one in two million),
// and each is eight lower-case hex digits.
func TestATimersShortIDIsEightRandomDigits(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		id := shortID()
		if len(id) != 8 {
			t.Fatalf("a short id is eight digits, got %q", id)
		}
		for _, c := range id {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("a short id is lower-case hex, got %q", id)
			}
		}
		if seen[id] {
			t.Fatalf("two timers set one after the other were handed the same id %q", id)
		}
		seen[id] = true
	}
}
