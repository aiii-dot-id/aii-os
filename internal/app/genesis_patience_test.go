package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// HOW LONG A START WAITS ON A FOUNDING SERVER IS THE OPERATOR'S. Ring 5 is
// fetched at every start; the tries and the time of one request were
// numbers in the client. No number takes the default, a number outside the
// range is refused by its key at load and at the settings door, and what
// the door takes is kept for the next start.
func TestAFoundingServersPatienceIsTheOperators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(genesis string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"genesis":{`+genesis+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	for _, ok := range []string{``, `"fetch_attempts":0,"fetch_timeout_ms":0`, `"fetch_attempts":1,"fetch_timeout_ms":1000`, `"fetch_attempts":5,"fetch_timeout_ms":60000`} {
		if _, err := load(ok); err != nil {
			t.Errorf("{%s} is in the range and was refused: %v", ok, err)
		}
	}
	for bad, key := range map[string]string{
		`"fetch_attempts":-1`: "genesis.fetch_attempts", `"fetch_attempts":6`: "genesis.fetch_attempts",
		`"fetch_timeout_ms":999`: "genesis.fetch_timeout_ms", `"fetch_timeout_ms":60001`: "genesis.fetch_timeout_ms", `"fetch_timeout_ms":-5`: "genesis.fetch_timeout_ms",
	} {
		if _, err := load(bad); err == nil || !strings.Contains(err.Error(), key+" must be zero (take the default") {
			t.Errorf("{%s} must be refused by its key and its range: %v", bad, err)
		}
	}

	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]any{"genesis.fetch_attempts": 6.0}); err == nil || !strings.Contains(err.Error(), "genesis.fetch_attempts") {
		t.Errorf("the door must refuse 6 attempts by its key: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"genesis.fetch_timeout_ms": 500.0}); err == nil || !strings.Contains(err.Error(), "genesis.fetch_timeout_ms") {
		t.Errorf("the door must refuse 500 ms by its key: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"genesis.fetch_attempts": 1.0, "genesis.fetch_timeout_ms": 5000.0}); err != nil {
		t.Fatalf("the door refused numbers in the range: %v", err)
	}
	kept, err := LoadConfig(path)
	if err != nil || kept.Genesis.FetchAttempts != 1 || kept.Genesis.FetchTimeoutMs != 5000 {
		t.Fatalf("what the door took is not what the next start reads: %+v, %v", kept.Genesis, err)
	}
}
