package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestToolsDisabledIsReadAtLoad(t *testing.T) {
	for _, tc := range []struct {
		name, list string
		want       []string // what the registry switches off
		refused    string   // the entry a refusal names
	}{
		{"legacy shell", `["bash"]`, []string{"shell"}, ""},
		{"builtins", `["shell","web_fetch"]`, []string{"shell", "web_fetch"}, ""},
		{"plugin operation", `["pl_id_example_memory_recent"]`, []string{"pl_id_example_memory_recent"}, ""},
		{"typo", `["read","shel"]`, nil, "shel"},
		{"a verb is no switch", `["note"]`, nil, "note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := unchangedConfigFile(t, "config.json", `{"tools":{"disabled":`+tc.list+`}}`)
			cfg, err := LoadConfig(path)
			if tc.refused != "" {
				var refusal *disabledToolError
				if !errors.As(err, &refusal) || refusal.Entry != tc.refused {
					t.Fatalf("want a refusal naming %q, got %v", tc.refused, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := disabledTools(cfg.Tools.Disabled); err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("disabledTools(%q) = %q, %v; want %q", cfg.Tools.Disabled, got, err, tc.want)
			}
		})
	}
}

// Both boots apply the list through the one reader: ["bash"] switches the
// shell off at a live boot and at a SAFE boot, and the shell's refusal is
// typed.
func TestBothBootsReadBashAsTheShell(t *testing.T) {
	for _, boot := range []struct {
		name string
		run  func(a *App) error
	}{
		{"live", startLiveForTest},
		{"safe", func(a *App) error { return a.startSafeBoot(safeRecord, "test: the operator's switches hold in SAFE") }},
	} {
		t.Run(boot.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Switches")
			cfg := safebootConfig(t, dir, "Switches", keyPath, ledgerPath, dbPath)
			cfg.Tools.Disabled = []string{"bash"}
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := boot.run(a); err != nil {
				t.Fatalf("%s boot: %v", boot.name, err)
			}
			res, err := a.toolReg.Execute(t.Context(), "shell", map[string]any{"command": "echo on"})
			if err != nil || res.ReasonCode != tools.ReasonOperatorDisabled {
				t.Fatalf("tools.disabled [\"bash\"] must switch the shell off at the %s boot, typed: %+v, %v", boot.name, res, err)
			}
			if !a.toolReg.ToolEnabled("read") {
				t.Fatalf("the %s boot switched off a tool the list does not name", boot.name)
			}
		})
	}
}

// reloadTools runs one live reload over a registry whose switches are the
// running list, after the file is edited, and returns the app and the log.
func reloadTools(t *testing.T, running []string, edit func(*Config)) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Tools.CWD = dir
	cfg.Tools.Disabled = running
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	a.rings = ring.NewManager() // a reload rewrites the floor once there is a registry
	a.toolReg = tools.NewRegistry(dir, a.ensureRing5Policy(), tools.Timeouts{})
	if err := applyDisabledTools(a.toolReg, running); err != nil {
		t.Fatal(err)
	}
	edited := *cfg
	edit(&edited)
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	buf := logsink.CaptureForTest(t)
	a.reloadConfig()
	return a, buf.String()
}

func TestReloadReadsBashAsTheShell(t *testing.T) {
	a, out := reloadTools(t, nil, func(c *Config) { c.Tools.Disabled = []string{"bash"} })
	if a.toolReg.ToolEnabled("shell") {
		t.Fatalf("a reload to [\"bash\"] left the shell on:\n%s", out)
	}
	if !a.toolReg.ToolEnabled("read") {
		t.Fatal("the reload switched off a tool the list does not name")
	}
	if !strings.Contains(out, `read as "shell"`) {
		t.Fatalf("the legacy name was read without a decision line:\n%s", out)
	}
}

