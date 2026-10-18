package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestColdStartupRecoversExpiredPluginShapeWithoutSafeOrHistoryLoss(t *testing.T) {
	dir := t.TempDir()
	key, record, database := birthFixture(t, dir, "ScopedRecovery")
	buildPriorProjection(t, record, database)
	s, err := store.NewForTest(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(store.PluginScope{PluginID: "p"}, "persistent", "kept", false, 10, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(store.PluginScope{PluginID: "p"}, "temporary", "expired", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	// Simulate data written by a prospective activation-scoped release.
	// No production schema introduces this field in this compatibility stage.
	if _, err := s.DB().Exec(`ALTER TABLE plugin_kv ADD COLUMN future_scope TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE plugin_kv SET future_scope='ended activation' WHERE temp=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	cfg := safebootConfig(t, dir, "ScopedRecovery", key, record, database)
	for i := 0; i < 2; i++ {
		a := New(cfg)
		t.Cleanup(func() { stopApp(t, a) })
		if err := startLiveForTest(a); err != nil {
			t.Fatalf("boot %d: %v", i, err)
		}
		if reason, safe := a.SafeMode(); safe || !a.live.Load() {
			t.Fatalf("intact identity was stranded in SAFE: %s", reason)
		}
		if v, ok, err := a.store.PluginKVGet(store.PluginScope{PluginID: "p"}, "persistent"); err != nil || !ok || v != "kept" {
			t.Fatalf("persistent state changed: %q %v %v", v, ok, err)
		}
		if _, ok, err := a.store.PluginKVGet(store.PluginScope{PluginID: "p"}, "temporary"); err != nil || ok {
			t.Fatalf("expired scoped state survived: %v %v", ok, err)
		}
		stopApp(t, a)
	}
	after, err := os.ReadFile(record)
	if err != nil || !bytes.HasPrefix(after, before) {
		t.Fatalf("schema recovery rewrote the signed prefix: %v", err)
	}
}

func TestDamagedRecordNeverAuthorizesStartupTemporaryRetirement(t *testing.T) {
	dir := t.TempDir()
	key, record, database := birthFixture(t, dir, "ScopedSafe")
	buildPriorProjection(t, record, database)
	s, err := store.NewForTest(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(store.PluginScope{PluginID: "p"}, "temporary", "forensic state", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE plugin_kv ADD COLUMN future_scope TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE plugin_kv SET future_scope='ended activation' WHERE temp=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	tamperChain(t, key, record)
	before, stamp := fileDigest(t, database)
	a := New(safebootConfig(t, dir, "ScopedSafe", key, record, database))
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.startLive(); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("damaged record did not enter existing SAFE path")
	}
	stopApp(t, a)
	after, again := fileDigest(t, database)
	if before != after || !stamp.Equal(again) {
		t.Fatal("SAFE startup changed the prior database")
	}
}
