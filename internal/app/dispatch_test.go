package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// The capability surface's FOURTH leg (live finding 2026-08-18): the
// schema drift test pinned schema+registry+charter, but dispatch was a
// separate hand-enumerated switch — work was advertised and unroutable
// ("unknown tool: work" from the physical registry). Every verb the
// definitions advertise must route to the verb engine, end to end.
func TestDispatchRoutesEveryAdvertisedVerb(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DispatchTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "DispatchTest", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	// Minimal valid args per advertised verb. A NEW function-calling
	// verb must add its probe here — the loop below fails loudly on a
	// verb it has no args for, keeping this test aligned with the
	// registry instead of silently skipping.
	probes := map[string]string{
		"note":    `{"content": "dispatch probe"}`,
		"recall":  `{"query": "probe"}`,
		"timer":   `{"action": "list"}`,
		"send":    `{"message": "dispatch probe"}`,
		"work":    `{"action": "start", "description": "dispatch probe"}`,
		"project": `{"action": "list"}`,
		"commit":  `{}`,
		"tools":   `{"depth": 1}`,
	}

	for _, v := range identity.Verbs() {
		argsJSON, ok := probes[v.Name]
		if !ok {
			t.Fatalf("verb %q is advertised but this test has no probe args for it — add one", v.Name)
		}
		var tc llm.ToolCall
		tc.Function.Name = v.Name
		tc.Function.Arguments = argsJSON
		out := app.executeToolCall(context.Background(), tc)
		if strings.Contains(out, "unknown tool") || strings.Contains(out, "unknown verb") {
			t.Errorf("advertised verb %q did not route to the verb engine: %q", v.Name, out)
		}
	}

	// A name in NEITHER surface still gets the honest registry error.
	var tc llm.ToolCall
	tc.Function.Name = "no_such_tool"
	tc.Function.Arguments = `{}`
	out := app.executeToolCall(context.Background(), tc)
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("unadvertised name must still fail honestly, got %q", out)
	}
}
