package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// workerResultOf runs one sub-agent on the fixture's model, "fake-child"
// on the entry "Fixture", whose own bound is model; the engine's is engine.
// The child makes call (a tool and its JSON arguments), and the result the
// child's model was then sent is returned.
func workerResultOf(t *testing.T, engine, model int, prepare func(a *App, st *store.Store, child string), tool, args string) string {
	t.Helper()
	var calls atomic.Int32
	bodies := make(chan []byte, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case bodies <- body:
		default:
			t.Error("unexpected extra provider request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			quoted, _ := json.Marshal(args)
			_, _ = io.WriteString(w, `{"model":"fake-child","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"`+tool+`","arguments":`+string(quoted)+`}}]},"finish_reason":"tool_calls"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"fake-child","choices":[{"message":{"role":"assistant","content":"served: read"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{SubagentMaxToolRounds: limits.Of(3), SubagentMaxToolCalls: limits.Of(8), SubagentMaxLegs: limits.Of(1), SubagentWallSeconds: 10})
	t.Cleanup(func() { a.bgCancel(); a.bgWG.Wait() })
	a.engine = identity.NewEngine(st, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg}, identity.Ports{WorkState: a.readWorkState, ToolResultChars: engine})
	a.providers.runtime.mu.Lock()
	a.providers.runtime.activeProvider = providerEntry{Name: "Fixture"}
	a.providers.runtime.mu.Unlock()
	a.cfgMu.Lock()
	a.cfg.Prompt.MaxToolResultChars = engine
	a.cfg.Overrides = configWith(t, `{"Fixture":{"models":{"fake-child":{"prompt":{"max_tool_result_chars":`+fmt.Sprint(model)+`}}}}}`).Overrides
	a.cfgMu.Unlock()
	const parent, child = "ws_parent_bound", "ws_child_bound"
	if err := st.StartWorkSession(t.Context(), parent, "resident"); err != nil {
		t.Fatal(err)
	}
	queueChild(t, st, child)
	if err := st.StartWorkSession(t.Context(), child, store.SubagentDescription("own bound")); err != nil {
		t.Fatal(err)
	}
	prepare(a, st, child)
	payload, err := json.Marshal(identity.SubagentRequest{SessionID: child, ParentSessionID: parent, Goal: "make the one call", Depth: 1, Context: "folded", WallSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&subagentHandler{a: a}).RunWork(t.Context(), &store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), MaxRetries: 1}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one tool round trip, got %d requests", calls.Load())
	}
	<-bodies
	var sent struct {
		Messages []llm.Message `json:"messages"`
	}
	if err := json.Unmarshal(<-bodies, &sent); err != nil {
		t.Fatal(err)
	}
	var result string
	for _, m := range sent.Messages {
		if m.Role == "tool" {
			result += m.Content
		}
	}
	return result
}

// A worker on a model with its own LARGER bound reads a page of that size:
// a 15,000-character file comes whole, which the engine's 8,000 cut.
func TestAWorkerOnAModelWithALargerBoundGetsIt(t *testing.T) {
	var lines []string
	for i := 0; len(strings.Join(lines, "\n")) < 15000; i++ {
		lines = append(lines, "a line of the long page, kept whole")
	}
	lines = append(lines, "BIG_FILE_TAIL")
	result := workerResultOf(t, 8000, 20000, func(a *App, _ *store.Store, _ string) {
		home, _ := a.toolReg.Roots()
		if err := os.WriteFile(filepath.Join(home, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "read", `{"file_path":"big.txt"}`)
	if !strings.Contains(result, "BIG_FILE_TAIL") || !strings.Contains(result, "a line of the long page") {
		t.Fatalf("the worker's read was not its own model's size (want the whole page under 20,000): %d chars, tail %v", len(result), strings.Contains(result, "BIG_FILE_TAIL"))
	}
}

func TestAWorkerOnAModelWithASmallerBoundHasReadsFittedToIt(t *testing.T) {
	contract := strings.Repeat("whole criterion\n", 150) + "CHILD_CRITERION_TAIL"
	result := workerResultOf(t, 8000, 1200, func(_ *App, st *store.Store, child string) {
		if err := st.UpdateWorkPlan(t.Context(), child, nil, nil, nil, &contract, nil, nil); err != nil {
			t.Fatal(err)
		}
	}, "work", `{"action":"status"}`)
	if !strings.Contains(result, "status.size") || !strings.Contains(result, "prompt.max_tool_result_chars") || strings.Contains(result, "CHILD_CRITERION_TAIL") {
		t.Fatalf("the worker's status was not fitted to its own smaller bound: %q", compactText(result, 300))
	}
}
