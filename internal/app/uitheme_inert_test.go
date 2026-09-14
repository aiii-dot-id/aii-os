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
func TestUIThemeInertTokenWarns(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	path := a.uiThemePath()

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	// .
	// .
	// .
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

	// .
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

	// .
	// .
	if raw := string(a.currentUITheme()); !strings.Contains(raw, "--acent") {
		t.Fatalf("an inert token must be KEPT and transmitted, got: %s", raw)
	}
}
