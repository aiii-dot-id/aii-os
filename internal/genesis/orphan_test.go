package genesis

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
func TestRefusedBundleBirthLeavesVirginGround(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "unborn")
	root, goodBundle := mintTestRing0(t, "# Constitution\nHonesty.")
	cfg := &BirthConfig{
		Name:       "Orphan",
		Root:       root.Env,
		KeyPath:    filepath.Join(home, "identity.sec"),
		LedgerPath: filepath.Join(home, "ledger.jsonl"),
		DBPath:     filepath.Join(home, "aii.db"),
		// .
		Ring0Bundle: []byte(`{"artifact_kind":"not-a-ring0-bundle","payload":{"laws":"# Constitution\nHonesty."},"payload_sha256":"x"}`),
	}
	if _, err := Birth(cfg); err == nil {
		t.Fatal("birth with a wrong-kind bundle must refuse")
	}

	for _, p := range []string{cfg.KeyPath, cfg.LedgerPath, cfg.DBPath} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("refused birth left an orphan artifact: %s", p)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("bundle refusal touched the identity home: %v", err)
	}

	// .
	cfg.Ring0Bundle = goodBundle
	result, err := Birth(cfg)
	if err != nil {
		t.Fatalf("retry after a refused birth must succeed on virgin ground: %v", err)
	}
	result.Ledger.Close()
}

// .
func TestEmptyConstitutionBundleBirthLeavesVirginGround(t *testing.T) {
	dir := t.TempDir()
	root, emptyBundle := mintTestRing0(t, "")
	cfg := &BirthConfig{
		Name:       "Orphan2",
		Root:       root.Env,
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
		// .
		Ring0Bundle: emptyBundle,
	}
	if _, err := Birth(cfg); err == nil {
		t.Fatal("birth with a signed-but-empty-constitution bundle must refuse")
	}
	if _, err := os.Stat(cfg.KeyPath); err == nil {
		t.Error("empty-constitution refusal left an orphan key")
	}
}
