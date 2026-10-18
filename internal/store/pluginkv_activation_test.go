package store

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func kvScopedValue(t *testing.T, s *Store, scope PluginScope, key, want string, found bool) {
	t.Helper()
	value, gotFound, err := s.PluginKVGet(scope, key)
	if err != nil || gotFound != found || value != want {
		t.Fatalf("get %v %q = %q, %v, %v; want %q, %v", scope, key, value, gotFound, err, want, found)
	}
}

func TestPluginKVActivationOverlay(t *testing.T) {
	s := testStore(t)
	a := PluginScope{PluginID: "p", Activation: "a"}
	b := PluginScope{PluginID: "p", Activation: "b"}
	other := PluginScope{PluginID: "other", Activation: "a"}
	for _, key := range []string{"mem:1", "mem:2", "mem:3", "outside"} {
		if err := s.PluginKVPut(PluginScope{PluginID: "p"}, key, "persistent", false, 10, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PluginKVPut(other, "mem:1", "other", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	before := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv WHERE plugin_id='p' AND temp=0 ORDER BY key`)
	for _, scope := range []PluginScope{a, b} {
		kvScopedValue(t, s, scope, "mem:1", "persistent", true)
		if err := s.PluginKVPut(scope, "mem:1", scope.Activation, true, 10, 1000); err != nil {
			t.Fatal(err)
		}
	}
	kvScopedValue(t, s, a, "mem:1", "a", true)
	kvScopedValue(t, s, b, "mem:1", "b", true)
	kvScopedValue(t, s, PluginScope{PluginID: "p"}, "mem:1", "persistent", true)
	for _, key := range []string{"mem:1", "mem:2"} {
		if deleted, err := s.PluginKVDelete(a, key, true); err != nil || !deleted {
			t.Fatalf("delete %q: %v, %v", key, deleted, err)
		}
		kvScopedValue(t, s, a, key, "", false)
		if deleted, err := s.PluginKVDelete(a, key, true); err != nil || deleted {
			t.Fatalf("repeat delete %q: %v, %v", key, deleted, err)
		}
	}
	if deleted, err := s.PluginKVDelete(a, "absent", true); err != nil || deleted {
		t.Fatalf("absent delete = %v, %v", deleted, err)
	}
	if keys, more, err := s.PluginKVList(a, "mem:", 1); err != nil || more || !reflect.DeepEqual(keys, []string{"mem:3"}) {
		t.Fatalf("hidden list = %v, %v, %v", keys, more, err)
	}
	if keys, more, err := s.PluginKVList(b, "mem:", 2); err != nil || !more || !reflect.DeepEqual(keys, []string{"mem:1", "mem:2"}) {
		t.Fatalf("bounded deduplicated list = %v, %v, %v", keys, more, err)
	}
	if keys, more, err := s.PluginKVList(b, "mem:", 10); err != nil || more || !reflect.DeepEqual(keys, []string{"mem:1", "mem:2", "mem:3"}) {
		t.Fatalf("inherited list = %v, %v, %v", keys, more, err)
	}
	if err := s.PluginKVPut(a, "mem:2", "restored local", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	kvScopedValue(t, s, a, "mem:2", "restored local", true)
	if err := s.PluginKVPut(a, "local only", "temporary", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	kvScopedValue(t, s, b, "local only", "", false)
	if deleted, err := s.PluginKVDelete(a, "local only", true); err != nil || !deleted {
		t.Fatalf("delete local-only key = %v, %v", deleted, err)
	}
	if err := s.PluginKVClearTemp(a); err != nil {
		t.Fatal(err)
	}
	kvScopedValue(t, s, a, "local only", "", false)
	for _, key := range []string{"mem:1", "mem:2"} {
		kvScopedValue(t, s, a, key, "persistent", true)
	}
	kvScopedValue(t, s, b, "mem:1", "b", true)
	kvScopedValue(t, s, other, "mem:1", "other", true)
	after := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv WHERE plugin_id='p' AND temp=0 ORDER BY key`)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("temporary edits changed persistent rows: before=%v after=%v", before, after)
	}
	if err := s.PluginKVPut(a, "durable", "shared", false, 10, 1000); err != nil {
		t.Fatal(err)
	}
	kvScopedValue(t, s, b, "durable", "shared", true)
	if rows := startupQueryRows(t, s, `SELECT activation,hidden,temp FROM plugin_kv WHERE key='durable'`); !reflect.DeepEqual(rows, [][]any{{"", nil, int64(0)}}) {
		t.Fatalf("persistent row compatibility = %v", rows)
	}
	if deleted, err := s.PluginKVDelete(b, "durable", false); err != nil || !deleted {
		t.Fatalf("durable delete = %v, %v", deleted, err)
	}
	kvScopedValue(t, s, a, "durable", "", false)
}

func TestPluginKVActivationQuota(t *testing.T) {
	s := testStore(t)
	a := PluginScope{PluginID: "p", Activation: "a"}
	b := PluginScope{PluginID: "p", Activation: "b"}
	if err := s.PluginKVPut(PluginScope{PluginID: "p"}, "é", "12", false, 1, 4); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []PluginScope{a, b} {
		if err := s.PluginKVPut(scope, "é", "ab", true, 1, 4); err != nil {
			t.Fatalf("same key replacement double charged: %v", err)
		}
	}
	for _, tc := range []struct {
		key, value  string
		keys, bytes int
	}{{"new", "", 1, 100}, {"é", "abc", 1, 4}} {
		before := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv ORDER BY plugin_id,activation,key`)
		if err := s.PluginKVPut(a, tc.key, tc.value, true, tc.keys, tc.bytes); !errors.Is(err, ErrPluginKVQuota) {
			t.Fatalf("quota refusal = %v", err)
		}
		if after := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv ORDER BY plugin_id,activation,key`); !reflect.DeepEqual(before, after) {
			t.Fatal("quota failure changed rows")
		}
	}
	if _, err := s.PluginKVDelete(a, "é", true); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(a, "new", "", true, 1, 100); !errors.Is(err, ErrPluginKVQuota) {
		t.Fatalf("hidden key escaped key quota: %v", err)
	}
	if err := s.PluginKVPut(a, "x", "", true, 2, 2); !errors.Is(err, ErrPluginKVQuota) {
		t.Fatalf("hidden UTF-8 key escaped byte quota: %v", err)
	}
	if err := s.PluginKVPut(a, "é", "", true, 1, 2); err != nil {
		t.Fatalf("replace marker at exact quota: %v", err)
	}
	kvScopedValue(t, s, a, "é", "", true)
	kvScopedValue(t, s, b, "é", "ab", true)
	kvScopedValue(t, s, PluginScope{PluginID: "p"}, "é", "12", true)
}

