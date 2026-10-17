package cursor

import (
	"encoding/json"
	"testing"
)

func TestACursorsJSONIsItsStoredFormat(t *testing.T) {
	const stored = `{"turn":"turn_a","position":5}`
	c := TurnCursor{Turn: "turn_a", Position: 5}
	v, err := json.Marshal(c)
	if err != nil || string(v) != stored {
		t.Fatalf("a cursor encodes as %s (%v), want %s", v, err, stored)
	}
	var back TurnCursor
	if err := json.Unmarshal([]byte(stored), &back); err != nil || back != c {
		t.Fatalf("the stored cursor decodes as %+v (%v), want %+v", back, err, c)
	}
	if (ConversationBatch{Start: stored, Through: c}).Moved() {
		t.Fatal("a batch that ends where its stored cursor stands says it moves the cursor")
	}
	if !(ConversationBatch{Start: stored, Through: TurnCursor{Turn: "turn_a", Position: 6}}).Moved() {
		t.Fatal("a batch one code point past its stored cursor says it moves nothing")
	}
	if (ConversationBatch{Start: stored}).Moved() {
		t.Fatal("a batch with no cursor of its own says it moves the cursor")
	}
}

// Keep shortens a batch from the end, and its cursor ends at the last part
// kept — never past a part that was dropped. Positions are code points.
func TestKeepNeverPassesADroppedPart(t *testing.T) {
	b := ConversationBatch{
		Start:   `{"turn":"t0","position":2}`,
		Through: TurnCursor{Turn: "room", Position: 9}, // past room words after the last part
		Parts: []TurnPart{
			{ID: "t0", Role: "operator", Text: "llo", From: 2, Whole: true},
			{ID: "t1", Role: "resident", Text: "héllo wörld", Whole: true}, // 11 code points, 13 bytes
			{ID: "t2", Role: "operator", Text: "ok", Whole: true},
		},
	}
	all := b.Keep(3, 0)
	if all.Through != b.Through || len(all.Parts) != 3 {
		t.Fatalf("keeping everything gave %+v, want the batch as it was", all)
	}
	if whole := b.Keep(2, 11); whole.Through != (TurnCursor{Turn: "t1", Position: 11}) || !whole.Parts[1].Whole || whole.Parts[1].Text != "héllo wörld" {
		t.Fatalf("keeping the second part to its whole length gave %+v, want it whole, the cursor at its end", whole)
	}
	cut := b.Keep(2, 3)
	if len(cut.Parts) != 2 || cut.Parts[1].Text != "hél" || cut.Parts[1].Whole || cut.Through != (TurnCursor{Turn: "t1", Position: 3}) {
		t.Fatalf("keeping three code points of the second part gave %+v, want \"hél\", not whole, the cursor at t1:3", cut)
	}
	if first := b.Keep(1, 1); first.Parts[0].Text != "l" || first.Through != (TurnCursor{Turn: "t0", Position: 3}) {
		t.Fatalf("a cut of a part that began inside its turn gave %+v, want the cursor at t0:3", first)
	}
	if b.Parts[1].Text != "héllo wörld" || !b.Parts[1].Whole {
		t.Fatalf("Keep changed the batch it was asked of: %+v", b.Parts[1])
	}
	none := b.Keep(0, 0)
	if len(none.Parts) != 0 || none.Start != b.Start || none.Moved() {
		t.Fatalf("keeping nothing gave %+v, want a batch that moves no cursor", none)
	}
}

// THE length of a turn is its code points as Go counts them: a NUL is one,
// a byte of a torn rune is one, a two-byte letter is one.
func TestTheLengthOfATurnIsItsCodePoints(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int
	}{
		{"", 0}, {"ab\x00c", 4}, {"é", 1}, {"\xc3", 1}, {"héllo wörld", 11},
	} {
		if got := TurnLength(c.s); got != c.want {
			t.Errorf("TurnLength(%q) = %d, want %d", c.s, got, c.want)
		}
	}
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{0, 11, "héllo wörld"}, {1, 4, "éll"}, {-3, 2, "hé"}, {9, 40, "ld"}, {5, 5, ""}, {7, 2, ""},
	} {
		if got := RuneSlice("héllo wörld", c.from, c.to); got != c.want {
			t.Errorf("RuneSlice(%d, %d) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}
