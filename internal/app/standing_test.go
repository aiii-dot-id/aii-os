package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func standingApp(t *testing.T) *App {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a := New(&Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2},
	})
	a.store = st
	a.toolReg = reg
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	return a
}

func workCall(t *testing.T, a *App, args string) (string, bool) {
	t.Helper()
	var c llm.ToolCall
	c.Type = "function"
	c.Function.Name = "work"
	c.Function.Arguments = args
	obs := a.executeToolCall(t.Context(), c)
	return obs.Text, obs.Failed
}

// .
// .
// .
func TestStandingStateNeedsNoWorkSession(t *testing.T) {
	a := standingApp(t)

	// .
	// .
	if txt, failed := workCall(t, a, `{"action":"update","state":"x"}`); !failed {
		t.Fatalf("state= without a session should refuse, got %q", txt)
	}

	// .
	txt, failed := workCall(t, a, `{"action":"update","standing":"holding for the telemetry fire; nothing is blocked on my seat"}`)
	if failed {
		t.Fatalf("standing= must not require a session: %s", txt)
	}
	got, err := a.store.StandingState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "nothing is blocked") {
		t.Fatalf("standing state not stored: %q", got)
	}

	// .
	// .
	// .
	// .
	// .
	txt, failed = workCall(t, a, `{"action":"update","standing":"revised","state":"needs a session"}`)
	if !failed {
		t.Fatalf("a partially applied call must fail, not report success: %s", txt)
	}
	if !strings.Contains(txt, "persists") || !strings.Contains(txt, "NOT applied") {
		t.Fatalf("the failure must name both what landed and what did not: %q", txt)
	}
	if got, _ = a.store.StandingState(); got != "revised" {
		t.Fatalf("the standing half of a partial call must persist, got %q", got)
	}
}

// .
// .
// .
func TestComposeRendersStandingStateAndSuppressesTheGuess(t *testing.T) {
	a := standingApp(t)

	// .
	if err := a.store.StartWorkSession("ws_old", "an earlier arc"); err != nil {
		t.Fatal(err)
	}
	plan := "the old plan from a finished session"
	if err := a.store.UpdateWorkPlan("ws_old", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession("ws_old", "done", "", ""); err != nil {
		t.Fatal(err)
	}

	// .
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "Standing plan") {
		t.Fatalf("the session-derived fallback should render when nothing stands:\n%s", ws)
	}

	// .
	if err := a.store.SetStandingState("waiting on the operator's restart word"); err != nil {
		t.Fatal(err)
	}
	ws, err = a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "Standing state") || !strings.Contains(ws, "restart word") {
		t.Fatalf("standing state did not render:\n%s", ws)
	}
	if strings.Contains(ws, "Standing plan") {
		t.Fatalf("both surfaces rendered — the guess must yield to the authored answer:\n%s", ws)
	}
}

// .
// .
// .
// .
// .
func TestUnreadableStandingStateSaysSoRatherThanReadingAsAbsent(t *testing.T) {
	a := standingApp(t)
	if err := a.store.SetStandingState("holding for the telemetry fire"); err != nil {
		t.Fatal(err)
	}

	// .
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "telemetry fire") {
		t.Fatalf("control: standing state should render:\n%s", ws)
	}

	// .
	if _, err := a.store.DB().Exec(`DROP TABLE standing_state`); err != nil {
		t.Fatal(err)
	}
	ws, err = a.buildWorkState()
	if err != nil {
		t.Fatalf("a failed standing read must not break the whole compose: %v", err)
	}
	if !strings.Contains(ws, "unavailable") {
		t.Errorf("an unreadable standing state must SAY it could not be read, not read as having none:\n%s", ws)
	}
	if strings.Contains(ws, "telemetry fire") {
		t.Errorf("stale content rendered from a failed read:\n%s", ws)
	}
}

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
func TestUnreadableRecentPlanSaysSoRatherThanReadingAsEmpty(t *testing.T) {
	a := standingApp(t)
	if err := a.store.SetStandingState(""); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession("ws_old", "an earlier arc"); err != nil {
		t.Fatal(err)
	}
	plan := "the old plan from a finished session"
	if err := a.store.UpdateWorkPlan("ws_old", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession("ws_old", "done", "", ""); err != nil {
		t.Fatal(err)
	}

	// .
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "the old plan from a finished session") {
		t.Fatalf("control: the fallback should render:\n%s", ws)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if _, err := a.store.DB().Exec(`UPDATE work_sessions SET created_seq = 'not-a-number' WHERE id = 'ws_old'`); err == nil || !strings.Contains(err.Error(), "cannot store TEXT value in INTEGER column") {
		t.Fatalf("STRICT must refuse a text in an integer column at the write: %v", err)
	}
	ws, err = a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "the old plan from a finished session") {
		t.Fatalf("the refused write leaves the plan readable:\n%s", ws)
	}
}
