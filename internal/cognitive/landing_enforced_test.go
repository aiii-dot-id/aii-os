package cognitive

import (
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
)

// THE ORDER OF A PASS'S EFFECTS IS NOT A FACILITY'S TO GET WRONG, because a
// facility holds nothing it could get it wrong with. DREAM keeps no door
// and no value that can publish a cursor; CONSOLIDATE keeps its door — its
// belief operations are its own — and no value that can publish the
// outcome cursor. The only way either moves a cursor is a landing
// (landing.Land, landing.PassOver), which has one order. This test is what
// stops a future field from quietly giving that power back.
func TestAFacilityHoldsNothingItCouldMoveACursorWith(t *testing.T) {
	door := reflect.TypeOf((*landing.Door)(nil)).Elem()
	publishers := []string{"PublishConversationCursor", "PublishOutcomeCursor"}
	holds := func(facility any, allowDoor bool) {
		typ := reflect.TypeOf(facility).Elem()
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			ft := f.Type
			if ft.Kind() != reflect.Interface && ft.Kind() != reflect.Ptr {
				continue
			}
			if !allowDoor && ft.Kind() == reflect.Interface && ft.Implements(door) {
				t.Errorf("%s.%s can append to the ledger — DREAM lands through its Lander and holds no door", typ.Name(), f.Name)
			}
			for _, m := range publishers {
				if _, ok := ft.MethodByName(m); ok {
					t.Errorf("%s.%s has %s — a facility holds the READING half of its source and opaque cursors, never a publisher", typ.Name(), f.Name, m)
				}
			}
		}
	}
	holds(&DreamFacility{}, false)
	holds(&ConsolidateFacility{}, true)
}
