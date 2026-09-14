package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
func TestUIThemeLoadValidateWatch(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})

	path := a.uiThemePath()
	if path != filepath.Join(dir, "theme.json") {
		t.Fatalf("theme must live in the data dir root, got %s", path)
	}

	// .
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

	// .
	for _, bad := range []string{
		`{"v":1,"tokens":{`,
		`{"v":2,"tokens":{}}`,
		`{"v":1,"tokens":{"accent":"#fff"}}`,
		`{"v":1,"tokens":{"--x":"url(https://evil/x)"}}`,
		`{"v":1,"tokens":{"--x":"red;} body{display:none"}}`,
		`{"v":1,"tokens":{"--x":"red/*c*/"}}`,
		`{"v":1,"tokens":{"--x":"\"quoted\""}}`,
		`{"v":1,"tokens":{"--x":"@import 'x'"}}`,
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

	// .
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !a.loadUITheme(true) || a.currentUITheme() != nil {
		t.Fatal("deleting the file must clear the tokens")
	}

	// .
	// .
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

// .
// .
// .
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
