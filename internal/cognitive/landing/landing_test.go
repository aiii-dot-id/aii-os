package landing

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
type journal struct {
	did    []string
	refuse string
	seq    uint64
}

func (j *journal) Append(t ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error) {
	if string(t) == j.refuse {
		return nil, errors.New("refused before append")
	}
	j.seq++
	j.did = append(j.did, string(t))
	return &ledger.Event{Seq: j.seq, Type: t}, nil
}

func (j *journal) PublishConversationCursor(b store.ConversationBatch) error {
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

func read(parts int) store.ConversationBatch {
	b := store.ConversationBatch{Through: store.TurnCursor{Turn: "turn_a", Position: 5}}
	for i := 0; i < parts; i++ {
		b.Parts = append(b.Parts, store.TurnPart{ID: "turn_a", Role: "operator", Text: "hello", Whole: true})
	}
	return b
}

var aNote = map[string]interface{}{"id": "exp_dream_x", "content": "a noticing", "provenance": "dream"}

// .
// .
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

// .
// .
// .
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

// .
// .
// .
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
	// .
	// .
	j = &journal{}
	if _, err := New(nil).Land(Pass{Product: aNote, Cursor: ConversationCursors(j)(read(1))}); !errors.Is(err, ErrNoDoor) || len(j.did) != 0 {
		t.Fatalf("with no door: %v, and %v happened", err, j.did)
	}
}

// .
// .
// .
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

// .
// .
// .
// .
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
