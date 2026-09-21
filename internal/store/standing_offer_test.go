package store

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStandingOfferRoundTrips(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.StandingOffer(); err != nil || got != nil {
		t.Fatalf("a fresh store holds %v, %v — want nothing, no error", got, err)
	}
	want := []StandingSeat{{Name: "pl_com_example_memory_store", Print: "5b1c"}, {Name: "pl_com_example_memory_search", Print: "9e02"}}
	if err := s.SetStandingOffer(want); err != nil {
		t.Fatal(err)
	}
	if got, err := s.StandingOffer(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %v, %v", got, err)
	}
	if err := s.SetStandingOffer(nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.StandingOffer(); err != nil || len(got) != 0 {
		t.Fatalf("a cleared offer reads back %v, %v", got, err)
	}
}

// .
// .
// .
func TestAStandingOfferOfNamesReadsBackWithoutFingerprints(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	err = s.setRuntimeMeta(standingOfferKey, `["pl_com_example_memory_store","pl_com_example_memory_search"]`)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.StandingOffer()
	want := []StandingSeat{{Name: "pl_com_example_memory_store"}, {Name: "pl_com_example_memory_search"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("a record of names read back %v, %v", got, err)
	}
}
