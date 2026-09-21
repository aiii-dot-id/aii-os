package app

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestDatabaseExportStaysOnOpenedPathAfterConfigReload(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.DBPath = filepath.Join(dir, "opened.db")
	a := New(cfg)
	opened, err := store.New(cfg.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.AddConversationTurn("operator", "the active database"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activateDatabaseFormat(t.Context(), *cfg, opened); err != nil {
		t.Fatal(err)
	}
	a.live = true
	next := a.configSnapshot()
	next.Identity.DBPath = filepath.Join(dir, "next-boot.db")
	other, err := store.New(next.Identity.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AddConversationTurn("operator", "not the active database"); err != nil {
		other.Close()
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(&next); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Identity.DBPath; got != next.Identity.DBPath {
		t.Fatalf("fixture did not publish the next boot's configuration: %s", got)
	}
	download, _, err := a.exportDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer download.Close()
	b, err := io.ReadAll(download)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "download.db")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var content string
	if err := db.QueryRow("SELECT content FROM conversations").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "the active database" {
		t.Fatalf("configuration redirected export: %q", content)
	}
}
