package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Wiring test for the dashboard's read surfaces: boot startLive against a
// birthed identity and assert GetIdentity/GetContinuity return REAL data
// (not just non-nil) — the handlers must be bound to the live app state,
// not defaults. (Born from a smoke run where a broken probe sent me
// chasing zeros; the test keeps the wiring pinned.)
func TestDashboardReadSurfacesWiring(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "WiringTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "WiringTest", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Witness:    WitnessConfig{URL: "https://witness.example", IntervalEvents: 5},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	h := app.buildLiveHandler()

	c, err := h.GetContinuity()
	if err != nil {
		t.Fatal(err)
	}
	if c.LedgerSeq != app.ledger.LastSeq() || c.LedgerSeq == 0 {
		t.Fatalf("continuity must read the live ledger: got %d, app has %d", c.LedgerSeq, app.ledger.LastSeq())
	}
	if c.WitnessURL != "https://witness.example" {
		t.Fatalf("continuity must read the configured witness URL, got %q", c.WitnessURL)
	}
	if c.Unanchored != int64(app.ledger.LastSeq()) {
		t.Fatalf("fresh identity: everything is unanchored, got %d", c.Unanchored)
	}

	st, err := h.GetIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if st.Charter != "" || st.TrustLevel != "" {
		t.Fatalf("birth must not project a founding relationship, got charter=%d chars trust=%q", len(st.Charter), st.TrustLevel)
	}
	// sanity: the payload marshals with the dashboard's json tags
	b, _ := json.Marshal(c)
	if !json.Valid(b) || len(b) < 10 {
		t.Fatal("continuity payload must marshal")
	}
}

// THE IDENTITY WAKES: a due timer fires through the REAL scheduler
// (startLive wires TIME + the delivery owner), the floor lands in the
// outbox, and the resident's wake turn speaks through the configured LLM.
func TestTimerWakeEndToEnd(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I am awake."}}]}`))
	}))
	defer llmServer.Close()

	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "WakeTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		Name: "WakeTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", llmServer.URL, "m", "sk-x")
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()

	// A timer due NOW: the scheduler fires it within its next pass.
	res, err := app.engine.ExecuteAction(context.Background(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "wakeup", "tag": "test", "duration": "50ms", "message": "the identity wakes",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(res)

	// Both delivery layers must land: the raw floor and the resident's
	// own wake speech. Returning at the floor would not prove the wake.
	deadline := time.Now().Add(5 * time.Second)
	floor, wake := false, false
	for time.Now().Before(deadline) {
		msgs, _ := app.engine.UndeliveredMessages()
		for _, m := range msgs {
			if strings.HasPrefix(m.ID, "timer_wakeup_") && strings.Contains(m.Content, "the identity wakes") {
				floor = true
			}
			if strings.HasPrefix(m.ID, "wake_wakeup_") && m.Content == "I am awake." {
				wake = true
			}
		}
		if floor && wake {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timer delivery incomplete: floor=%v wake=%v", floor, wake)
}

// newSandboxApp builds the minimal registry, rings, and persisted config
// needed to exercise operator root edits without starting an identity.
func newSandboxApp(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(filepath.Dir(home), "grantme")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Tools:      ToolsConfig{CWD: home},
		SourcePath: filepath.Join(home, "config.json"),
	}
	a := New(cfg)
	a.rings = ring.NewManager()
	a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{})
	return a, work
}

// Sandbox root edits apply directly; the structural wall still refuses
// substrate exposure.
func TestSandboxRootsApplyOnEdit(t *testing.T) {
	a, dir := newSandboxApp(t)
	extra := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(extra, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := a.setSandboxRoots([]string{extra}); err != nil {
		t.Fatalf("an operator root edit applies directly: %v", err)
	}
	_, got := a.toolReg.Roots()
	if len(got) != 1 {
		t.Fatalf("root not applied: %v", got)
	}
	st, err := a.sandboxState()
	if err != nil || len(st.ExtraRoots) != 1 {
		t.Fatalf("state must show the applied root: %+v %v", st, err)
	}
}

func TestSubstratePathsStillRefuse(t *testing.T) {
	a, _ := newSandboxApp(t)
	if err := a.setSandboxRoots([]string{filepath.Dir(a.cfg.Identity.LedgerPath)}); err == nil {
		t.Fatal("the structural wall must refuse exposing the identity substrate — a wall, not a ceremony")
	}
}

func TestSettingsStayLiveOnlyAfterPersistence(t *testing.T) {
	a, extra := newSandboxApp(t)
	blocker := filepath.Join(filepath.Dir(a.cfg.SourcePath), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	a.cfg.SourcePath = filepath.Join(blocker, "config.json")

	if err := a.setSandboxRoots([]string{extra}); err == nil {
		t.Fatal("sandbox persistence failure reported success")
	}
	if _, roots := a.toolReg.Roots(); len(roots) != 0 {
		t.Fatalf("failed sandbox persistence changed live roots: %v", roots)
	}
	if err := a.setToolEnabled("read", false); err == nil {
		t.Fatal("tool persistence failure reported success")
	}
	if !a.toolReg.ToolEnabled("read") {
		t.Fatal("failed tool persistence changed the live registry")
	}
}
