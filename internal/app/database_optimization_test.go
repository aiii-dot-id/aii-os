package app

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
)

func TestDatabaseOptimizationPreferencesAreIndependentAndDurable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.DBPath = filepath.Join(dir, "identity.db")
	a := New(cfg)
	for _, pair := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}, {false, false}} {
		state, err := a.applyConfigChange(map[string]any{"identity.db_optimize_levels": pair[0], "identity.db_learn_dictionary": pair[1]})
		if err != nil {
			t.Fatal(err)
		}
		if state.Database.OptimizeLevels != pair[0] || state.Database.LearnDictionary != pair[1] {
			t.Fatalf("readback: %+v", state.Database)
		}
		loaded, err := LoadConfig(cfg.SourcePath)
		if err != nil || loaded.Identity.DBOptimizeLevels != pair[0] || loaded.Identity.DBLearnDictionary != pair[1] {
			t.Fatalf("persisted preferences: %v", err)
		}
	}
	for _, key := range []string{"identity.db_optimize_levels", "identity.db_learn_dictionary"} {
		for _, bad := range []any{"true", 1, nil} {
			if _, err := a.applyConfigChange(map[string]any{key: bad}); err == nil {
				t.Fatalf("bad preference accepted: %s=%v", key, bad)
			}
		}
	}
	failure := errors.New("injected preference save failure")
	if _, err := a.applyConfigChangeWith(map[string]any{"identity.db_learn_dictionary": true}, func(*Config) (bool, error) { return false, failure }); !errors.Is(err, failure) {
		t.Fatalf("save failure: %v", err)
	}
	if a.configSnapshot().Identity.DBLearnDictionary {
		t.Fatal("failed save enabled learning")
	}
}

func TestDatabaseOptimizationMaintenanceAndRestart(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	key, record, path := birthFixture(t, dir, "StorageOptimization")
	cfg := safebootConfig(t, dir, "StorageOptimization", key, record, path)
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.DBFormat = "zstd"
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer func() { stopApp(t, a) }()
	rng := rand.New(rand.NewPCG(73, 91))
	shared := make([]byte, 3000)
	for i := range shared {
		shared[i] = byte(33 + rng.IntN(90))
	}
	for row := 0; row < 256; row++ {
		b := make([]byte, 4000)
		copy(b, shared)
		for i := len(shared); i < len(b); i++ {
			b[i] = byte(33 + rng.IntN(90))
		}
		if _, err := a.store.DB().Exec("INSERT INTO plugin_kv(plugin_id,key,value,updated_at) VALUES('optimization',?,?,?)", fmt.Sprint(row), string(b), "2026-09-21T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	state, err := a.applyConfigChange(map[string]any{"identity.db_optimize_levels": true, "identity.db_learn_dictionary": true})
	if err != nil {
		t.Fatal(err)
	}
	if state.Database.DictionaryBytes == nil || *state.Database.DictionaryBytes != 0 {
		t.Fatal("preference save fabricated a dictionary")
	}
	maintenancePass(t, a)
	observed := a.databaseState()
	if observed.DictionaryBytes == nil || *observed.DictionaryBytes == 0 || observed.Notice != "" {
		t.Fatalf("maintenance did not activate a dictionary: %+v", observed)
	}
	if _, err := a.applyConfigChange(map[string]any{"identity.db_learn_dictionary": false}); err != nil {
		t.Fatal(err)
	}
	if after := a.databaseState(); after.LearnDictionary || after.DictionaryBytes == nil || *after.DictionaryBytes == 0 {
		t.Fatalf("disable lost decoding metadata: %+v", after)
	}
	stopApp(t, a)
	loaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	a = New(loaded)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("dictionary restart SAFE: %s", reason)
	}
	if after := a.databaseState(); after.DictionaryBytes == nil || *after.DictionaryBytes == 0 || after.LearnDictionary || !after.OptimizeLevels {
		t.Fatalf("restart lost observed dictionary or preference: %+v", after)
	}
	var rows int
	if err := a.store.DB().QueryRow("SELECT count(*) FROM plugin_kv WHERE plugin_id='optimization'").Scan(&rows); err != nil || rows != 256 {
		t.Fatalf("data lost: %d %v", rows, err)
	}
}
