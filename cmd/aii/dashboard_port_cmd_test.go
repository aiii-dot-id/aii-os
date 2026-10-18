package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestDashboardPortCommandWritesThroughTheOneConfigWriter(t *testing.T) {
	dir, err := install.Create(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runDashboardPort([]string{"-dir", dir, "8199"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	cfg, err := app.LoadConfig(install.ConfigPathIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dashboard.Port != 8199 || stdout.String() != "8199\n" {
		t.Fatalf("port %d, stdout %q", cfg.Dashboard.Port, stdout.String())
	}
	if !strings.Contains(stderr.String(), "address changes") || !strings.Contains(stderr.String(), "signs in again") {
		t.Fatalf("the command does not say that the address and its sign-in change: %q", stderr.String())
	}

	raw, _ := os.ReadFile(install.ConfigPathIn(dir))
	for _, args := range [][]string{{"-dir", dir, "0"}, {"-dir", dir, "65536"}, {"-dir", dir, "eighty"}, {"-dir", dir}} {
		stdout.Reset()
		stderr.Reset()
		if code := runDashboardPort(args, &stdout, &stderr); code == 0 {
			t.Fatalf("%v was accepted", args)
		}
	}
	if now, _ := os.ReadFile(install.ConfigPathIn(dir)); !bytes.Equal(raw, now) {
		t.Fatal("a refused port changed config.json")
	}
	empty := t.TempDir()
	if code := runDashboardPort([]string{"-dir", empty, "8199"}, &stdout, &stderr); code == 0 {
		t.Fatal("a directory with no config took a port")
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Fatalf("a refused port created %v", entries)
	}
	if _, err := os.Stat(filepath.Join(empty, "config")); !os.IsNotExist(err) {
		t.Fatal("a refused port created a config directory")
	}
}
