package cognitive

import (
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
)

// .
// .
// .
// .
// .
// .
// .
func TestAFacilityHoldsNothingItCouldMoveACursorWith(t *testing.T) {
	door := reflect.TypeOf((*landing.Door)(nil)).Elem()
	publishers := []string{"PublishConversationCursor", "PublishOutcomeCursor"}
	holds := func(facility interface{}, allowDoor bool) {
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
