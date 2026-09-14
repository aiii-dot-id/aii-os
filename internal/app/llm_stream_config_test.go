package app

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
func TestLLMStreamFlagReachesTheClient(t *testing.T) {
	a := &App{cfg: defaultConfig()}
	entry := providerEntry{Name: "local", URL: "http://local.invalid", DefaultModel: "m"}
	cc, err := a.clientConfigForEntry(newEffectiveCaps(nil), entry, LLMConfig{TimeoutSeconds: 480}, "m", "k")
	if err != nil {
		t.Fatal(err)
	}
	if cc.NoStream || cc.TimeoutSeconds != 480 {
		t.Fatalf("absent: streamed under the operator's ceiling, got %+v", cc)
	}
	off := false
	cc, err = a.clientConfigForEntry(newEffectiveCaps(nil), entry, LLMConfig{Stream: &off}, "m", "k")
	if err != nil {
		t.Fatal(err)
	}
	if !cc.NoStream {
		t.Fatal("llm.stream=false must reach the client as NoStream")
	}
	// .
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"llm":{"provider":"local","model":"m","stream":false,"timeout_seconds":900}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Stream == nil || *cfg.LLM.Stream || cfg.LLM.TimeoutSeconds != 900 {
		t.Fatalf("the file's stream=false and ceiling are read: %+v", cfg.LLM)
	}
}
