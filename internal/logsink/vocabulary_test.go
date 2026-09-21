package logsink

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestEveryCategoryTheTreeNamesIsDeclared(t *testing.T) {
	root := repoRoot(t)
	call := regexp.MustCompile(`logsink\.(?:Error|Warn|Info|Debug|Trace|Tick)\("([^"]*)"`)

	declared := byName()
	seen := map[string][]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "dist", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		line := 0
		for sc.Scan() {
			line++
			for _, m := range call.FindAllStringSubmatch(sc.Text(), -1) {
				seen[m[1]] = append(seen[m[1]], rel)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("no logsink call sites were found at all — this test would pass while checking nothing")
	}
	for category, where := range seen {
		if _, ok := declared[category]; !ok {
			t.Errorf("%q is used in %s but not declared in vocabulary.go — a detail is declared where it is emitted", category, strings.Join(dedupe(where), ", "))
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

// .
// .
// .
// .
func TestEveryDeclaredDetailIsWellFormed(t *testing.T) {
	known := map[Aspect]bool{}
	for _, a := range Aspects() {
		known[a] = true
	}
	seen := map[string]bool{}
	for _, d := range Details() {
		if d.Subsystem == "" || strings.ContainsAny(d.Subsystem, ". ") {
			t.Errorf("%q: a subsystem is one lower-case word", d.Name())
		}
		if !known[d.Aspect] {
			t.Errorf("%q: %q is not an aspect", d.Name(), d.Aspect)
		}
		if strings.TrimSpace(d.What) == "" {
			t.Errorf("%q: a detail says what it is, because `aii log details` prints it", d.Name())
		}
		if seen[d.Name()] {
			t.Errorf("%q is declared twice", d.Name())
		}
		seen[d.Name()] = true
	}
}

// .
// .
func TestTheMostSpecificNamingWins(t *testing.T) {
	restore(t)

	// .
	// .
	SetGroups(map[string][]string{"quiet": {"rhythm.pass", "route.renewal"}})

	SetLevels(slog.LevelError, map[string]slog.Level{"pass": slog.LevelWarn})
	if got := thresholdFor("rhythm.pass"); got != slog.LevelWarn {
		t.Fatalf("by aspect: %v, want warn", got)
	}
	SetLevels(slog.LevelError, map[string]slog.Level{"pass": slog.LevelWarn, "rhythm": slog.LevelInfo})
	if got := thresholdFor("rhythm.pass"); got != slog.LevelInfo {
		t.Fatalf("subsystem must beat aspect: %v, want info", got)
	}
	SetLevels(slog.LevelError, map[string]slog.Level{"pass": slog.LevelWarn, "rhythm": slog.LevelInfo, "quiet": slog.LevelDebug})
	if got := thresholdFor("rhythm.pass"); got != slog.LevelDebug {
		t.Fatalf("a defined group must beat the subsystem: %v, want debug", got)
	}
	SetLevels(slog.LevelError, map[string]slog.Level{"pass": slog.LevelWarn, "rhythm": slog.LevelInfo, "quiet": slog.LevelDebug, "rhythm.pass": LevelTrace})
	if got := thresholdFor("rhythm.pass"); got != LevelTrace {
		t.Fatalf("the detail itself must beat everything: %v, want trace", got)
	}
}

// .
// .
func TestTheSmallestDefinedGroupWins(t *testing.T) {
	restore(t)
	SetGroups(map[string][]string{
		"broad":  {"rhythm.pass", "route.renewal", "workq.end"},
		"narrow": {"rhythm.pass"},
	})
	SetLevels(slog.LevelError, map[string]slog.Level{"broad": slog.LevelInfo, "narrow": LevelTrace})
	for i := 0; i < 50; i++ {
		if got := thresholdFor("rhythm.pass"); got != LevelTrace {
			t.Fatalf("run %d resolved to %v, want the narrower group's trace", i, got)
		}
	}
}

// .
// .
func TestAPayloadDetailIsNotInherited(t *testing.T) {
	restore(t)
	declared = append(declared, Detail{Subsystem: "probe", Aspect: AspectPrompt, Payload: true, What: "a probe payload"})
	rebuildIndex()
	t.Cleanup(func() { declared = declared[:len(declared)-1]; rebuildIndex() })

	SetGroups(map[string][]string{"everything": {"probe.prompt"}})
	SetLevels(slog.LevelError, map[string]slog.Level{
		"probe": LevelTrace, "prompt": LevelTrace, "everything": LevelTrace,
	})
	if got := thresholdFor("probe.prompt"); got != slog.LevelError {
		t.Fatalf("a payload detail was turned on by inheritance: %v, want the default error", got)
	}
	SetLevels(slog.LevelError, map[string]slog.Level{"probe.prompt": LevelTrace})
	if got := thresholdFor("probe.prompt"); got != LevelTrace {
		t.Fatalf("a payload detail refused its own name: %v, want trace", got)
	}
}

// .
// .
// .
func TestDerivedGroupsNeedNoDefinition(t *testing.T) {
	g := DerivedGroups()
	if members, ok := g["rhythm"]; !ok || len(members) == 0 {
		t.Fatalf("a subsystem is not a group: %v", g)
	}
	if members, ok := g["pass"]; !ok || len(members) == 0 {
		t.Fatalf("an aspect is not a group: %v", g)
	}
}

func restore(t *testing.T) {
	t.Helper()
	def, cat := Levels()
	groups := Groups()
	t.Cleanup(func() { SetLevels(def, cat); SetGroups(groups) })
}
