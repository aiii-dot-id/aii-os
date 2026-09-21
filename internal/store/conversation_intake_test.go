package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const roomNote = "[voice] (heard in the room, not addressed to you) "

// .
func say(t *testing.T, s *Store, role, content string) string {
	t.Helper()
	if err := s.AddConversationTurn(role, content); err != nil {
		t.Fatal(err)
	}
	turns, err := s.RecentTurnsIncludingSystem(1)
	if err != nil || len(turns) != 1 {
		t.Fatalf("read back the turn: %v %v", turns, err)
	}
	return turns[0].ID
}

func partsText(b ConversationBatch) string {
	var out []string
	for _, p := range b.Parts {
		out = append(out, p.Role+":"+p.Text)
	}
	return strings.Join(out, "|")
}

// .
// .
func beginAt(t *testing.T, s *Store, turn string) {
	t.Helper()
	if err := s.PublishConversationCursor(ConversationBatch{Through: TurnCursor{Turn: turn, Position: 0}}); err != nil {
		t.Fatal(err)
	}
}

func consume(t *testing.T, s *Store, b ConversationBatch) {
	t.Helper()
	if err := s.PublishConversationCursor(b); err != nil {
		t.Fatal(err)
	}
}

// .
// .
func TestTheIntakeReadsTheViewsRolesInSourceOrder(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "good morning")
	say(t, s, "system", "tool: ls -> 3 files")
	say(t, s, "resident", "good morning, Sam")
	last := say(t, s, "participant", "[from Nova] hello both")

	b, err := s.NextConversation(1000, roomNote)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := partsText(b), "operator:good morning|resident:good morning, Sam|participant:[from Nova] hello both"; got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
	for _, p := range b.Parts {
		if !p.Whole || p.From != 0 {
			t.Errorf("a turn that fits was not read whole: %+v", p)
		}
	}
	if b.Through.Turn != last || b.Through.Position != len("[from Nova] hello both") {
		t.Fatalf("the batch ends at %+v, want the last turn read through", b.Through)
	}
	consume(t, s, b)
	b, err = s.NextConversation(1000, roomNote)
	if err != nil || len(b.Parts) != 0 || b.Moved() {
		t.Fatalf("after the batch was consumed: %+v %v, want nothing unread", b, err)
	}
	say(t, s, "operator", "one more thing")
	if b, _ = s.NextConversation(1000, roomNote); partsText(b) != "operator:one more thing" {
		t.Fatalf("a turn said after the cursor was selected as %q", partsText(b))
	}
}

