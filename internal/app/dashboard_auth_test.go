package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestEnsureDashboardTokenMintsOnceAndPersistsTheHash(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Host = "127.0.0.1"

	// .
	a := New(cfg)
	a.ensureDashboardToken()
	if cfg.Dashboard.AuthTokenSHA256 != "" || a.DashboardMintedToken() != "" {
		t.Fatal("a token was minted without require_token")
	}

	// .
	// .
	// .
	var logbuf bytes.Buffer
	log.SetOutput(&logbuf)
	defer log.SetOutput(os.Stderr)
	cfg.Dashboard.RequireToken = true
	a.ensureDashboardToken()
	hash := cfg.Dashboard.AuthTokenSHA256
	if len(hash) != 64 {
		t.Fatalf("no hash on record after mint: %q", hash)
	}
	tok := a.DashboardMintedToken()
	if tok == "" {
		t.Fatal("the minted token is not recoverable this boot")
	}
	sum := sha256.Sum256([]byte(tok))
	if hex.EncodeToString(sum[:]) != hash {
		t.Fatal("the stored hash is not the hash of the minted token")
	}
	if a.DashboardMintedToken() != "" {
		t.Fatal("the token survived its pickup — the read must clear it")
	}
	if strings.Contains(logbuf.String(), tok) {
		t.Fatal("the RAW token reached the log package — logsink persists that stream")
	}
	ondisk, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatalf("config was not persisted: %v", err)
	}
	if !strings.Contains(string(ondisk), hash) {
		t.Fatal("persisted config lost the hash")
	}
	if strings.Contains(string(ondisk), tok) {
		t.Fatal("the RAW token reached disk — only the hash may rest")
	}

	// .
	a.ensureDashboardToken()
	if cfg.Dashboard.AuthTokenSHA256 != hash {
		t.Fatal("an existing token was re-minted")
	}
}
