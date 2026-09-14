package store

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecordMemoryAccessCountsAndKeepsABoundedHistory(t *testing.T) {
	s := testStore(t)
	ref := MemoryRef{Store: "experiences", ID: "e1"}
	t0 := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)

	if _, ok, err := s.MemoryAccessOf(ref); err != nil || ok {
		t.Fatalf("a memory never recalled has no record: ok=%v err=%v", ok, err)
	}
	if err := s.RecordMemoryAccess(t0, ref); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryAccess(t0.Add(time.Hour), ref); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.MemoryAccessOf(ref)
	if err != nil || !ok {
		t.Fatalf("record missing after two accesses: ok=%v err=%v", ok, err)
	}
	if a.Count != 2 || !a.LastAt.Equal(t0.Add(time.Hour)) || len(a.History) != 2 || !a.History[0].Equal(t0) {
		t.Fatalf("record = %+v", a)
	}

	// .
	for i := 2; i < 40; i++ {
		if err := s.RecordMemoryAccess(t0.Add(time.Duration(i)*time.Hour), ref); err != nil {
			t.Fatal(err)
		}
	}
	a, _, err = s.MemoryAccessOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	if a.Count != 40 {
		t.Errorf("count = %d, want 40", a.Count)
	}
	if len(a.History) != accessHistoryLimit {
		t.Errorf("history holds %d, want the bound %d", len(a.History), accessHistoryLimit)
	}
	if !a.History[0].Equal(t0.Add(8 * time.Hour)) {
		t.Errorf("the history must drop its oldest entries: first is %v", a.History[0])
	}
	if !a.History[len(a.History)-1].Equal(a.LastAt) {
		t.Errorf("the newest history entry must be the last access")
	}
}

func TestRecordMemoryAccessRefusesUnknownStoresBeforeWriting(t *testing.T) {
	s := testStore(t)
	err := s.RecordMemoryAccess(time.Now(),
		MemoryRef{Store: "experiences", ID: "e1"},
		MemoryRef{Store: "nothing", ID: "x"})
	if err == nil || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("an unknown store must be refused by name: %v", err)
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "experiences", ID: "e1"}); ok {
		t.Fatal("a refused batch wrote its good half")
	}
	if err := s.RecordMemoryAccess(time.Now(), MemoryRef{Store: "beliefs", ID: ""}); err == nil {
		t.Fatal("an empty id was accepted")
	}
	if _, err := s.MemoryAccesses("nothing", []string{"x"}); err == nil {
		t.Fatal("MemoryAccesses accepted an unknown store")
	}
}

func TestMemoryAccessesReadsManyIdsInOneStore(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	var refs []MemoryRef
	var ids []string
	for i := 0; i < 450; i++ {
		id := "b" + strings.Repeat("0", 3-len(strconv.Itoa(i))) + strconv.Itoa(i)
		refs = append(refs, MemoryRef{Store: "beliefs", ID: id})
		ids = append(ids, id)
	}
	if err := s.RecordMemoryAccess(at, refs...); err != nil {
		t.Fatal(err)
	}
	// .
	if err := s.RecordMemoryAccess(at, MemoryRef{Store: "experiences", ID: "b000"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MemoryAccesses("beliefs", append(ids, "never"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 450 {
		t.Fatalf("read %d records, want 450 (more than one chunk)", len(got))
	}
	if got["b000"].Store != "beliefs" || got["b000"].Count != 1 || !got["b000"].LastAt.Equal(at) {
		t.Errorf("b000 = %+v", got["b000"])
	}
	if _, ok := got["never"]; ok {
		t.Error("an id never recalled appeared in the result")
	}
	empty, err := s.MemoryAccesses("beliefs", nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("no ids must read nothing: %v %v", empty, err)
	}
}
