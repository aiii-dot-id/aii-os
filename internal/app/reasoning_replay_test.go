package app

import (
	"path/filepath"
	"testing"
)

// Every client is built from its provider entry by the one resolver that
// boot, reload, a provider switch and each routed sub-agent share, so an
// entry that asks for reasoning replay gets it on all of them and one that
// does not never does.
func TestAnEntryDecidesReasoningReplayForItsClient(t *testing.T) {
	for _, replay := range []bool{true, false} {
		dir := t.TempDir()
		entry := providerEntry{Name: "Local (oMLX)", APIType: "openai", URL: "http://127.0.0.1:9/v1", APIKey: "k",
			DefaultModel: "m", ContextLength: 65536, MaxOutputTokens: 16384, ReplayReasoning: replay}
		writeTestProviders(t, dir, entry)
		cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
		cfg.LLM.Provider = entry.Name
		a := New(cfg)
		cc, _, err := a.resolveLLMConfig(Config{LLM: cfg.LLM}, &providerRegistry{Providers: []providerEntry{entry}})
		if err != nil {
			t.Fatal(err)
		}
		if cc.ReplayReasoning != replay {
			t.Fatalf("replay_reasoning=%v reached the client as %v", replay, cc.ReplayReasoning)
		}
	}
}
