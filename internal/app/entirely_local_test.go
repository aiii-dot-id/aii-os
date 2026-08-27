package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// Operator law (2026-08-20): Go aii-os is ENTIRELY LOCAL. The install
// directory is the identity's whole world — config, ledger, database,
// key all live beside the binary. There is no other config, no other
// database, no <user>/.aii-os, no %APPDATA%, no search paths, no
// adoption of data found elsewhere. Deleting the directory deletes the
// identity. (The violation this pins: a configless run resolved its
// data dir to /root/.aii-os and resurrected an identity the operator
// believed deleted.)
func TestDefaultsAreEntirelyLocal(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	for name, p := range map[string]string{
		"ledger": cfg.Identity.LedgerPath,
		"db":     cfg.Identity.DBPath,
		"key":    cfg.Identity.KeyPath,
	} {
		if filepath.IsAbs(p) {
			t.Fatalf("%s default is absolute (%q) — an entirely-local install uses paths relative to its own directory", name, p)
		}
		if strings.Contains(p, "..") {
			t.Fatalf("%s default escapes the install dir: %q", name, p)
		}
		if strings.Contains(p, ".aii-os") {
			t.Fatalf("%s default reaches a global home (%q) — there is no <user>/.aii-os", name, p)
		}
	}
}
