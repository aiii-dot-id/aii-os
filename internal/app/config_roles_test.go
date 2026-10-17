package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgencyRolesAndWorkersParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{"agency":{"queue_workers":4,"roles":{"critic":{"provider":"OpenRouter","model":"openai/gpt-5.6-sol"}}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agency.QueueWorkers != 4 {
		t.Fatalf("queue_workers = %d, want 4", cfg.Agency.QueueWorkers)
	}
	r, ok := cfg.Agency.Roles["critic"]
	if !ok || r.Provider != "OpenRouter" || r.Model != "openai/gpt-5.6-sol" {
		t.Fatalf("roles parsed as %+v", cfg.Agency.Roles)
	}
}

func TestQueueWorkersDerivesFromParallelSubagents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agency.QueueWorkers != cfg.Agency.MaxParallelSubagents {
		t.Fatalf("queue_workers %d must derive from max_parallel_subagents %d when unset",
			cfg.Agency.QueueWorkers, cfg.Agency.MaxParallelSubagents)
	}
	if cfg.Agency.QueueWorkers < 1 {
		t.Fatalf("derived workers %d — the queue must always have a worker", cfg.Agency.QueueWorkers)
	}
}
