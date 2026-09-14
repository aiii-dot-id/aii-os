package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .
// .
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
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		// .
		// .
		// .
		// .
		// .
		// .
		Agency: armedNudges(defaultConfig().Agency),
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	// .
	// .
	// .
	// .
	probes := map[string]string{
		"note":   `{"content": "dispatch probe"}`,
		"recall": `{"query": "probe"}`,
		"send":   `{"message": "dispatch probe"}`,
		"work":   `{"action": "start", "description": "dispatch probe"}`,
		"commit": `{}`,
		"tools":  `{"depth": 1}`,
	}

	for _, v := range identity.Verbs() {
		argsJSON, ok := probes[v.Name]
		if !ok {
			t.Fatalf("verb %q is advertised but this test has no probe args for it — add one", v.Name)
		}
		var tc llm.ToolCall
		tc.Function.Name = v.Name
		tc.Function.Arguments = argsJSON
		out := app.executeToolCall(context.Background(), tc).Text
		if strings.Contains(out, "unknown tool") || strings.Contains(out, "unknown verb") {
			t.Errorf("advertised verb %q did not route to the verb engine: %q", v.Name, out)
		}
	}

	// .
	// .
	// .
	for _, absorbed := range []string{"timer", "skill", "project", "curiosity", "measure"} {
		var tc llm.ToolCall
		tc.Function.Name = absorbed
		tc.Function.Arguments = `{"action": "list"}`
		if out := app.executeToolCall(context.Background(), tc).Text; !strings.Contains(out, "unknown tool") {
			t.Errorf("absorbed name %q must not route as a tool, got %q", absorbed, out)
		}
	}

	// .
	var tc llm.ToolCall
	tc.Function.Name = "no_such_tool"
	tc.Function.Arguments = `{}`
	out := app.executeToolCall(context.Background(), tc).Text
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("unadvertised name must still fail honestly, got %q", out)
	}
}

// .
// .
func dispatchApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SeamTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     armedNudges(defaultConfig().Agency),
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	return app, dir
}

func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// .
// .
// .
// .
func TestObservationCarriesTypedStatusAcrossTheBoundary(t *testing.T) {
	app, dir := dispatchApp(t)
	defer app.Stop()

	var tc llm.ToolCall
	tc.Function.Name = "read"
	tc.Function.Arguments = `{"file_path":` + strconv.Quote(filepath.Join(dir, "absent.txt")) + `}`
	obs := app.executeToolCall(context.Background(), tc)
	if !obs.Failed {
		t.Errorf("reading a missing file must set Failed, got %+v", obs)
	}
	if !strings.Contains(obs.Text, "Error") {
		t.Errorf("the rendered text still carries the error for the model: %q", obs.Text)
	}

	if err := writeFileForTest(filepath.Join(dir, "present.txt"), "hello"); err != nil {
		t.Fatal(err)
	}
	tc.Function.Arguments = `{"file_path":` + strconv.Quote(filepath.Join(dir, "present.txt")) + `}`
	obs = app.executeToolCall(context.Background(), tc)
	if obs.Failed || obs.Truncated {
		t.Errorf("clean read must not set status flags: %+v", obs)
	}

	tc.Function.Name = "no_such_tool"
	tc.Function.Arguments = `{}`
	if obs := app.executeToolCall(context.Background(), tc); !obs.Failed {
		t.Errorf("unknown tool must set Failed, got %+v", obs)
	}
}

// .
// .
func armedNudges(a AgencyConfig) AgencyConfig {
	on := true
	a.HeuristicNudges = &on
	return a
}
