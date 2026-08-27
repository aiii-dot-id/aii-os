package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A live install whose config still carries the pre-rename key dies at
// boot on `unknown field "bash_timeout_seconds"` — a key that by then
// exists nowhere in the tree, so the operator is told what is wrong and
// nothing about what to do. The rename stays a hard cut (no alias, no
// migration): the refusal itself has to carry the new name.
func TestRenamedConfigKeyRefusalNamesTheReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"tools":{"bash_timeout_seconds":300}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("a config carrying a renamed key must be refused")
	}
	if !strings.Contains(err.Error(), "bash_timeout_seconds") {
		t.Errorf("the refusal must name the offending key: %v", err)
	}
	if !strings.Contains(err.Error(), "tools.shell_timeout_seconds") {
		t.Errorf("the refusal must name the key that replaced it — it is the only thing the operator can act on: %v", err)
	}
}

// The advice above is a hand-written string, and the test above only
// reads that same string back — so a typo in it advertises a key that
// exists nowhere and every assertion still passes. Type what the refusal
// hands the operator and require the value to land: that is the only
// thing that pins the advice to the real schema.
func TestTheAdvertisedReplacementKeyIsTheRealSchema(t *testing.T) {
	advertised := renamedConfigKeys["bash_timeout_seconds"]
	section, leaf, ok := strings.Cut(advertised, ".")
	if !ok {
		t.Fatalf("the refusal advertises %q — not a section.key an operator can type", advertised)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, fmt.Appendf(nil, `{%q:{%q:300}}`, section, leaf), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("%s is what the operator is told to type; it must load: %v", advertised, err)
	}
	if cfg.Tools.ShellTimeoutSeconds != 300 {
		t.Errorf("%s carried %d, want the operator's 300", advertised, cfg.Tools.ShellTimeoutSeconds)
	}
}
