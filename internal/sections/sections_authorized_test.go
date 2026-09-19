package sections

import (
	"errors"
	"sync/atomic"
	"testing"
)

// .
// .
// .
func TestAWithdrawnSectionIsNeitherRegisteredNorRead(t *testing.T) {
	r := NewRegistry()
	var prevOK, nextOK atomic.Bool
	prevOK.Store(true)
	nextOK.Store(true)
	prev := &Section{Decl: Decl{ID: "panel"}, Allowed: prevOK.Load}
	next := &Section{Decl: Decl{ID: "panel"}, Allowed: nextOK.Load}
	if err := r.Register(prev); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	nextOK.Store(false)
	if err := r.Replace(prev, next); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("a withdrawn successor was registered: %v", err)
	}
	if got, ok := r.Get("panel"); !ok || got != prev {
		t.Fatal("a refused replacement displaced the predecessor")
	}
	if err := r.Register(&Section{Decl: Decl{ID: "other"}, Allowed: nextOK.Load}); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("a withdrawn section was registered: %v", err)
	}
	// .
	// .
	nextOK.Store(true)
	if err := r.Replace(prev, next); err != nil {
		t.Fatal(err)
	}
	nextOK.Store(false)
	if _, ok := r.Get("panel"); ok {
		t.Error("a withdrawn section is still served")
	}
	if n := len(r.List()); n != 0 {
		t.Errorf("a withdrawn section is still listed (%d)", n)
	}
	if !r.RemoveOwned(next) {
		t.Error("the owner could not take its own withdrawn registration away")
	}
	// .
	plain := &Section{Decl: Decl{ID: "dev"}}
	if err := r.Register(plain); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get("dev"); !ok || got != plain {
		t.Error("a section with no owner's word was hidden")
	}
}
