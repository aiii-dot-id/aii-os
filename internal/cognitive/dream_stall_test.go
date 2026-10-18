package cognitive

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

// oneTurn is a transcript holding one unread turn, until it is published.
// at is where its cursor then stands.
type oneTurn struct {
	read bool
	at   cursor.TurnCursor
}

func (o *oneTurn) NextConversation(budget int, exclude string) (cursor.ConversationBatch, error) {
	if o.read {
		return readTo(o.at), nil
	}
	return cursor.ConversationBatch{
		Through: cursor.TurnCursor{Turn: "turn_a", Position: 5},
		Parts:   []cursor.TurnPart{{ID: "turn_a", Role: "operator", Text: "hello", Whole: true}},
	}, nil
}
func (o *oneTurn) PublishConversationCursor(b cursor.ConversationBatch) error {
	o.read, o.at = true, b.Through
	return nil
}

// readTo is what the store answers of a transcript with nothing unread: no
// part, and the cursor where it stands (store.NextConversation sets Through
// to the stored cursor before it selects). Never the zero batch: only a
// transcript that was never read has no cursor to answer with, and a fake
// that answered so hid what a pass makes of a cursor that stands.
func readTo(at cursor.TurnCursor) cursor.ConversationBatch {
	stored, _ := json.Marshal(at)
	return cursor.ConversationBatch{Start: string(stored), Through: at}
}

// noRaw is a store with no raw experience in it.
type noRaw struct{}

func (noRaw) UnprocessedExperienceCount() (int, error)             { return 0, nil }
func (noRaw) ListRawExperiences(n int) ([]store.Experience, error) { return nil, nil }
func (noRaw) ListRawExperiencesExcept(n int, idPrefix string) ([]store.Experience, error) {
	return nil, nil
}

// A PASS THAT ADVANCED NOTHING WAITS ITS SPACING. A model that reliably
// answers over the bound — or a door that reliably refuses the note —
// leaves the conversation exactly where it was, so every tick selected
// the same turns and made the same call: one model call per tick under
// the one-voice gate, for good, and for conversation nothing else can ever
// take the delta. Spacing on attempt is RHYTHM's law for every other
// facility for this reason.
func TestAStalledDreamPassWaitsItsSpacing(t *testing.T) {
	model := &mockLLM{override: strings.Repeat("x", 500)} // over the bound, every time
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{MaxChars: 100})
	talk := &oneTurn{}
	d.SetConversation(talk)
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	for i := 0; i < 3; i++ { // three ticks, ten minutes apart
		d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
		clock = clock.Add(10 * time.Minute)
	}
	if model.calls != 1 {
		t.Fatalf("a pass that stalls made %d model calls in three ticks, want 1 — it must wait its spacing", model.calls)
	}
	if talk.read {
		t.Fatal("a stalled pass advanced the conversation")
	}
	// The spacing over, it is tried again — and a pass that works clears
	// the back-off, so new material is read on the very next tick.
	model.override = "You notice the operator says hello and waits."
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 2 || !talk.read {
		t.Fatalf("after the spacing: %d calls, conversation read=%v — want the retry to land", model.calls, talk.read)
	}
	talk.read = false
	clock = clock.Add(10 * time.Minute)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 3 {
		t.Fatalf("after a pass that worked, new material waited (%d calls) — only a stall is spaced", model.calls)
	}
}

// vanishing shows a turn to the probe and nothing to the pass that follows
// it — another pass read it in between — and then holds a turn again.
type vanishing struct {
	oneTurn
	asked int
}

func (v *vanishing) NextConversation(budget int, exclude string) (cursor.ConversationBatch, error) {
	v.asked++
	if v.asked == 2 {
		return readTo(cursor.TurnCursor{Turn: "turn_a", Position: 5}), nil
	}
	return v.oneTurn.NextConversation(budget, exclude)
}

// Finding nothing to read is not a stall. Material that was there for the
// probe and gone for the pass cost no model call, and must not cost DREAM
// its next half hour.
func TestAPassThatFoundNothingArmsNoBackOff(t *testing.T) {
	model := &mockLLM{override: "You notice the operator says hello."}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{})
	talk := &vanishing{}
	d.SetConversation(talk)
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "") // probe sees a turn; the pass finds none
	if model.calls != 0 {
		t.Fatalf("the rig made %d model calls on the tick whose material vanished", model.calls)
	}
	clock = clock.Add(10 * time.Minute)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 1 || !talk.read {
		t.Fatalf("ten minutes after a pass that found nothing: %d calls, read=%v — DREAM was held back as if it had stalled", model.calls, talk.read)
	}
}

// endlessRoom is a transcript of nothing but words nobody reads.
type endlessRoom struct{ selections, published int }

func (e *endlessRoom) NextConversation(budget int, exclude string) (cursor.ConversationBatch, error) {
	e.selections++
	return cursor.ConversationBatch{Start: "s", Through: cursor.TurnCursor{Turn: "room", Position: e.selections}}, nil
}
func (e *endlessRoom) PublishConversationCursor(b cursor.ConversationBatch) error {
	e.published++
	return nil
}

// Crossing a meeting is bounded: a probe makes a fixed number of
// selections, each one bounded read, and then says nothing is unread.
func TestCrossingRoomWordsIsBounded(t *testing.T) {
	room := &endlessRoom{}
	d := NewDream(noRaw{}, &mockLLM{}, &captureDoor{}, nil, DreamConfig{})
	d.SetConversation(room)
	if d.ConversationPending() {
		t.Fatal("a transcript of room words alone was reported unread")
	}
	if room.selections != maxRoomStretches || room.published != maxRoomStretches {
		t.Fatalf("one probe made %d selections and %d publications, want %d of each", room.selections, room.published, maxRoomStretches)
	}
}

// stuckCursor is a transcript whose cursor cannot be published.
type stuckCursor struct{ oneTurn }

func (s *stuckCursor) PublishConversationCursor(b cursor.ConversationBatch) error {
	return errors.New("the database is read-only")
}

// memRings is the surfacing section, in memory.
type memRings struct{ surfacing string }

func (m *memRings) SetRingSection(level ring.RingLevel, name, content string) { m.surfacing = content }
func (m *memRings) RingSection(level ring.RingLevel, name string) string      { return m.surfacing }

// THE NOTE LANDED AND THE CURSOR DID NOT. The surfacing follows the record
// — the note is in it — and the pass still counts as a STALL: the same
// turns will be selected again, and a cursor that cannot be published must
// not become one note per tick.
func TestANoteWhoseCursorCannotMoveIsShownAndIsAStall(t *testing.T) {
	model := &mockLLM{override: "You notice the operator says hello and waits."}
	door, rings := &captureDoor{}, &memRings{}
	d := NewDream(noRaw{}, model, door, rings, DreamConfig{})
	d.SetConversation(&stuckCursor{})
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if len(door.payloads) != 1 || rings.surfacing != model.override {
		t.Fatalf("the note landed (%d) and the surfacing reads %q — it must follow the record", len(door.payloads), rings.surfacing)
	}
	clock = clock.Add(10 * time.Minute)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 1 || len(door.payloads) != 1 {
		t.Fatalf("with a cursor that cannot move, the next tick made call %d and note %d — one note per tick, for good", model.calls, len(door.payloads))
	}
}
