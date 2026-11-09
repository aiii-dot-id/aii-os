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

// A REBUILT PART IS WHOLE, OR IT GIVES WAY TO ITS RECORD. Kept whole, the cursor stands at the
// recorded end. Behind another part it is not cut, so not taken. First in the pass and cut, it
// gives way to the turn as recorded, cut there, which moves the cursor inside the record.
func TestARebuiltPartIsWholeOrGivesWayToItsRecord(t *testing.T) {
	recorded := &TurnPart{ID: "t1", Role: "participant", Text: "[voice] a frame around the words", Whole: true, TurnID: "x"}
	rebuilt := TurnPart{ID: "t1", Role: "operator", Text: "the words", Whole: true, TurnID: "x", AsRecorded: recorded}
	end := TurnCursor{Turn: "t1", Position: TurnLength(recorded.Text)}
	two := ConversationBatch{Start: "s", Through: end, Parts: []TurnPart{{ID: "t0", Role: "operator", Text: "hello", Whole: true}, rebuilt}}
	if whole := two.Keep(2, 0); whole.Through != end || whole.Parts[1].Text != "the words" {
		t.Errorf("kept whole: %+v", whole)
	}
	if behind := two.Keep(2, 3); len(behind.Parts) != 1 || behind.Through != (TurnCursor{Turn: "t0", Position: 5}) {
		t.Errorf("cut behind another part: %+v", behind)
	}
	// Kept whole with a part after it dropped, the cursor stands at the recorded end all the same.
	first := ConversationBatch{Start: "s", Through: TurnCursor{Turn: "t3", Position: 1}, Parts: []TurnPart{rebuilt, {ID: "t3", Role: "operator", Text: "x", Whole: true}}}
	if kept := first.Keep(1, 0); kept.Through != end || kept.Parts[0].Text != "the words" {
		t.Errorf("kept whole, the next part dropped: %+v", kept)
	}
	one := ConversationBatch{Start: "s", Through: end, Parts: []TurnPart{rebuilt}}
	cut := one.Keep(1, 3)
	if len(cut.Parts) != 1 || cut.Parts[0].Text != "[vo" || cut.Parts[0].Role != "participant" || cut.Parts[0].Whole || cut.Parts[0].TurnID != "x" {
		t.Errorf("cut alone: %+v", cut)
	}
	if cut.Start != "s" || cut.Through != (TurnCursor{Turn: "t1", Position: 3}) {
		t.Errorf("cut alone, the cursor: %+v", cut)
	}
	// A record shorter than the cut is read whole.
	short := TurnPart{ID: "t2", Role: "operator", Text: "a much longer rebuilt text", Whole: true, AsRecorded: &TurnPart{ID: "t2", Role: "operator", Text: "[voice] hi", Whole: true}}
	got := ConversationBatch{Start: "s", Parts: []TurnPart{short}}.Keep(1, 20)
	if len(got.Parts) != 1 || got.Parts[0].Text != "[voice] hi" || !got.Parts[0].Whole || got.Through != (TurnCursor{Turn: "t2", Position: 10}) {
		t.Errorf("a shorter record: %+v", got)
	}
	if rebuilt.Text != "the words" || one.Parts[0].AsRecorded != recorded {
		t.Errorf("Keep changed the batch it was asked of: %+v", one)
	}
}