// .
// .
func TestWholeTurnsArePreferred(t *testing.T) {
	s := testStore(t)
	first := say(t, s, "operator", strings.Repeat("a", 40))
	say(t, s, "resident", strings.Repeat("b", 40))
	say(t, s, "operator", strings.Repeat("c", 40))
	beginAt(t, s, first)
	b, err := s.NextConversation(100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := partsText(b); got != "operator:"+strings.Repeat("a", 40)+"|resident:"+strings.Repeat("b", 40) {
		t.Fatalf("a budget of 100 selected %q, want the two whole turns that fit", got)
	}
	consume(t, s, b)
	if b, _ = s.NextConversation(100, ""); partsText(b) != "operator:"+strings.Repeat("c", 40) || !b.Parts[0].Whole {
		t.Fatalf("the turn that waited was selected as %q", partsText(b))
	}
}

// .
// .
func TestAnOversizeTurnIsReadAcrossPassesWithoutALostRemainder(t *testing.T) {
	s := testStore(t)
	big := strings.Repeat("é", 25) + strings.Repeat("ü", 25) + "END"
	id := say(t, s, "resident", big)
	after := say(t, s, "operator", "short")
	beginAt(t, s, id)

	read := map[string]string{}
	passes := 0
	for ; passes < 10; passes++ {
		b, err := s.NextConversation(20, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Parts) == 0 {
			break
		}
		if passes < 2 {
			// .
			// .
			p := b.Parts[0]
			if len(b.Parts) != 1 || p.ID != id || p.From != passes*20 || p.Whole || b.Through != (TurnCursor{Turn: id, Position: (passes + 1) * 20}) {
				t.Fatalf("pass %d selected %+v through %+v, want 20 code points of the oversize turn from %d", passes, b.Parts, b.Through, passes*20)
			}
		}
		for _, p := range b.Parts {
			if len(read[p.ID]) != len(string([]rune(big)[:p.From])) && p.ID == id {
				t.Fatalf("pass %d begins at position %d, which is not where the last part ended", passes, p.From)
			}
			read[p.ID] += p.Text
		}
		consume(t, s, b)
	}
	if read[id] != big {
		t.Fatalf("read across passes %q, want the turn exactly — a remainder was lost or repeated", read[id])
	}
	if read[after] != "short" {
		t.Fatalf("the turn after the oversize one was read as %q, want it once and whole", read[after])
	}
	if passes != 3 {
		t.Fatalf("53 code points and a short turn took %d passes of 20, want 3", passes)
	}
}

// .
// .
// .
func TestRoomWordsArePassedOverAndNeverBlockWhatFollows(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "before the meeting")
	b, _ := s.NextConversation(1000, roomNote)
	consume(t, s, b)
	for i := 0; i < MaxTurnsPerPass+10; i++ {
		say(t, s, "operator", roomNote+fmt.Sprintf("room words %d", i))
	}
	after := say(t, s, "operator", "after the meeting")

	for pass := 0; pass < 3; pass++ {
		b, err := s.NextConversation(1000, roomNote)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range b.Parts {
			if strings.Contains(p.Text, "room words") {
				t.Fatalf("room words were selected: %q", p.Text)
			}
		}
		if len(b.Parts) == 1 && b.Parts[0].ID == after {
			return
		}
		if !b.Moved() {
			t.Fatalf("pass %d: the cursor did not move over the room's words — what follows them is unreachable", pass)
		}
		consume(t, s, b)
	}
	t.Fatal("three passes never reached the turn after the meeting")
}

