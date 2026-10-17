package install_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

// A slot is only a slot if the runtime will actually boot it. Nothing
// otherwise binds the installer's idea of a config to the runtime's:
// config_load refuses unknown fields, so the day a key is renamed or
// cut, `aii init` starts writing a config that every new identity dies
// on — and it would surface in a person's hands rather than here.
//
// This is the one test that fails when those two drift apart. It has
// already had a near miss: identity.name was cut from the config while
// the installer was being written, and the embedded default happened to
// have been updated in the same change.
func TestCreatedSlotConfigLoadsInTheRuntime(t *testing.T) {
	root := t.TempDir()
	dir, err := install.Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	path := install.ConfigPathIn(dir)
	// LoadConfig creates defaults when a path is missing. Require the
	// installer's actual bytes before using it as the contract witness.
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

	// And it must be pointed at the slot it was created in, not at
	// whatever directory the process happens to be sitting in.
	if cfg.Dashboard.Port != install.Port(0) {
		t.Fatalf("created config serves on %d, want %d", cfg.Dashboard.Port, install.Port(0))
	}

	// LOADING WITHOUT ERROR IS NOT ENOUGH, and this test learned that
	// the hard way: it passed while every slot it created was
	// unbirthable. The template ships the identity paths as empty
	// strings, LoadConfig treats an explicit empty as an operator
	// value, and genesis.Birth then refuses with "key, ledger, and db
	// paths are required". A config that parses but resolves nowhere is
	// exactly the shape that gets through a weak assertion.
	if cfg.Identity.LedgerPath == "" || cfg.Identity.DBPath == "" || cfg.Identity.KeyPath == "" {
		t.Fatalf("the slot config resolves no identity paths — birth would refuse it: ledger=%q db=%q key=%q",
			cfg.Identity.LedgerPath, cfg.Identity.DBPath, cfg.Identity.KeyPath)
	}
	// Relative, so they land inside the slot rather than wherever the
	// process happens to run (the ENTIRELY LOCAL law).
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

// Every slot number a person can reach, not just the first.
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
