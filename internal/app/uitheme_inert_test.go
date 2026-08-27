package app

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUIThemeInertTokenWarns pins tolerate-AND-log for theme tokens,
// the sibling of TestUILayoutInertProfileWarns.
//
// theme.json validates a token's SHAPE — `--name` under a character
// allowlist, value under an allowlist. Shape is not consumption:
// `--acent` for `--acc` is perfectly well-formed, so it is validated,
// stored, re-marshalled, transmitted and applied to documentElement,
// and then read by nothing. Nothing errors. The operator's edit simply
// does not happen.
//
// It is NOT refused, because sections carry their own stylesheets and
// receive the same tokens over the bridge — an undeclared name can be
// deliberate. So the whole defence is the log line, and a log line
// needs a test or it is prose that compiles.
func TestUIThemeInertTokenWarns(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	path := a.uiThemePath()

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	// Negative control: a token theme.css really declares must produce
	// no inert line. This proves the assertion below discriminates
	// rather than firing on any load at all.
	declared := `{"v":1,"tokens":{"--acc":"#123456"}}`
	if err := os.WriteFile(path, []byte(declared), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUITheme(false) {
		t.Fatal("a fresh valid theme must register as changed")
	}
	if strings.Contains(logBuf.String(), "INERT") {
		t.Fatalf("a declared token must not warn, got: %s", logBuf.String())
	}

	// The real case: same file plus one plausible typo.
	logBuf.Reset()
	withTypo := `{"v":1,"tokens":{"--acc":"#123456","--acent":"#654321"}}`
	if err := os.WriteFile(path, []byte(withTypo), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUITheme(false) {
		t.Fatal("the changed theme must register as changed")
	}
	out := logBuf.String()
	if !strings.Contains(out, "INERT") {
		t.Fatalf("a token no stylesheet declares must be announced, got: %s", out)
	}
	if !strings.Contains(out, "--acent") {
		t.Fatalf("the warning must NAME the inert token, got: %s", out)
	}
	if strings.Contains(out, "--acc ") || strings.Contains(out, "token --acc is") {
		t.Fatalf("the declared token must not be reported inert, got: %s", out)
	}

	// Tolerance is the other half of the rule: the inert token is kept,
	// not discarded, so a theme written for a newer frame survives.
	if raw := string(a.currentUITheme()); !strings.Contains(raw, "--acent") {
		t.Fatalf("an inert token must be KEPT and transmitted, got: %s", raw)
	}
}
