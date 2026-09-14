package store

import (
	"errors"
	"testing"
	"time"
)

func TestPluginMemoriesAreScopedSupersededAndBounded(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	add := func(id, plugin, text string, temp bool) error {
		return s.PluginMemoryAdd(PluginMemory{ID: id, PluginID: plugin, Text: text, Project: "p1", Temp: temp, CreatedAt: now}, 2)
	}
	if err := add("m1", "id.a", "first memory of plugin a", false); err != nil {
		t.Fatal(err)
	}
	if err := add("m2", "id.a", "second memory of plugin a", false); err != nil {
		t.Fatal(err)
	}
	if err := add("m3", "id.a", "third, over the ceiling", false); !errors.Is(err, ErrPluginMemoryQuota) {
		t.Fatalf("the ceiling must hold: %v", err)
	}
	if err := add("b1", "id.b", "plugin b's own", false); err != nil {
		t.Fatalf("another plugin's ceiling is its own: %v", err)
	}
	// .
	if _, found, err := s.PluginMemoryGet("id.b", "m1"); err != nil || found {
		t.Fatalf("plugin b read plugin a's memory: found=%v err=%v", found, err)
	}
	m, found, err := s.PluginMemoryGet("id.a", "m1")
	if err != nil || !found || m.Text != "first memory of plugin a" || m.Project != "p1" || m.Attribution != "plugin" || !m.CreatedAt.Equal(now) {
		t.Fatalf("m1 = %+v found=%v err=%v", m, found, err)
	}
	// .
	if n := countRows(t, s, pmFTS, "second"); n != 1 {
		t.Fatalf("the sidecar does not hold the memory: %d", n)
	}
	// .
	if err := s.PluginMemorySupersede("id.a", "m1", "m2"); err != nil {
		t.Fatal(err)
	}
	if err := add("m3", "id.a", "third, now within the ceiling", false); err != nil {
		t.Fatalf("a superseded memory must not count: %v", err)
	}
	old, _, _ := s.PluginMemoryGet("id.a", "m1")
	if old.SupersededBy != "m2" {
		t.Fatalf("m1 is not marked superseded: %+v", old)
	}
	if err := s.PluginMemorySupersede("id.a", "m1", "m3"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("a superseded memory cannot be superseded again: %v", err)
	}
	if err := s.PluginMemorySupersede("id.b", "m2", "b1"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("plugin b superseded plugin a's memory: %v", err)
	}
	if err := s.PluginMemorySupersede("id.a", "m2", "nothing"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("a successor that does not exist was accepted: %v", err)
	}
	if n, _ := s.PluginMemoryCount("id.a"); n != 2 {
		t.Fatalf("current memories of a = %d, want 2", n)
	}
}

func TestTempPluginMemoriesDieWithTheActivation(t *testing.T) {
	s := testStore(t)
	if err := s.PluginMemoryAdd(PluginMemory{ID: "t1", PluginID: "id.t0", Text: "an uncertified plugin's note", Temp: true}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginMemoryAdd(PluginMemory{ID: "k1", PluginID: "id.t0", Text: "a kept note", Temp: false}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryAccess(time.Now(), MemoryRef{Store: "plugin_memories", ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginMemoryClearTemp("id.t0"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.PluginMemoryGet("id.t0", "t1"); found {
		t.Fatal("the temp memory survived the clear")
	}
	if _, found, _ := s.PluginMemoryGet("id.t0", "k1"); !found {
		t.Fatal("the persistent memory was cleared")
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "t1"}); ok {
		t.Fatal("the temp memory's access record survived the clear")
	}
	if n := countRows(t, s, pmFTS, "uncertified"); n != 0 {
		t.Fatalf("the sidecar still holds the cleared memory: %d", n)
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("sidecars disagree after the clear: %v", failed)
	}
}
