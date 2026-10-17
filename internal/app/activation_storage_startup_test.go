package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func lifetimeCall(t *testing.T, b *broker.Binding, operation string, target, arguments any, wantStatus string) json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]any{"operation": operation, "target": target, "arguments": arguments})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := b.Dispatch(context.Background(), "invoke-call", params)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"operation_result"`
	}
	if err := json.Unmarshal(reply, &result); err != nil || result.Status != wantStatus {
		t.Fatalf("%s = %s, %v; want %s", operation, reply, err, wantStatus)
	}
	return result.Result
}

func lifetimeHost(t *testing.T, s *store.Store) *broker.Host {
	t.Helper()
	h, err := broker.New(broker.Config{Store: s, Grants: map[string]broker.Grant{
		"p": {KV: true, Memory: true}, "legacy": {KV: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func lifetimeBinding(h *broker.Host, root string, tier packagefmt.Tier) *broker.Binding {
	b := h.Bind("p", tier, []string{"ring4.kv", "ring4.memory", "fs.private"})
	b.SetFiles(filepath.Join(root, "p"), 0)
	return b
}

type lifetimeFixture struct {
	cfg                           *Config
	root, orphan, temporaryMemory string
	persistent                    store.PluginMemory
	files                         map[string]string
}

// The dead binding has completed every call. Closing only its store models a
// process death: the old binding is deliberately never called or closed again.
func seedDeadActivation(t *testing.T) lifetimeFixture {
	t.Helper()
	dir := t.TempDir()
	key, record, database := birthFixture(t, dir, "ActivationLifetime")
	buildPriorProjection(t, record, database)
	f := lifetimeFixture{cfg: safebootConfig(t, dir, "ActivationLifetime", key, record, database)}
	f.root = filepath.Join(identityDataDir(record), "plugins-data")
	s, err := store.NewForTest(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := lifetimeHost(t, s)
	durable := lifetimeBinding(h, f.root, packagefmt.TierT1)
	lifetimeCall(t, durable, "kv.put", map[string]any{"key": "shared"}, map[string]any{"value": "persistent"}, "succeeded")
	raw := lifetimeCall(t, durable, "memory.remember", nil, map[string]any{"text": "The operator prefers concise lifetime reports."}, "succeeded")
	var remembered struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &remembered); err != nil || remembered.ID == "" {
		t.Fatalf("persistent memory = %s, %v", raw, err)
	}
	f.persistent, _, err = s.PluginMemoryGet(store.PluginScope{PluginID: "p"}, remembered.ID)
	if err != nil {
		t.Fatal(err)
	}
	lifetimeCall(t, durable, "fs.write", map[string]any{"root": "private", "path": "persistent.txt"}, map[string]any{"data": "persistent file"}, "succeeded")
	if err := durable.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := h.Bind("legacy", packagefmt.TierT1, []string{"fs.private"})
	legacy.SetFiles(filepath.Join(f.root, "legacy"), 0)
	lifetimeCall(t, legacy, "fs.write", map[string]any{"root": "private", "path": "old.txt"}, map[string]any{"data": "unlabelled legacy file"}, "succeeded")
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	f.files = map[string]string{
		filepath.Join(f.root, "p", "persistent.txt"): "persistent file",
		filepath.Join(f.root, "legacy", "old.txt"):   "unlabelled legacy file",
	}
	dead := lifetimeBinding(h, f.root, packagefmt.TierT0)
	lifetimeCall(t, dead, "kv.put", map[string]any{"key": "shared"}, map[string]any{"value": "temporary override"}, "succeeded")
	lifetimeCall(t, dead, "kv.put", map[string]any{"key": "dead-only"}, map[string]any{"value": "temporary"}, "succeeded")
	raw = lifetimeCall(t, dead, "memory.remember", nil, map[string]any{"text": "Reports should include every temporary detail.", "supersedes": f.persistent.ID}, "succeeded")
	if err := json.Unmarshal(raw, &remembered); err != nil || remembered.ID == "" || remembered.ID == f.persistent.ID {
		t.Fatalf("temporary replacement = %s, %v", raw, err)
	}
	f.temporaryMemory = remembered.ID
	for table, want := range map[string]int{"plugin_kv": 2, "plugin_memories": 1} {
		var n int
		if err := s.DB().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE plugin_id='p' AND temp=1 AND activation<>''").Scan(&n); err != nil || n != want {
			t.Fatalf("fixture did not create scoped temporary %s: %d %v", table, n, err)
		}
	}
	lifetimeCall(t, dead, "fs.write", map[string]any{"root": "private", "path": "orphan.txt"}, map[string]any{"data": "dead temporary file"}, "succeeded")
	if err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == "orphan.txt" {
			if f.orphan != "" {
				return fmt.Errorf("multiple orphan fixture files")
			}
			f.orphan = path
		}
		return err
	}); err != nil || f.orphan == "" {
		t.Fatalf("discover binding-created temporary file: %q %v", f.orphan, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return f
}

func lifetimeFilesKept(t *testing.T, files map[string]string) {
	t.Helper()
	for path, want := range files {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatalf("preserved file %s = %q, %v", path, data, err)
		}
	}
}

func TestAppActivationStorageColdRestart(t *testing.T) {
	for _, cleanupFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_failure=%v", cleanupFailure), func(t *testing.T) {
			f := seedDeadActivation(t)
			if cleanupFailure {
				// An unrecognized entry is retained and makes the real cleanup
				// return an error, while recognized dead activations can retire.
				unknown := filepath.Join(filepath.Dir(filepath.Dir(f.orphan)), "unrecognized.txt")
				if err := os.WriteFile(unknown, []byte("keep unknown"), 0o600); err != nil {
					t.Fatal(err)
				}
				f.files[unknown] = "keep unknown"
			}
			for boot := 0; boot < 2; boot++ {
				a := New(f.cfg)
				t.Cleanup(func() { stopApp(t, a) })
				if err := startLiveForTest(a); err != nil {
					t.Fatal(err)
				}
				if reason, safe := a.SafeMode(); safe || !a.live.Load() {
					t.Fatalf("boot %d stranded healthy identity: %q", boot, reason)
				}
				for _, table := range []string{"plugin_kv", "plugin_memories"} {
					var n int
					if err := a.store.DB().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE plugin_id='p' AND temp=1").Scan(&n); err != nil || n != 0 {
						t.Fatalf("boot %d retained temporary %s: %d %v", boot, table, n, err)
					}
				}
				if got, found, err := a.store.PluginMemoryGet(store.PluginScope{PluginID: "p"}, f.persistent.ID); err != nil || !found || !reflect.DeepEqual(got, f.persistent) {
					t.Fatalf("persistent memory changed: %+v %v %v", got, found, err)
				}
				if _, err := os.Stat(f.orphan); !os.IsNotExist(err) {
					t.Fatalf("dead files survived: %v", err)
				}
				entries, err := os.ReadDir(filepath.Dir(filepath.Dir(f.orphan)))
				wantEntries := 0
				if cleanupFailure {
					wantEntries = 1
				}
				if err != nil || len(entries) != wantEntries {
					t.Fatalf("boot %d retained activation directories: %v %v", boot, entries, err)
				}
				lifetimeFilesKept(t, f.files)
				if cleanupFailure && broker.ClearOrphanTempFiles(f.root) == nil {
					t.Fatal("failure fixture no longer refuses unrecognized entry")
				}
				fresh := lifetimeBinding(lifetimeHost(t, a.store), f.root, packagefmt.TierT0)
				raw := lifetimeCall(t, fresh, "kv.get", map[string]any{"key": "shared"}, nil, "succeeded")
				var kv struct {
					Value string `json:"value"`
				}
				if err := json.Unmarshal(raw, &kv); err != nil || kv.Value != "persistent" {
					t.Fatalf("new activation inheritance: %s %v", raw, err)
				}
				lifetimeCall(t, fresh, "kv.get", map[string]any{"key": "dead-only"}, nil, "failed")
				lifetimeCall(t, fresh, "memory.recall", nil, map[string]any{"id": f.temporaryMemory}, "failed")
				lifetimeCall(t, fresh, "fs.read", map[string]any{"root": "private", "path": "orphan.txt"}, nil, "failed")
				lifetimeCall(t, fresh, "fs.read", map[string]any{"root": "private", "path": "persistent.txt"}, nil, "failed")
				lifetimeCall(t, fresh, "kv.put", map[string]any{"key": "new"}, map[string]any{"value": "works"}, "succeeded")
				raw = lifetimeCall(t, fresh, "kv.get", map[string]any{"key": "new"}, nil, "succeeded")
				if err := json.Unmarshal(raw, &kv); err != nil || kv.Value != "works" {
					t.Fatalf("new activation KV readback: %s %v", raw, err)
				}
				raw = lifetimeCall(t, fresh, "memory.remember", nil, map[string]any{"text": "Fresh activation writes remain operational."}, "succeeded")
				var memory struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &memory); err != nil || memory.ID == "" {
					t.Fatalf("new activation memory: %s %v", raw, err)
				}
				lifetimeCall(t, fresh, "memory.recall", nil, map[string]any{"id": memory.ID}, "succeeded")
				lifetimeCall(t, fresh, "fs.write", map[string]any{"root": "private", "path": "fresh.txt"}, map[string]any{"data": "works"}, "succeeded")
				raw = lifetimeCall(t, fresh, "fs.read", map[string]any{"root": "private", "path": "fresh.txt"}, nil, "succeeded")
				var file struct {
					Data string `json:"data_b64"`
				}
				if err := json.Unmarshal(raw, &file); err != nil || file.Data != base64.StdEncoding.EncodeToString([]byte("works")) {
					t.Fatalf("new activation file readback: %s %v", raw, err)
				}
				// The first new binding also ends by process death. App owns
				// its store, but this fixture binding is deliberately abandoned
				// after its calls finish, so the second boot must retire it.
				if boot == 1 {
					if err := fresh.Close(); err != nil {
						t.Fatal(err)
					}
				}
				stopApp(t, a)
			}
		})
	}
}

func TestAppActivationStorageSafeStartupPreservesForensics(t *testing.T) {
	f := seedDeadActivation(t)
	tamperChain(t, f.cfg.Identity.KeyPath, f.cfg.Identity.LedgerPath)
	before, stamp := fileDigest(t, f.cfg.Identity.DBPath)
	orphanBefore, orphanStamp := fileDigest(t, f.orphan)
	a := New(f.cfg)
	t.Cleanup(func() { stopApp(t, a) })
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe || a.live.Load() {
		t.Fatal("tampered ledger did not remain in minimal SAFE")
	}
	for _, table := range []string{"plugin_kv", "plugin_memories"} {
		var n int
		if err := a.store.DB().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE plugin_id='p' AND temp=1").Scan(&n); err != nil || n == 0 {
			t.Fatalf("SAFE lost forensic %s: %d %v", table, n, err)
		}
	}
	if err := a.store.PluginKVPut(store.PluginScope{PluginID: "p"}, "forbidden", "write", false, 10, 1000); err == nil {
		t.Fatal("SAFE store accepted a write")
	}
	stopApp(t, a)
	after, again := fileDigest(t, f.cfg.Identity.DBPath)
	if before != after || !stamp.Equal(again) {
		t.Fatal("SAFE startup changed prior database bytes or timestamp")
	}
	orphanAfter, orphanAgain := fileDigest(t, f.orphan)
	if orphanBefore != orphanAfter || !orphanStamp.Equal(orphanAgain) {
		t.Fatal("SAFE startup changed orphan evidence")
	}
	lifetimeFilesKept(t, f.files)
}
