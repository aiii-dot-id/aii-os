package install_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestCreatedSlotConfigLoadsInTheRuntime(t *testing.T) {
	root := t.TempDir()
	dir, err := install.Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	path := install.ConfigPathIn(dir)
	// .
	// .
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("installer did not write its config: %v", err)
	}
	cfg, err := app.LoadConfig(path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("the runtime refuses the config `aii init` writes: %v\n\nconfig was:\n%s", err, raw)
	}

	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("loading the installed config changed it: %v", err)
	}
	if cfg.SourcePath != path {
		t.Fatalf("loaded %q, want the installer's %q", cfg.SourcePath, path)
	}
	if _, err := os.Stat(filepath.Join(dir, install.ConfigFileName)); !os.IsNotExist(err) {
		t.Fatalf("fresh slot acquired a second config: %v", err)
	}

	// .
	// .
	if cfg.Dashboard.Port != install.Port(0) {
		t.Fatalf("created config serves on %d, want %d", cfg.Dashboard.Port, install.Port(0))
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if cfg.Identity.LedgerPath == "" || cfg.Identity.DBPath == "" || cfg.Identity.KeyPath == "" {
		t.Fatalf("the slot config resolves no identity paths — birth would refuse it: ledger=%q db=%q key=%q",
			cfg.Identity.LedgerPath, cfg.Identity.DBPath, cfg.Identity.KeyPath)
	}
	// .
	// .
	for name, p := range map[string]string{
		"ledger": cfg.Identity.LedgerPath,
		"db":     cfg.Identity.DBPath,
		"key":    cfg.Identity.KeyPath,
	} {
		if filepath.IsAbs(p) {
			t.Fatalf("%s path is absolute (%q) — it would escape the slot", name, p)
		}
	}
}

// .
func TestEverySlotConfigLoads(t *testing.T) {
	root := t.TempDir()
	for n := 0; n < 3; n++ {
		dir, err := install.Create(root, n)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := app.LoadConfig(install.ConfigPathIn(dir))
		if err != nil {
			t.Fatalf("slot %d writes a config the runtime refuses: %v", n, err)
		}
		if cfg.Dashboard.Port != install.Port(n) {
			t.Fatalf("slot %d serves on %d, want %d", n, cfg.Dashboard.Port, install.Port(n))
		}
		if cfg.Identity.LedgerPath == "" || cfg.Identity.DBPath == "" || cfg.Identity.KeyPath == "" {
			t.Fatalf("slot %d resolves no identity paths — birth would refuse it", n)
		}
	}
}
