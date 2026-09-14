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

	path := filepath.Join(dir, "config.json")
	cfg, err := app.LoadConfig(path)
	if err != nil {
		raw, _ := os.ReadFile(path)
		t.Fatalf("the runtime refuses the config `aii init` writes: %v\n\nconfig was:\n%s", err, raw)
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
		cfg, err := app.LoadConfig(filepath.Join(dir, "config.json"))
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
