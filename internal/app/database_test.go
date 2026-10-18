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
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDatabasePreferencePersistsWithoutHotSwap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
	a := New(cfg)
	st, err := store.NewForTest(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a.databaseView.Store(&databaseView{store: st, path: cfg.Identity.DBPath})
	for _, value := range []string{"zstd", "sqlite", ""} {
		state, err := a.applyConfigChange(map[string]any{"identity.db_format": value})
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
		if _, err := a.applyConfigChange(map[string]any{"identity.db_format": bad}); err == nil {
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

// databaseExportCloseObserver reports the real server-owned cleanup, not the
// client's receipt of Content-Length bytes. The buffered result also lets the
// handler finish if the request or an earlier test assertion fails.
type databaseExportCloseObserver struct {
	io.ReadCloser
	closed chan<- error
}

func (r *databaseExportCloseObserver) Close() error {
	err := r.ReadCloser.Close()
	r.closed <- err
	return err
}

func TestDatabaseStartupAndExportJourney(t *testing.T) {
	dir := t.TempDir()
	// Startup watches the relative plugins directory. Keep every boot side
	// effect inside this disposable identity, including on native test hosts.
	t.Chdir(dir)
	key, record, dbPath := birthFixture(t, dir, "StorageJourney")
	cfg := safebootConfig(t, dir, "StorageJourney", key, record, dbPath)
	for _, format := range []string{"zstd", "sqlite"} {
		t.Run(format, func(t *testing.T) {
			cfg.Identity.DBFormat = format
			a := New(cfg)
			if err := startLiveForTest(a); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, a)
			if reason, safe := a.SafeMode(); safe {
				t.Fatalf("boot SAFE: %s", reason)
			}
			state := a.databaseState()
			if state.Active != format || state.Preferred != format || !state.CanExport || state.Notice != "" {
				t.Fatalf("state %+v", state)
			}
			// A consumer built after conversion must reach the final store.
			if a.timeFac == nil {
				t.Fatal("TIME missing after conversion")
			}
			if _, err := a.store.DB().Exec("INSERT INTO plugin_kv(plugin_id,key,value,updated_at) VALUES('storage-test',?,?,?)", format, "current-data", "2026-09-20T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			closed := make(chan error, 1)
			handler := a.buildLiveHandler()
			export := handler.DatabaseExport
			handler.DatabaseExport = func(ctx context.Context) (io.ReadCloser, int64, error) {
				file, size, err := export(ctx)
				if err != nil {
					return nil, size, err
				}
				return &databaseExportCloseObserver{ReadCloser: file, closed: closed}, size, nil
			}
			a.dashboard.SwapHandler(handler)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.dashboard.LocalURL()+"/database/export", nil)
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
			// Closing the client body does not wait for the server's deferred
			// Close. Observe its result before asserting sensitive data is gone.
			select {
			case err := <-closed:
				if err != nil {
					t.Fatalf("server export cleanup: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("server export cleanup did not complete: %v", ctx.Err())
			}
			left, err := filepath.Glob(filepath.Join(dir, ".db-convert-export-*"))
			if err != nil || len(left) != 0 {
				t.Fatalf("export material retained: %v %v", left, err)
			}
		})
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
	cfg.Identity.DBOptimizeLevels = true
	cfg.Identity.DBLearnDictionary = true
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
	if _, _, err := a.continuity.ExportDatabase(context.Background()); err == nil {
		t.Fatal("SAFE exported a mutable file")
	}
	maintenancePass(t, a)
	stopApp(t, a)
	after, afterTime := fileDigest(t, dbPath)
	if after != before || !afterTime.Equal(mtime) {
		t.Fatal("rejected boot touched database")
	}
}
