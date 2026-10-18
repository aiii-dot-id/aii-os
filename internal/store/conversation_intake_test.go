package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

const roomNote = "[voice] (heard in the room, not addressed to you) "

// say adds a turn and returns its stable id.
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

// beginAt stands a cursor at the start of a turn, from nothing: without a
// cursor, selection begins a budget back from the END of the transcript.
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

// The turns the searchable view names, in source order — and no others:
// a system turn is tool output, a record and not a memory.
func TestTheIntakeReadsTheViewsRolesInSourceOrder(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "good morning")
	say(t, s, "system", "tool: ls -> 3 files")
	say(t, s, "resident", "good morning, Quinn")
	last := say(t, s, "participant", "[from Mira] hello both")

	b, err := s.NextConversation(1000, roomNote)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := partsText(b), "operator:good morning|resident:good morning, Quinn|participant:[from Mira] hello both"; got != want {
		t.Fatalf("selected %q, want %q", got, want)
	}
	for _, p := range b.Parts {
		if !p.Whole || p.From != 0 {
			t.Errorf("a turn that fits was not read whole: %+v", p)
		}
	}
	if b.Through.Turn != last || b.Through.Position != len("[from Mira] hello both") {
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

// Whole turns while they fit; the one that does not fit waits, whole, for
// the next pass — never half of it now because there was room for half.
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

// A turn larger than a whole pass is read across passes by position, in
// code points, and the cursor never moves past an unread remainder.
func TestAnOversizeTurnIsReadAcrossPassesWithoutALostRemainder(t *testing.T) {
	s := testStore(t)
	big := strings.Repeat("é", 25) + strings.Repeat("ü", 25) + "END" // 53 code points, 103 bytes
	// Historical byte-bounded records can contain torn UTF-8. Current append
	// rejects new invalid UTF-8; seed the historical bytes directly to retain
	// the reader regression without weakening new-write validation.
	id := "legacy-torn-turn"
	if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at) VALUES(?,'default','resident',?,1,'2026-09-25T00:00:00Z')`, id, big); err != nil {
		t.Fatal(err)
	}
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
			// The oversize turn alone, a full budget of it, from where
			// the last pass stopped — and the cursor inside it.
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

// Words the host recorded from a room, addressed to no one, are passed
// over — and the cursor passes over them too: a long meeting must not
// stand between the intake and everything said after it.
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

// With no cursor the intake begins one budget back from the END — the
// most recent whole turns that fit — never at turn one: a first run must
// not dream a transcript the identity already lived.
func TestAFirstRunBeginsOneBudgetBackFromTheEnd(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 50; i++ {
		say(t, s, "operator", fmt.Sprintf("old turn %02d ", i)+strings.Repeat("x", 30)) // 42 code points each
	}
	b, err := s.NextConversation(100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 2 || !strings.HasPrefix(b.Parts[0].Text, "old turn 48") || !strings.HasPrefix(b.Parts[1].Text, "old turn 49") {
		t.Fatalf("a first run with a budget of 100 selected %q, want the last two turns", partsText(b))
	}
	// Room words cost the first pass none of its budget: they are not read.
	s3 := testStore(t)
	say(t, s3, "operator", "far too old to fit "+strings.Repeat("x", 80))
	say(t, s3, "operator", "before "+strings.Repeat("a", 33)) // 40
	say(t, s3, "operator", roomNote+strings.Repeat("r", 60))
	say(t, s3, "operator", "after "+strings.Repeat("b", 34)) // 40
	b, err = s3.NextConversation(100, roomNote)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 2 || !strings.HasPrefix(b.Parts[0].Text, "before") || !strings.HasPrefix(b.Parts[1].Text, "after") {
		t.Fatalf("with room words between them a first run selected %q, want the two turns that fit", partsText(b))
	}
	// A last turn larger than a whole pass: begin AT it, in parts.
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
	// An empty transcript is nothing unread, and no error.
	if b, err = testStore(t).NextConversation(100, ""); err != nil || len(b.Parts) != 0 || b.Moved() {
		t.Fatalf("an empty transcript gave %+v %v", b, err)
	}
}

// A cursor whose turn the table no longer holds was never this
// transcript's: it is not believed, and it can never select a different
// turn through a reused sequence number.
func TestACursorForATurnThatIsGoneIsNotBelieved(t *testing.T) {
	s := testStore(t)
	say(t, s, "operator", "first")
	say(t, s, "operator", "second")
	s.mu.Lock()
	err := setRuntimeMeta(s.w(), ConversationCursorKey, `{"turn":"turn_from_another_life","position":3}`)
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
	consume(t, s, b) // and it is replaced by compare-and-swap from the value that was read
	if c, _ := s.ConversationCursor(); c.Turn != b.Through.Turn {
		t.Fatalf("the cursor reads %+v, want %+v", c, b.Through)
	}
	for _, junk := range []string{"not json", `{"turn":"","position":1}`, `{"turn":"x","position":-4}`} {
		s.mu.Lock()
		_ = setRuntimeMeta(s.w(), ConversationCursorKey, junk)
		s.mu.Unlock()
		if b, err := s.NextConversation(1000, ""); err != nil || len(b.Parts) != 2 {
			t.Fatalf("cursor %q: %+v %v, want it disbelieved and work repeated", junk, b.Parts, err)
		}
	}
}

// Published only if the cursor still holds the value the batch was
// selected from: a late pass cannot move it back.
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

// A read that fails is an error, never an empty backlog; and the store
// owns the bound of its own read.
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

// Bounded whatever the length of the transcript: one selection examines
// at most MaxTurnsPerPass turns.
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

// ONE LENGTH FOR A TURN. SQLite's length() stops at the first NUL — which
// the JSON a message arrives as may carry — and Go's count does not. With
// two lengths a cursor inside an oversize turn was read as past its end,
// and the rest of the turn was never read.
func TestATurnHoldingANulIsReadWholeAcrossPasses(t *testing.T) {
	s := testStore(t)
	// A NUL, and a rune torn at a byte boundary (a byte-bounded cut upstream
	// makes these): SQL's length() miscounts both, in different ways.
	big := "ab\x00" + strings.Repeat("é", 120) + "\xc3" + "END"
	// Historical byte-bounded records can contain torn UTF-8. Current append
	// rejects new invalid UTF-8; seed the historical bytes directly to retain
	// the reader regression without weakening new-write validation.
	id := "legacy-torn-turn"
	if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at) VALUES(?,'default','resident',?,1,'2026-09-25T00:00:00Z')`, id, big); err != nil {
		t.Fatal(err)
	}
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
	// Compared in code points, which is what a position counts: a torn
	// byte is shown as U+FFFD once a turn is cut into parts, and is still
	// one code point, in its place.
	if want := string([]rune(big)); read[id] != want {
		t.Fatalf("read %d of %d code points of the oversize turn — a remainder was lost behind a NUL or a torn rune",
			len([]rune(read[id])), len([]rune(want)))
	}
	if read[after] != "and\x00then" {
		t.Fatalf("the turn after it was read as %q", read[after])
	}
	// And a first run counts the same way. The last turn is 8 code points,
	// not the 3 SQL sees: with a budget of 10 it fits and "older" does not
	// fit behind it. Counted in SQL, both seemed to fit, the pass began at
	// "older", and the newest turn — the one a first run is for — waited.
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

