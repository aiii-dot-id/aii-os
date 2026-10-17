package landing

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

// journal is a door and both cursor owners that write down, in one list,
// the order things happened in.
type journal struct {
	did    []string
	refuse string // an event type, or "cursor"
	seq    uint64
}

func (j *journal) Append(t ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	if string(t) == j.refuse {
		return nil, errors.New("refused before append")
	}
	j.seq++
	j.did = append(j.did, string(t))
	return &ledger.Event{Seq: j.seq, Type: t}, nil
}

func (j *journal) PublishConversationCursor(b cursor.ConversationBatch) error {
	if j.refuse == "cursor" {
		return errors.New("the database is read-only")
	}
	j.did = append(j.did, "cursor")
	return nil
}

func (j *journal) PublishOutcomeCursor(from, through uint64) error {
	j.did = append(j.did, "outcome-cursor")
	return nil
}

func read(parts int) cursor.ConversationBatch {
	b := cursor.ConversationBatch{Through: cursor.TurnCursor{Turn: "turn_a", Position: 5}}
	for i := 0; i < parts; i++ {
		b.Parts = append(b.Parts, cursor.TurnPart{ID: "turn_a", Role: "operator", Text: "hello", Whole: true})
	}
	return b
}

var aNote = map[string]any{"id": "exp_dream_x", "content": "a noticing", "provenance": "dream"}

// THE ONE ORDER: the product, then the marker, then the cursor — and the
// marker names the product it closes over.
func TestALandingHasOneOrder(t *testing.T) {
	j := &journal{}
	out, err := New(j).Land(Pass{Product: aNote, Marker: ledger.EventDreamRun, Inputs: []string{"exp_a"}, Cursor: ConversationCursors(j)(read(1))})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(j.did, ","); got != "experience.create,dream.run,cursor" {
		t.Fatalf("landed in the order %q", got)
	}
	if out.Product == nil || !out.Marked || !out.Advanced {
		t.Fatalf("landed %+v, want all three", out)
	}
}

// EACH STEP ONLY IF THE ONE BEFORE IT LANDED. A refused product leaves no
// marker and no cursor; a refused marker leaves no cursor; and the result
// says how far it got, because what the caller may do next depends on it.
func TestALandingStopsAtTheFirstStepThatDidNotLand(t *testing.T) {
	for refuse, want := range map[string]string{
		"experience.create": "",
		"dream.run":         "experience.create",
		"cursor":            "experience.create,dream.run",
	} {
		j := &journal{refuse: refuse}
		out, err := New(j).Land(Pass{Product: aNote, Marker: ledger.EventDreamRun, Inputs: []string{"exp_a"}, Cursor: ConversationCursors(j)(read(1))})
		if err == nil {
			t.Fatalf("%s refused and the landing reported no error", refuse)
		}
		if got := strings.Join(j.did, ","); got != want {
			t.Errorf("%s refused: what happened is %q, want %q", refuse, got, want)
		}
		if out.Advanced || (refuse != "cursor" && out.Marked) || (refuse == "experience.create" && out.Product != nil) {
			t.Errorf("%s refused: the landing claims %+v", refuse, out)
		}
	}
}

// AN HONEST NOTHING consumes what it read and mints no product: the marker
// carries the inputs, and a pass that read no experiences mints no marker
// at all — a marker with no inputs is a ledgered no-op.
func TestAnHonestNothingLandsItsMarkerAndItsCursorAndNoProduct(t *testing.T) {
	j := &journal{}
	if _, err := New(j).Land(Pass{Marker: ledger.EventDreamRun, Inputs: []string{"exp_a"}, Cursor: ConversationCursors(j)(read(1))}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(j.did, ","); got != "dream.run,cursor" {
		t.Fatalf("an honest nothing over experiences and conversation landed %q", got)
	}
	j = &journal{}
	if _, err := New(j).Land(Pass{Marker: ledger.EventDreamRun, Cursor: ConversationCursors(j)(read(1))}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(j.did, ","); got != "cursor" {
		t.Fatalf("an honest nothing over conversation alone landed %q, want the cursor and no marker", got)
	}
	// No door: nothing durable can be said, so nothing is consumed — not
	// even a cursor.
	j = &journal{}
	if _, err := New(nil).Land(Pass{Product: aNote, Cursor: ConversationCursors(j)(read(1))}); !errors.Is(err, ErrNoDoor) || len(j.did) != 0 {
		t.Fatalf("with no door: %v, and %v happened", err, j.did)
	}
}

// WHAT WAS READ IS NEVER STEPPED OVER. PassOver moves a cursor over turns
// nobody read; a cursor under which a pass was shown anything is refused
// there — it is consumed by a product or an honest nothing, through Land.
func TestOnlyWhatNobodyReadIsPassedOver(t *testing.T) {
	j := &journal{}
	l := New(j)
	if moved, err := l.PassOver(ConversationCursors(j)(read(0))); err != nil || !moved || strings.Join(j.did, ",") != "cursor" {
		t.Fatalf("passing over room words: moved=%v err=%v did=%v", moved, err, j.did)
	}
	j = &journal{}
	if moved, err := New(j).PassOver(ConversationCursors(j)(read(2))); err == nil || moved || len(j.did) != 0 {
		t.Fatalf("A CURSOR OVER TURNS A PASS WAS SHOWN WAS PASSED OVER: moved=%v err=%v did=%v", moved, err, j.did)
	}
	j = &journal{}
	if moved, _ := New(j).PassOver(OutcomeCursors(j)(store.OutcomeBatch{From: 3, Through: 9, Outcomes: []store.Outcome{{Seq: 5}}})); moved {
		t.Fatal("a window holding an outcome was passed over")
	}
}

// A CURSOR CANNOT BE FIRED FROM OUTSIDE THIS PACKAGE: it has no exported
// field and no exported method that publishes. That is the whole of the
// enforcement — a facility holds cursors and a Lander, and nothing else it
// could move a cursor with.
func TestACursorIsOpaque(t *testing.T) {
	typ := reflect.TypeOf(Cursor{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Errorf("Cursor.%s is exported — a facility could fire or forge a cursor", typ.Field(i).Name)
		}
	}
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; name != "Moves" {
			t.Errorf("Cursor has an exported method %s — the only thing a facility may ask a cursor is whether it moves", name)
		}
	}
}
