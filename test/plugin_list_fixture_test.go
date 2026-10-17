package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// guard:tree
func TestNoFixtureWritesThePluginListWithoutItsLock(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "app", "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures to check (%v) — this test would pass while checking nothing", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range unlockedPluginWrites(string(src)) {
			t.Errorf("%s:%d writes the plugin list without pluginMu — use addPlugin or setPlugins (channels_test.go)", filepath.Base(f), line)
		}
	}
}

var pluginListWrite = regexp.MustCompile(`^\s*\w+\.plugins\s*=[^=]`)

// unlockedPluginWrites returns the lines that assign an App's plugins field
// in a function that has taken no pluginMu.Lock() before them.
func unlockedPluginWrites(src string) []int {
	var bare []int
	held := false
	for i, l := range strings.Split(src, "\n") {
		switch {
		case strings.HasPrefix(l, "func "):
			held = false
		case strings.Contains(l, "pluginMu.Lock()"):
			held = true
		case pluginListWrite.MatchString(l) && !held:
			bare = append(bare, i+1)
		}
	}
	return bare
}

// The guard above can fail: a bare write is seen, a locked one is not.
func TestThePluginListGuardSeesABareWrite(t *testing.T) {
	bare := "func f(a *App) {\n\ta.plugins = append(a.plugins, ap)\n}\n"
	locked := "func f(a *App) {\n\ta.pluginMu.Lock()\n\tdefer a.pluginMu.Unlock()\n\ta.plugins = append(a.plugins, ap)\n}\n"
	if got := unlockedPluginWrites(bare); len(got) != 1 || got[0] != 2 {
		t.Fatalf("a bare write was not seen: %v", got)
	}
	if got := unlockedPluginWrites(locked); len(got) != 0 {
		t.Fatalf("a locked write was refused: %v", got)
	}
	// One function's lock is not another's, and a lock taken after the write
	// is not held at it.
	other := "func f(a *App) {\n\ta.pluginMu.Lock()\n\ta.pluginMu.Unlock()\n}\nfunc g(a *App) {\n\ta.plugins = nil\n}\n"
	after := "func f(a *App) {\n\ta.plugins = nil\n\ta.pluginMu.Lock()\n}\n"
	if got := unlockedPluginWrites(other); len(got) != 1 || got[0] != 6 {
		t.Fatalf("another function's lock excused a bare write: %v", got)
	}
	if got := unlockedPluginWrites(after); len(got) != 1 || got[0] != 2 {
		t.Fatalf("a lock taken after the write excused it: %v", got)
	}
}
