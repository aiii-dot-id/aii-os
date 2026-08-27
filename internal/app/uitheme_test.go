package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUIThemeLoadValidateWatch mirrors TestUILayoutLoadValidateWatch:
// the theme loads from the data dir root, refuses garbage while
// KEEPING the previous state, treats deletion as back-to-defaults,
// and the watcher picks up an mtime change within its poll.
func TestUIThemeLoadValidateWatch(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})

	path := a.uiThemePath()
	if path != filepath.Join(dir, "theme.json") {
		t.Fatalf("theme must live in the data dir root, got %s", path)
	}

	// Absent = compiled defaults.
	a.loadUITheme(true)
	if a.currentUITheme() != nil {
		t.Fatal("absent file must mean no tokens")
	}

	good := `{"v":1,"tokens":{"--accent":"#7cc4ff","--bg":"rgba(11,15,20,.8)"}}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUITheme(true) {
		t.Fatal("a fresh valid theme must register as changed")
	}
	stored := string(a.currentUITheme())
	if !strings.Contains(stored, "--accent") || !strings.Contains(stored, "#7cc4ff") {
		t.Fatalf("validated tokens must be stored, got %s", stored)
	}

	// Garbage keeps the last good state.
	for _, bad := range []string{
		`{"v":1,"tokens":{`,                                  // invalid JSON
		`{"v":2,"tokens":{}}`,                                // wrong version
		`{"v":1,"tokens":{"accent":"#fff"}}`,                 // not a custom property
		`{"v":1,"tokens":{"--x":"url(https://evil/x)"}}`,     // the beacon
		`{"v":1,"tokens":{"--x":"red;} body{display:none"}}`, // declaration breakout
		`{"v":1,"tokens":{"--x":"red/*c*/"}}`,                // comment
		`{"v":1,"tokens":{"--x":"\"quoted\""}}`,              // string breakout
		`{"v":1,"tokens":{"--x":"@import 'x'"}}`,             // at-rule
	} {
		if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if a.loadUITheme(true) {
			t.Fatalf("refused theme must not register as changed: %s", bad)
		}
		if got := string(a.currentUITheme()); !strings.Contains(got, "#7cc4ff") {
			t.Fatalf("refused theme must keep the previous tokens, got %s after %s", got, bad)
		}
	}

	// Deletion = an operator statement: compiled defaults again.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !a.loadUITheme(true) || a.currentUITheme() != nil {
		t.Fatal("deleting the file must clear the tokens")
	}

	// The watcher half: start on the absent file, then let the
	// operator's write land — the poll must pick it up.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	go a.watchUITheme()
	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(string(a.currentUITheme()), "#7cc4ff") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("watcher never picked up the theme change")
}

// TestUIThemeValueGrammar pins the allowlist directly: the values a
// theme legitimately needs pass, and every construct that turns a
// stylesheet into something other than a stylesheet is refused.
func TestUIThemeValueGrammar(t *testing.T) {
	ok := []string{"#0b0f14", "13px", "rgba(255,255,255,.06)", "1.5", "hsl(210,20%,12%)",
		"'SF Mono'", "0 1px 2px", "calc(100%+2px)", "system-ui"}
	for _, v := range ok {
		if !validThemeValue(v) {
			t.Errorf("legitimate token value refused: %q", v)
		}
	}
	bad := []string{"", "url(x)", "URL(x)", "red;color:blue", "a}b{c", "@import 'x'",
		"a/*c*/", "\"q\"", "a\\65", "<x>", "a:b", strings.Repeat("a", 201)}
	for _, v := range bad {
		if validThemeValue(v) {
			t.Errorf("dangerous token value accepted: %q", v)
		}
	}
	if validThemeName("accent") || validThemeName("--") || validThemeName("--a b") {
		t.Error("invalid custom-property names accepted")
	}
	if !validThemeName("--accent-2") || !validThemeName("--a_b") {
		t.Error("valid custom-property names refused")
	}
}