func TestTheStoredConversationCursorKeepsItsBytes(t *testing.T) {
	s := testStore(t)
	id := say(t, s, "operator", "héllo") // 5 code points, 6 bytes
	stored := `{"turn":"` + id + `","position":5}`
	s.mu.Lock()
	err := setRuntimeMeta(s.w(), ConversationCursorKey, stored)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.NextConversation(1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Parts) != 0 || b.Start != stored || b.Through != (TurnCursor{Turn: id, Position: 5}) || b.Moved() {
		t.Fatalf("a stored cursor %s read back as %+v (moved %v), want it believed where it stood and not moved", stored, b, b.Moved())
	}
	next := say(t, s, "operator", "more")
	b, err = s.NextConversation(1000, "")
	if err != nil {
		t.Fatal(err)
	}
	consume(t, s, b)
	s.mu.RLock()
	got, err := s.getRuntimeMeta(ConversationCursorKey)
	s.mu.RUnlock()
	if want := `{"turn":"` + next + `","position":4}`; err != nil || got != want {
		t.Fatalf("the cursor was stored as %q (%v), want %q", got, err, want)
	}
}

// ONE REFUSAL, ONE VALUE. The landing reads a lost race as
// cursor.ErrCursorMoved and the store's two cursors answer with
// store.ErrCursorMoved: they are one value, so a pass that lost its race is
// read as that, not as a fault — for the conversation cursor and the
// outcome cursor both.
func TestTheCursorRefusalIsOneValue(t *testing.T) {
	if ErrCursorMoved != cursor.ErrCursorMoved {
		t.Fatal("store.ErrCursorMoved is a value of its own, not cursor.ErrCursorMoved")
	}
	s := testStore(t)
	say(t, s, "operator", "one")
	early, _ := s.NextConversation(1000, "")
	consume(t, s, early)
	if err := s.PublishConversationCursor(early); !errors.Is(err, cursor.ErrCursorMoved) {
		t.Fatalf("the conversation cursor's lost race answered %v, want cursor.ErrCursorMoved", err)
	}
	r := newOutcomeRig(t) // two records: the genesis and a relationship
	if err := r.st.PublishOutcomeCursor(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.st.PublishOutcomeCursor(0, 1); !errors.Is(err, cursor.ErrCursorMoved) {
		t.Fatalf("the outcome cursor's lost race answered %v, want cursor.ErrCursorMoved", err)
	}
}
