package app

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDatabasePreferencePersistsWithoutHotSwap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
	a := New(cfg)
	st, err := store.New(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a.databaseView.Store(&databaseView{store: st, path: cfg.Identity.DBPath})
	for _, value := range []string{"zstd", "sqlite", ""} {
		state, err := a.applyConfigChange(map[string]interface{}{"identity.db_format": value})
		if err != nil {
			t.Fatal(err)
		}
		if state.Database.Preferred != value || state.Database.Active != "sqlite" || !slices.Contains(state.RestartRequired, "identity.db_format") {
			t.Fatalf("readback: %+v", state.Database)
		}
		loaded, err := LoadConfig(cfg.SourcePath)
		if err != nil || loaded.Identity.DBFormat != value {
			t.Fatalf("persistence: %v %v", loaded, err)
		}
	}
	for _, bad := range []any{"zip", true, nil} {
		if _, err := a.applyConfigChange(map[string]interface{}{"identity.db_format": bad}); err == nil {
			t.Fatalf("invalid preference accepted: %v", bad)
		}
	}
	if err := os.WriteFile(cfg.SourcePath, []byte(`{"identity":{"db_format":"zip"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(cfg.SourcePath); err == nil {
		t.Fatal("invalid persisted preference accepted")
	}
}

func TestDatabaseStartupAndExportJourney(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	t.Chdir(dir)
	key, record, dbPath := birthFixture(t, dir, "StorageJourney")
	cfg := safebootConfig(t, dir, "StorageJourney", key, record, dbPath)
	for _, format := range []string{"zstd", "sqlite"} {
		cfg.Identity.DBFormat = format
		a := New(cfg)
		if err := startLiveForTest(a); err != nil {
			t.Fatal(err)
		}
		func() {
			defer a.Stop()
			if reason, safe := a.SafeMode(); safe {
				t.Fatalf("boot SAFE: %s", reason)
			}
			state := a.databaseState()
			if state.Active != format || state.Preferred != format || !state.CanExport || state.Notice != "" {
				t.Fatalf("state %+v", state)
			}
			// .
			if a.timeFac == nil {
				t.Fatal("TIME missing after conversion")
			}
			if _, err := a.store.DB().Exec("INSERT INTO plugin_kv(plugin_id,key,value,updated_at) VALUES('storage-test',?,?,?)", format, "current-data", "2026-09-20T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, a.dashboard.LocalURL()+"/database/export", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", a.dashboard.LocalURL())
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			file, size := response.Body, response.ContentLength
			defer file.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("export HTTP status: %s", response.Status)
			}
			bytes, err := io.ReadAll(file)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(bytes)) != size || !strings.HasPrefix(string(bytes), "SQLite format 3\x00") {
				t.Fatal("export is not ordinary SQLite")
			}
			exported := filepath.Join(t.TempDir(), "download.db")
			if err := os.WriteFile(exported, bytes, 0600); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			plain, err := sql.Open("sqlite", exported)
			if err != nil {
				t.Fatal(err)
			}
			defer plain.Close()
			var got string
			if err := plain.QueryRow("SELECT value FROM plugin_kv WHERE plugin_id='storage-test' AND key=?", format).Scan(&got); err != nil || got != "current-data" {
				t.Fatalf("exported data: %q %v", got, err)
			}
			if a.databaseState().Active != format {
				t.Fatal("export changed running format")
			}
			left, err := filepath.Glob(filepath.Join(dir, ".db-convert-export-*"))
			if err != nil || len(left) != 0 {
				t.Fatalf("export material retained: %v %v", left, err)
			}
		}()
	}
}

func TestDatabasePreferenceDoesNotConvertRejectedBoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	key, record, dbPath := birthFixture(t, dir, "StorageSafe")
	buildPriorProjection(t, record, dbPath)
	tamperChain(t, key, record)
	before, mtime := fileDigest(t, dbPath)
	cfg := safebootConfig(t, dir, "StorageSafe", key, record, dbPath)
	cfg.Identity.DBFormat = "zstd"
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("rejected record booted normally")
	}
	if state := a.databaseState(); state.Active != "sqlite" || state.CanExport || state.Preferred != "zstd" {
		t.Fatalf("SAFE storage readback: %+v", state)
	}
	if _, _, err := a.exportDatabase(context.Background()); err == nil {
		t.Fatal("SAFE exported a mutable file")
	}
	a.Stop()
	after, afterTime := fileDigest(t, dbPath)
	if after != before || !afterTime.Equal(mtime) {
		t.Fatal("rejected boot touched database")
	}
}