func TestPluginKVActivationLocalDeleteReleasesQuota(t *testing.T) {
	s := testStore(t)
	scope := PluginScope{PluginID: "p", Activation: "a"}
	if err := s.PluginKVPut(scope, "a", "v", true, 1, 2); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.PluginKVDelete(scope, "a", true); err != nil || !deleted {
		t.Fatalf("delete local-only a = %v, %v", deleted, err)
	}
	if err := s.PluginKVPut(scope, "b", "v", true, 1, 2); err != nil {
		t.Fatalf("deleted local-only key retained quota: %v", err)
	}
	kvScopedValue(t, s, scope, "a", "", false)
	kvScopedValue(t, s, scope, "b", "v", true)
	if keys, more, err := s.PluginKVList(scope, "", 1); err != nil || more || !reflect.DeepEqual(keys, []string{"b"}) {
		t.Fatalf("reused quota list = %v, %v, %v", keys, more, err)
	}
}

func TestPluginKVActivationFailuresAndSAFE(t *testing.T) {
	s := testStore(t)
	a := PluginScope{PluginID: "p", Activation: "a"}
	if err := s.PluginKVPut(PluginScope{PluginID: "p"}, "key", "persistent", false, 10, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(a, "key", "local", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name  string
		write func() error
	}{
		{"put", func() error { return s.PluginKVPut(a, "key", "changed", true, 10, 1000) }},
		{"delete", func() error { _, err := s.PluginKVDelete(a, "key", true); return err }},
		{"clear", func() error { return s.PluginKVClearTemp(a) }},
	}
	before := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv ORDER BY activation,key`)
	s.SetFrozen(true, "KV test")
	for _, mutation := range mutations {
		if err := mutation.write(); !errors.As(err, new(*FrozenError)) {
			t.Fatalf("SAFE %s = %v", mutation.name, err)
		}
	}
	kvScopedValue(t, s, a, "key", "local", true)
	if keys, _, err := s.PluginKVList(a, "", 10); err != nil || !reflect.DeepEqual(keys, []string{"key"}) {
		t.Fatalf("SAFE list = %v, %v", keys, err)
	}
	s.SetFrozen(false, "")
	for _, statement := range []string{
		`CREATE TRIGGER kv_fail_insert BEFORE INSERT ON plugin_kv BEGIN SELECT RAISE(ABORT,'injected'); END`,
		`CREATE TRIGGER kv_fail_delete BEFORE DELETE ON plugin_kv BEGIN SELECT RAISE(ABORT,'injected'); END`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutation := range mutations {
		if err := mutation.write(); err == nil {
			t.Fatalf("injected %s failure returned success", mutation.name)
		}
		if after := startupQueryRows(t, s, `SELECT rowid,* FROM plugin_kv ORDER BY activation,key`); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s failure changed rows: %v", mutation.name, after)
		}
	}
}

func TestPluginKVActivationConcurrentQuota(t *testing.T) {
	s := testStore(t)
	a := PluginScope{PluginID: "p", Activation: "a"}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.PluginKVPut(a, key, "v", true, 1, 10)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, refused := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrPluginKVQuota):
			refused++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("concurrent quota accepted=%d refused=%d", accepted, refused)
	}
}
