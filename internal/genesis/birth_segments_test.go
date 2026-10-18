package genesis_test

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestBirthNeverAppendsANewIdentityToSurvivingSealedRecords(t *testing.T) {
	dir := t.TempDir()
	root := genesistest.NewRoot(t)
	cfg := &genesis.BirthConfig{Name: "Original", Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	born, err := genesis.Birth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := born.Ledger.Append(ledger.EventSystemWitnessed, 0,
		map[string]any{"receipt": map[string]any{"ledger_ordinal": 1, "ledger_hash": born.Ledger.LastHash()}}, born.KeyPair); err != nil {
		t.Fatal(err)
	}
	if err := born.Ledger.Seal(2); err != nil {
		t.Fatal(err)
	}
	if err := born.Ledger.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{cfg.KeyPath, cfg.LedgerPath} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	segment := filepath.Join(dir, "segment-1-2.jsonl.gz")
	before, err := os.ReadFile(segment)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Name = "Replacement"
	if next, err := genesis.Birth(cfg); !errors.Is(err, ledger.ErrRecordExists) {
		if next != nil {
			next.Ledger.Close()
		}
		t.Fatalf("birth accepted surviving identity or refused for another reason: %v", err)
	}
	for _, p := range []string{cfg.KeyPath, cfg.LedgerPath, cfg.DBPath} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("birth refusal left an artifact at %s: %v", p, err)
		}
	}
	after, err := os.ReadFile(segment)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatalf("birth changed original history: %v", err)
	}
}