// .
// .
// .
func TestAFirstRunBeginsOneBudgetBackFromTheEnd(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 50; i++ {
		say(t, s, "operator", fmt.Sprintf("old turn %02d ", i)+strings.Repeat("x", 30))
	}
	b, err := s.NextConversation(100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 2 || !strings.HasPrefix(b.Parts[0].Text, "old turn 48") || !strings.HasPrefix(b.Parts[1].Text, "old turn 49") {
		t.Fatalf("a first run with a budget of 100 selected %q, want the last two turns", partsText(b))
	}
	// .
	s3 := testStore(t)
	say(t, s3, "operator", "far too old to fit "+strings.Repeat("x", 80))
	say(t, s3, "operator", "before "+strings.Repeat("a", 33))
	say(t, s3, "operator", roomNote+strings.Repeat("r", 60))
	say(t, s3, "operator", "after "+strings.Repeat("b", 34))
	b, err = s3.NextConversation(100, roomNote)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 2 || !strings.HasPrefix(b.Parts[0].Text, "before") || !strings.HasPrefix(b.Parts[1].Text, "after") {
		t.Fatalf("with room words between them a first run selected %q, want the two turns that fit", partsText(b))
	}
	// .
	s2 := testStore(t)
	say(t, s2, "operator", "older")
	big := say(t, s2, "resident", strings.Repeat("y", 500))
	b, err = s2.NextConversation(100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 1 || b.Parts[0].ID != big || b.Parts[0].Whole || b.Parts[0].From != 0 {
		t.Fatalf("with an oversize last turn a first run selected %+v, want its first part", b.Parts)
	}
	// .
	if b, err = testStore(t).NextConversation(100, ""); err != nil || len(b.Parts) != 0 || b.Moved() {
		t.Fatalf("an empty transcript gave %+v %v", b, err)
	}
}

// .
// .
// .
func TestACursorForATurnThatIsGoneIsNotBelieved(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "first")
	say(t, s, "operator", "second")
	s.mu.Lock()
	err := s.setRuntimeMeta(ConversationCursorKey, `{"turn":"turn_from_another_life","position":3}`)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.NextConversation(1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if partsText(b) != "operator:first|operator:second" {
		t.Fatalf("with a cursor from another transcript the intake selected %q, want a budget back from the end", partsText(b))
	}
	consume(t, s, b)
	if c, _ := s.ConversationCursor(); c.Turn != b.Through.Turn {
		t.Fatalf("the cursor reads %+v, want %+v", c, b.Through)
	}
	for _, junk := range []string{"not json", `{"turn":"","position":1}`, `{"turn":"x","position":-4}`} {
		s.mu.Lock()
		_ = s.setRuntimeMeta(ConversationCursorKey, junk)
		s.mu.Unlock()
		if b, err := s.NextConversation(1000, ""); err != nil || len(b.Parts) != 2 {
			t.Fatalf("cursor %q: %+v %v, want it disbelieved and work repeated", junk, b.Parts, err)
		}
	}
}

// .
// .
func TestALatePassCannotMoveTheConversationCursorBack(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "one")
	early, _ := s.NextConversation(1000, "")
	say(t, s, "operator", "two")
	late, _ := s.NextConversation(1000, "")
	consume(t, s, late)
	if err := s.PublishConversationCursor(early); !errors.Is(err, ErrCursorMoved) {
		t.Fatalf("a pass selected from a stale cursor got %v, want ErrCursorMoved", err)
	}
	if c, _ := s.ConversationCursor(); c != late.Through {
		t.Fatalf("the cursor reads %+v after a lost race, want %+v", c, late.Through)
	}
}

// .
// .
func TestAFailedConversationReadIsNotAnEmptyBacklog(t *testing.T) {
	s := testStore(t)
	for _, budget := range []int{0, -1} {
		if _, err := s.NextConversation(budget, ""); err == nil {
			t.Fatalf("a budget of %d was accepted", budget)
		}
	}
	s.Close()
	if _, err := s.NextConversation(100, ""); err == nil {
		t.Fatal("a closed database read as an empty backlog")
	}
}

// .
// .
func TestOneSelectionExaminesABoundedNumberOfTurns(t *testing.T) {
	s := testStore(t)
	beginAt(t, s, say(t, s, "operator", "a"))
	for i := 0; i < MaxTurnsPerPass+40; i++ {
		say(t, s, "operator", "b")
	}
	b, err := s.NextConversation(1_000_000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != MaxTurnsPerPass {
		t.Fatalf("one selection took %d turns, want %d", len(b.Parts), MaxTurnsPerPass)
	}
}

// .
// .
// .
// .
func TestATurnHoldingANulIsReadWholeAcrossPasses(t *testing.T) {
	s := testStore(t)
	// .
	// .
	big := "ab\x00" + strings.Repeat("é", 120) + "\xc3" + "END"
	id := say(t, s, "resident", big)
	after := say(t, s, "participant", "and\x00then")
	beginAt(t, s, id)

	read := map[string]string{}
	for pass := 0; pass < 10; pass++ {
		b, err := s.NextConversation(50, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Parts) == 0 {
			break
		}
		for _, p := range b.Parts {
			read[p.ID] += p.Text
		}
		consume(t, s, b)
	}
	// .
	// .
	// .
	if want := string([]rune(big)); read[id] != want {
		t.Fatalf("read %d of %d code points of the oversize turn — a remainder was lost behind a NUL or a torn rune",
			len([]rune(read[id])), len([]rune(want)))
	}
	if read[after] != "and\x00then" {
		t.Fatalf("the turn after it was read as %q", read[after])
	}
	// .
	// .
	// .
	// .
	s2 := testStore(t)
	say(t, s2, "operator", "older")
	last := say(t, s2, "operator", "and\x00then")
	b, err := s2.NextConversation(10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 1 || b.Parts[0].ID != last || !b.Parts[0].Whole {
		t.Fatalf("a first run over a turn holding a NUL selected %q, want the last turn, whole", partsText(b))
	}
}
