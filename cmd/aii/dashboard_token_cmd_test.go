package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func TestDashboardTokenCommandIsExplicitAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Dashboard.AccessToken = "operator-token"
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runDashboardToken([]string{"-dir", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if stdout.String() != "operator-token\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading the token changed config.json")
	}
}

func TestDashboardTokenCommandRefusesMissingCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := app.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runDashboardToken([]string{"-config", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "no dashboard access token") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
