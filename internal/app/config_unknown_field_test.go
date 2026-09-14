package app

import (
	"fmt"
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

// .
// .
// .
// .
// .
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
