package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type oneTurn struct{ read bool }

func (o *oneTurn) NextConversation(budget int, exclude string) (store.ConversationBatch, error) {
	if o.read {
		return store.ConversationBatch{}, nil
	}
	return store.ConversationBatch{
		Through: store.TurnCursor{Turn: "turn_a", Position: 5},
		Parts:   []store.TurnPart{{ID: "turn_a", Role: "operator", Text: "hello", Whole: true}},
	}, nil
}
func (o *oneTurn) PublishConversationCursor(b store.ConversationBatch) error {
	o.read = true
	return nil
}

// .
type noRaw struct{}

func (noRaw) UnprocessedExperienceCount() (int, error)             { return 0, nil }
func (noRaw) ListRawExperiences(n int) ([]store.Experience, error) { return nil, nil }
func (noRaw) ListRawExperiencesExcept(n int, idPrefix string) ([]store.Experience, error) {
	return nil, nil
}

// .
// .
// .
// .
// .
// .
// .
func TestAStalledDreamPassWaitsItsSpacing(t *testing.T) {
	model := &mockLLM{override: strings.Repeat("x", 500)}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{MaxChars: 100})
	talk := &oneTurn{}
	d.SetConversation(talk)
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	for i := 0; i < 3; i++ {
		d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
		clock = clock.Add(10 * time.Minute)
	}
	if model.calls != 1 {
		t.Fatalf("a pass that stalls made %d model calls in three ticks, want 1 — it must wait its spacing", model.calls)
	}
	if talk.read {
		t.Fatal("a stalled pass advanced the conversation")
	}
	// .
	// .
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

// .
// .
type vanishing struct {
	oneTurn
	asked int
}

func (v *vanishing) NextConversation(budget int, exclude string) (store.ConversationBatch, error) {
	v.asked++
	if v.asked == 2 {
		return store.ConversationBatch{}, nil
	}
	return v.oneTurn.NextConversation(budget, exclude)
}

// .
// .
// .
func TestAPassThatFoundNothingArmsNoBackOff(t *testing.T) {
	model := &mockLLM{override: "You notice the operator says hello."}
	d := NewDream(noRaw{}, model, &captureDoor{}, nil, DreamConfig{})
	talk := &vanishing{}
	d.SetConversation(talk)
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return clock }

	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 0 {
		t.Fatalf("the rig made %d model calls on the tick whose material vanished", model.calls)
	}
	clock = clock.Add(10 * time.Minute)
	d.OnAlarm(context.Background(), "rhythm:dream", "wall", 0, "")
	if model.calls != 1 || !talk.read {
		t.Fatalf("ten minutes after a pass that found nothing: %d calls, read=%v — DREAM was held back as if it had stalled", model.calls, talk.read)
	}
}

// .
type endlessRoom struct{ selections, published int }

func (e *endlessRoom) NextConversation(budget int, exclude string) (store.ConversationBatch, error) {
	e.selections++
	return store.ConversationBatch{Start: "s", Through: store.TurnCursor{Turn: "room", Position: e.selections}}, nil
}
func (e *endlessRoom) PublishConversationCursor(b store.ConversationBatch) error {
	e.published++
	return nil
}

// .
// .
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

// .
type stuckCursor struct{ oneTurn }

func (s *stuckCursor) PublishConversationCursor(b store.ConversationBatch) error {
	return errors.New("the database is read-only")
}

// .
type memRings struct{ surfacing string }

func (m *memRings) SetRingSection(level ring.RingLevel, name, content string) { m.surfacing = content }
func (m *memRings) RingSection(level ring.RingLevel, name string) string      { return m.surfacing }

// .
// .
// .
// .
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
