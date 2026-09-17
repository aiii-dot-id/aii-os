package speech

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// .
// .
// .
func TestSegmentsSpeakSoonThenInLongerBreaths(t *testing.T) {
	text := "The kettle is on. It will be a minute or two. I put the good tea in it, the one from the tin at the back, " +
		"because you said the other was too grassy. There is a biscuit as well, if you want it."
	got := Segments(text, 60, 120)
	if len(got) < 3 {
		t.Fatalf("the reply was not broken up at all: %q", got)
	}
	if !strings.HasPrefix(got[0], "The kettle is on.") || utf8.RuneCountInString(got[0]) > 60 {
		t.Errorf("the first piece is not the opening the listener waits for: %q", got[0])
	}
	for i, seg := range got {
		limit := 120
		if i == 0 {
			limit = 60
		}
		if n := utf8.RuneCountInString(seg); n > limit {
			t.Errorf("piece %d is %d runes, past its limit of %d: %q", i, n, limit, seg)
		}
	}
	// .
	// .
	if joined := strings.Join(got, " "); joined != text {
		t.Errorf("the reply changed on the way:\n got %q\nwant %q", joined, text)
	}
	// .
	long := strings.Repeat("a", 300)
	if parts := Segments(long, 40, 120); len(parts) < 3 || strings.Join(parts, "") != long {
		t.Errorf("a long sentence was not carried whole: %d parts", len(parts))
	}
	// .
	// .
	abbr := "Dr. Smith said the kettle would take a minute or two, and that the good tea was in the tin at the back of the cupboard."
	if parts := Segments(abbr, 40, 120); parts[0] == "Dr." || utf8.RuneCountInString(parts[0]) < minOpening {
		t.Errorf("the opening is an abbreviation: %q", parts)
	}
	// .
	// .
	if parts := Segments("Dr. "+strings.Repeat("b", 118), 40, 120); len(parts) != 2 || parts[0] != "Dr." {
		t.Errorf("the merge overran the limit: %q", parts)
	}
	// .
	if parts := Segments("Yes.", 40, 120); len(parts) != 1 || parts[0] != "Yes." {
		t.Errorf("a short reply was broken up: %q", parts)
	}
	if parts := Segments("", 40, 120); len(parts) != 0 {
		t.Errorf("nothing became something: %q", parts)
	}
}
