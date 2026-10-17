package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// reloadLog runs one reload over an edited config and returns what the
// operator would read in the log.
func reloadLog(t *testing.T, edit func(*Config)) string {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)

	edited := *cfg
	edit(&edited)
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}

	buf := logsink.CaptureForTest(t)
	a.reloadConfig()
	return buf.String()
}

// THE CONTRACT IS "NEVER SILENTLY". A restart-only edit is deliberately
// retained in the desired config so the next save cannot clobber it
// (TestReloadPreservesRestartEditOnNextSave pins that), which is
// exactly why the log has to say so: the file and the snapshot now
// agree on a value the running process is not using, and an operator
// with no line to read concludes the edit took effect.
func TestReloadDeclaresWhatWaitsForNextBoot(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Dashboard.Port = 9191 })
	if !strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Fatalf("a restart-only edit was absorbed in silence:\n%s", out)
	}
}

// Agency ceilings and broker policy ARE applied live, on every reload,
// and neither said so. A raised subagent depth changed what the engine
// would spawn with nothing in the log to show it — the same silence as
// a restart-only edit, but with the change actually in effect.
func TestReloadAnnouncesTheLiveChangesThatUsedToBeSilent(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Agency.MaxSubagentDepth++ })
	if !strings.Contains(out, "agency ceilings applied live") {
		t.Fatalf("a live agency change was applied without announcing it:\n%s", out)
	}
	if strings.Contains(out, "SAVED FOR NEXT BOOT") {
		t.Fatalf("agency is applied live; it must not be reported as waiting for a boot:\n%s", out)
	}
}

// A reload with nothing changed must stay quiet. A line every time the
// watcher fires is noise, and noise is how a real announcement gets
// missed.
func TestReloadOfAnUnchangedFileSaysNothing(t *testing.T) {
	out := reloadLog(t, func(c *Config) {})
	if strings.Contains(out, "Config reload:") {
		t.Fatalf("an unchanged reload announced something:\n%s", out)
	}
}
