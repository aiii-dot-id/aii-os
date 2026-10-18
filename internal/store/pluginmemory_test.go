package store

import (
	"errors"
	"testing"
	"time"
)

func TestPluginMemoriesAreScopedSupersededAndBounded(t *testing.T) {
	s := testStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	replace := func(id, plugin, text, supersedes string) error {
		return s.PluginMemoryAdd(PluginScope{PluginID: plugin}, PluginMemory{ID: id, Text: text, Project: "p1", CreatedAt: now}, 2, supersedes)
	}
	add := func(id, plugin, text string, temp bool) error {
		return s.PluginMemoryAdd(PluginScope{PluginID: plugin}, PluginMemory{ID: id, Text: text, Project: "p1", Temp: temp, CreatedAt: now}, 2, "")
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
	// Scoped reads: b cannot see a's memory by id.
	if _, found, err := s.PluginMemoryGet(PluginScope{PluginID: "id.b"}, "m1"); err != nil || found {
		t.Fatalf("plugin b read plugin a's memory: found=%v err=%v", found, err)
	}
	m, found, err := s.PluginMemoryGet(PluginScope{PluginID: "id.a"}, "m1")
	if err != nil || !found || m.Text != "first memory of plugin a" || m.Project != "p1" || m.Attribution != "plugin" || !m.CreatedAt.Equal(now) {
		t.Fatalf("m1 = %+v found=%v err=%v", m, found, err)
	}
	// The sidecars followed the write.
	if n := countRows(t, s, pmFTS, "second"); n != 1 {
		t.Fatalf("the sidecar does not hold the memory: %d", n)
	}

	if err := replace("m3", "id.a", "third, replacing the first", "m1"); err != nil {
		t.Fatalf("a replacement must not count against the ceiling: %v", err)
	}
	old, _, _ := s.PluginMemoryGet(PluginScope{PluginID: "id.a"}, "m1")
	if old.SupersededBy != "m3" {
		t.Fatalf("m1 is not marked superseded: %+v", old)
	}
	if err := replace("m4", "id.a", "a second replacement of the first", "m1"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("a superseded memory cannot be superseded again: %v", err)
	}
	if err := replace("b2", "id.b", "b replacing a's", "m2"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("plugin b superseded plugin a's memory: %v", err)
	}
	if err := replace("m2", "id.a", "a memory replacing itself", "m2"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("a memory that replaces itself was accepted: %v", err)
	}
	if n, _ := s.PluginMemoryCount(PluginScope{PluginID: "id.a"}); n != 2 {
		t.Fatalf("current memories of a = %d, want 2", n)
	}
}

func TestTempPluginMemoriesDieWithTheActivation(t *testing.T) {
	s := testStore(t)
	if err := s.PluginMemoryAdd(PluginScope{PluginID: "id.t0"}, PluginMemory{ID: "t1", Text: "an uncertified plugin's note", Temp: true}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginMemoryAdd(PluginScope{PluginID: "id.t0"}, PluginMemory{ID: "k1", Text: "a kept note", Temp: false}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryAccess(time.Now(), MemoryRef{Store: "plugin_memories", ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "id.t0"}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.PluginMemoryGet(PluginScope{PluginID: "id.t0"}, "t1"); found {
		t.Fatal("the temp memory survived the clear")
	}
	if _, found, _ := s.PluginMemoryGet(PluginScope{PluginID: "id.t0"}, "k1"); !found {
		t.Fatal("the persistent memory was cleared")
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "t1"}); ok {
		t.Fatal("the temp memory's access record survived the clear")
	}
	if n := countRows(t, s, pmFTS, "uncertified"); n != 0 {
		t.Fatalf("the sidecar still holds the cleared memory: %d", n)
	}
	if failed := checkSidecarsOn(s.db); len(failed) != 0 {
		t.Fatalf("sidecars disagree after the clear: %v", failed)
	}
}
