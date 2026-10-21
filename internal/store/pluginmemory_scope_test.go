package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPluginMemoryActivationReplacementViews(t *testing.T) {
	s := testStore(t)
	a, b, shared := PluginScope{"p", "a"}, PluginScope{"p", "b"}, PluginScope{"p", ""}
	stamp := time.Date(2026, 9, 20, 1, 2, 3, 0, time.UTC)
	add := func(scope PluginScope, id, replaces string, temp bool) {
		t.Helper()
		// The ceiling is of every row an activation's view holds, the
		// replaced ones with the current: three is the most this test's
		// views ever hold (root, a1, a2).
		if err := s.PluginMemoryAdd(scope, PluginMemory{ID: id, Text: "memory " + id, Temp: temp, CreatedAt: stamp}, 3, replaces); err != nil {
			t.Fatal(err)
		}
	}
	get := func(scope PluginScope, id, successor string) {
		t.Helper()
		m, found, err := s.PluginMemoryGet(scope, id)
		if err != nil || !found || m.SupersededBy != successor {
			t.Fatalf("scope=%+v id=%s: %+v found=%v err=%v; want successor %s", scope, id, m, found, err, successor)
		}
	}
	add(shared, "root", "", false)
	add(a, "a1", "root", true)
	get(a, "root", "a1")
	get(b, "root", "")
	add(b, "b1", "root", true)
	add(a, "a2", "a1", true)
	get(a, "root", "a2")
	get(b, "root", "b1")
	get(shared, "root", "")
	if _, found, err := s.PluginMemoryGet(a, "b1"); err != nil || found {
		t.Fatalf("another activation leaked: %v %v", found, err)
	}
	var rawLink, activation string
	var overrides any
	var updated string
	if err := s.db.QueryRow(`SELECT COALESCE(superseded_by,''), activation, overrides, updated_at FROM plugin_memories WHERE id='root'`).Scan(&rawLink, &activation, &overrides, &updated); err != nil {
		t.Fatal(err)
	}
	if rawLink != "" || activation != "" || overrides != nil || updated != stamp.Format(time.RFC3339Nano) {
		t.Fatalf("local replacement mutated persistent data: %q %q %v %q", rawLink, activation, overrides, updated)
	}
	if err := s.PluginMemoryClearTemp(a); err != nil {
		t.Fatal(err)
	}
	get(a, "root", "")
	get(b, "root", "b1")
	add(a, "a3", "root", true)
	add(shared, "permanent", "root", false)
	get(a, "a3", "permanent")
	get(b, "b1", "permanent")
	for _, scope := range []PluginScope{a, b, shared} {
		get(scope, "root", "permanent")
		if n, err := s.PluginMemoryCount(scope); err != nil || n != 1 {
			t.Fatalf("count %+v: %d %v", scope, n, err)
		}
		if err := s.PluginMemoryClearTemp(scope); err != nil {
			t.Fatal(err)
		}
	}
	get(shared, "root", "permanent")
	if bad := checkSidecarsOn(s.db); len(bad) != 0 {
		t.Fatal(bad)
	}
}

func TestPluginMemoryScopedAtomicRollbackQuotaAndSAFE(t *testing.T) {
	s := testStore(t)
	a, b := PluginScope{"p", "a"}, PluginScope{"p", "b"}
	// A replacement holds two rows, the replaced one and its successor:
	// a new memory is held to one row here, a replacement to two.
	add := func(scope PluginScope, id, old string) error {
		ceiling := 1
		if old != "" {
			ceiling = 2
		}
		return s.PluginMemoryAdd(scope, PluginMemory{ID: id, Text: id, Temp: true}, ceiling, old)
	}
	if err := add(a, "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := add(b, "b", ""); err != nil {
		t.Fatal(err)
	}
	if err := add(a, "extra", ""); !errors.Is(err, ErrPluginMemoryQuota) {
		t.Fatalf("quota: %v", err)
	}
	if err := add(a, "foreign", "b"); !errors.Is(err, ErrPluginMemoryNotFound) {
		t.Fatalf("foreign replacement: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_memory_link BEFORE UPDATE OF superseded_by ON plugin_memories BEGIN SELECT RAISE(ABORT, 'injected link failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := add(a, "failed", "a"); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, found, err := s.PluginMemoryGet(a, "failed"); err != nil || found {
		t.Fatalf("insert survived rollback: %v %v", found, err)
	}
	if m, _, err := s.PluginMemoryGet(a, "a"); err != nil || m.SupersededBy != "" {
		t.Fatalf("predecessor changed: %+v %v", m, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_memory_link`); err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "test")
	for _, err := range []error{add(a, "frozen", "a"), s.PluginMemoryClearTemp(a)} {
		var frozen *FrozenError
		if !errors.As(err, &frozen) {
			t.Fatalf("SAFE bypass: %v", err)
		}
	}
	if _, found, err := s.PluginMemoryGet(a, "a"); err != nil || !found {
		t.Fatalf("SAFE read: %v %v", found, err)
	}
	s.SetFrozen(false, "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"winner1", "winner2"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); results <- add(a, id, "a") }(id)
	}
	wg.Wait()
	close(results)
	winners, stale := 0, 0
	for err := range results {
		if err == nil {
			winners++
		} else if errors.Is(err, ErrPluginMemoryNotFound) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("replacement admission: winners=%d stale=%d", winners, stale)
	}
}
