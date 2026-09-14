package app

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
func reloadLog(t *testing.T, edit func(*Config)) string {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true

	edited := *cfg
	edit(&edited)
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	a.reloadConfig()
	return buf.String()
}

// .
// .
// .
// .
// .
// .
func TestReloadDeclaresWhatWaitsForNextBoot(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Dashboard.Port = 9191 })
	if !strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Fatalf("a restart-only edit was absorbed in silence:\n%s", out)
	}
}

// .
// .
// .
// .
func TestReloadAnnouncesTheLiveChangesThatUsedToBeSilent(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Agency.MaxSubagentDepth++ })
	if !strings.Contains(out, "agency ceilings applied live") {
		t.Fatalf("a live agency change was applied without announcing it:\n%s", out)
	}
	if strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Fatalf("agency is applied live; it must not be reported as waiting for a boot:\n%s", out)
	}
}

// .
// .
// .
func TestReloadOfAnUnchangedFileSaysNothing(t *testing.T) {
	out := reloadLog(t, func(c *Config) {})
	if strings.Contains(out, "Config reload:") {
		t.Fatalf("an unchanged reload announced something:\n%s", out)
	}
}