// A refused reload keeps the running state — the switches and every other
// live setting the edit carried — and says why, naming the entry.
func TestReloadRefusesAnUnknownToolAndKeepsTheRunningState(t *testing.T) {
	var depth int
	a, out := reloadTools(t, []string{"shell"}, func(c *Config) {
		c.Tools.Disabled = []string{"shel"}
		c.Agency.MaxSubagentDepth++
		depth = c.Agency.MaxSubagentDepth
	})
	if a.toolReg.ToolEnabled("shell") || !a.toolReg.ToolEnabled("read") {
		t.Fatal("a refused reload changed the running switches")
	}
	running := a.configSnapshot()
	if !slices.Equal(running.Tools.Disabled, []string{"shell"}) || running.Agency.MaxSubagentDepth == depth {
		t.Fatalf("a refused reload changed the running config: disabled %q, depth %d", running.Tools.Disabled, running.Agency.MaxSubagentDepth)
	}
	if !strings.Contains(out, `tools.disabled: "shel"`) {
		t.Fatalf("the refusal did not say why:\n%s", out)
	}
}

func TestADisabledBuiltinIsWithheldFromTheTurn(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Posture")
	cfg := safebootConfig(t, dir, "Posture", keyPath, ledgerPath, dbPath)
	cfg.Tools.Disabled = []string{"shell"}
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	const offered = "pl_org_example_docs_read"
	if err := a.toolReg.RegisterDynamic(&primaryOp{fakeOp{name: offered, out: `{}`}}, "org.example.docs"); err != nil {
		t.Fatal(err)
	}

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if obs.add(string(body)) == 1 {
			_, _ = io.WriteString(w, callReply("", toolCall("list", "tools", `{}`), toolCall("stale", "shell", `{"command":"echo on"}`)))
			return
		}
		_, _ = io.WriteString(w, textReply("done"))
	}))
	t.Cleanup(srv.Close)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	operatorTurn(t, a, "what can you use?")

	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("saw %d requests, want the tool round and the answer", len(reqs))
	}
	var sent [2]struct {
		Messages []llm.Message        `json:"messages"`
		Tools    []llm.ToolDefinition `json:"tools"`
	}
	for i := range sent {
		if err := json.Unmarshal([]byte(reqs[i]), &sent[i]); err != nil {
			t.Fatal(err)
		}
	}
	handed := map[string]bool{}
	for _, d := range sent[0].Tools {
		handed[d.Function.Name] = true
	}
	if handed["shell"] {
		t.Fatal("the provider was handed the shell the operator disabled")
	}
	for _, name := range append(a.toolReg.Builtins(), offered) {
		if name != "shell" && !handed[name] {
			t.Fatalf("%s is enabled and was not handed to the provider", name)
		}
	}

	// The system message's tool guidance names the installed plugin
	// operations, and no withheld tool.
	system := sent[0].Messages[0].Content
	i := strings.Index(system, "# How You Act")
	if i < 0 {
		t.Fatal("the system message carries no tool guidance")
	}
	guidance := system[i+1:]
	if j := strings.Index(guidance, "\n#"); j >= 0 {
		guidance = guidance[:j]
	}
	if strings.Contains(guidance, "shell") || !strings.Contains(guidance, offered) {
		t.Fatalf("the tool guidance must name %s and not the shell:\n%s", offered, guidance)
	}

	var listing, refusal string
	for _, m := range sent[1].Messages {
		switch m.ToolCallID {
		case "list":
			listing = m.Content
		case "stale":
			refusal = m.Content
		}
	}
	if !strings.Contains(listing, "Tools in your sandbox:") || !strings.Contains(listing, "\n  read — ") || strings.Contains(listing, "\n  shell") {
		t.Fatalf("the tools organ must list the sandbox tools without the shell:\n%s", listing)
	}
	if !strings.Contains(refusal, "tool disabled by operator") {
		t.Fatalf("the shell call must be refused: %q", refusal)
	}
	page, err := a.store.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolResult}}})
	if err != nil {
		t.Fatal(err)
	}
	typed := false
	for _, row := range page.Rows {
		typed = typed || (row.Details.Tool == "shell" && row.Details.ReasonCode == tools.ReasonOperatorDisabled)
	}
	if !typed {
		t.Fatalf("the refusal must be recorded with %s: %+v", tools.ReasonOperatorDisabled, page.Rows)
	}
}
