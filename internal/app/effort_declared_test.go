package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
// .
// .
type effortFixture struct {
	a      *App
	dir    string
	entry  providerEntry
	probes atomic.Int32
	// .
	refuse atomic.Bool
	mu     sync.Mutex
	last   map[string]any
}

func newEffortFixture(t *testing.T, model, stored string) *effortFixture {
	t.Helper()
	return newEffortFixtureNamed(t, "Reasoner", model, stored)
}

// .
// .
func newEffortFixtureNamed(t *testing.T, name, model, stored string) *effortFixture {
	t.Helper()
	f := &effortFixture{dir: t.TempDir()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": model}}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Reply with the single word OK") || strings.Contains(string(body), "report_ready") {
			f.probes.Add(1)
			if f.refuse.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"this model does not support that reasoning effort"}}`))
				return
			}
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.last = req
		f.mu.Unlock()
		msg := map[string]any{"role": "assistant", "content": "OK"}
		reason := "stop"
		if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
			reason = "tool_calls"
			msg["tool_calls"] = []any{map[string]any{"id": "c1", "type": "function",
				"function": map[string]any{"name": "report_ready", "arguments": `{"ready":true}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": msg, "finish_reason": reason}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11},
		})
	}))
	t.Cleanup(srv.Close)

	f.entry = providerEntry{Name: name, APIType: "openai", URL: srv.URL, APIKey: "fixture",
		DefaultModel: model, Models: []string{"own", "none", "borrowed", model}, Default: true,
		ContextLength: 64000, MaxOutputTokens: 4096, ReasoningEffort: stored,
		EffortLevels: []string{"low", "medium", "high", "xhigh"}}
	reg := providerRegistry{Providers: []providerEntry{f.entry}, ModelCapabilities: map[string]modelCapability{
		"own":  {Effort: []string{"low", "medium", "high"}},
		"none": {Effort: []string{}},
	}}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: f.entry.Name, Model: model, TimeoutSeconds: 3, Retries: -1}
	cfg.SourcePath = filepath.Join(f.dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	normalized, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	f.a = New(normalized)
	t.Cleanup(f.a.bgCancel)
	f.a.live = true
	f.a.composer = prompt.New(ring.NewManager(), 32000)
	cc, e, err := f.a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	f.a.llmSwap = newSwappableLLM(f.a.newLLMClient(cc, 32000))
	f.a.activeProvider = e
	return f
}

// .
// .
func (f *effortFixture) wireEffort(t *testing.T) any {
	t.Helper()
	if _, err := f.a.llmSwap.Chat(t.Context(), []llm.Message{{Role: "user", Content: "a turn's request"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last["reasoning_effort"]
}

func (f *effortFixture) stored(t *testing.T) string {
	t.Helper()
	reg, err := loadProvidersFile(filepath.Join(f.dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		if e.Name == f.entry.Name {
			return e.ReasoningEffort
		}
	}
	t.Fatalf("no entry named %q", f.entry.Name)
	return ""
}

// .
// .
// .
func TestTheComposerIsOfferedEveryLevelTheWireHonors(t *testing.T) {
	own := []string{"low", "medium", "high"}
	borrowed := []string{"low", "medium", "high", "xhigh"}
	for _, tc := range []struct {
		why, model, stored string
		want               *dashboard.EffortChoice
	}{
		{"its own row, a declared level stored", "own", "high", &dashboard.EffortChoice{Levels: own, InForce: "high"}},
		{"a stored level this model does not take is not sent, so not shown", "own", "xhigh", &dashboard.EffortChoice{Levels: own, InForce: ""}},
		{"nothing stored", "own", "", &dashboard.EffortChoice{Levels: own, InForce: ""}},
		{"its row says it has no effort parameter", "none", "high", nil},
		{"no row: the provider's list, every level checked", "borrowed", "high", &dashboard.EffortChoice{Levels: borrowed, Checked: borrowed, InForce: "high"}},
	} {
		f := newEffortFixture(t, tc.model, tc.stored)
		got := f.a.configState().LLM.EffortChoice
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: effort_choice = %+v, want %+v", tc.why, got, tc.want)
		}
	}
}

// .
// .
// .
func TestABorrowedLevelFromTheComposerIsCheckedFirst(t *testing.T) {
	f := newEffortFixture(t, "borrowed", "low")
	if err := f.a.setActiveEffort("xhigh"); err != nil {
		t.Fatal(err)
	}
	if n := f.probes.Load(); n == 0 {
		t.Fatal("a borrowed level from the composer reached the runtime without the substrate check")
	}
	if got := f.stored(t); got != "xhigh" {
		t.Fatalf("stored effort = %q, want the checked level", got)
	}

	r := newEffortFixture(t, "borrowed", "low")
	r.refuse.Store(true)
	err := r.a.setActiveEffort("high")
	if err == nil || !strings.Contains(err.Error(), "does not support that reasoning effort") {
		t.Fatalf("a level the model rejected was kept, or the model's words were lost: %v", err)
	}
	if got := r.stored(t); got != "low" {
		t.Fatalf("a rejected level changed the file: stored %q", got)
	}
}

// .
// .
// .
func TestAnOlderModelRowGainsTheShippedEffortLevels(t *testing.T) {
	shipped := declaredEffortLevels(newEffectiveCaps(nil), "claude-opus-5")
	if len(shipped) == 0 {
		t.Skip("the shipped registry names no effort levels for claude-opus-5; nothing to lend")
	}
	eff := newEffectiveCaps(&providerRegistry{ModelCapabilities: map[string]modelCapability{
		"claude-opus-5":   {},
		"claude-sonnet-5": {Effort: []string{}},
	}})
	if got := declaredEffortLevels(eff, "claude-opus-5"); !reflect.DeepEqual(got, shipped) {
		t.Fatalf("an older row hides the shipped levels: %v, want %v", got, shipped)
	}
	if got := declaredEffortLevels(eff, "claude-sonnet-5"); len(got) != 0 {
		t.Fatalf("the operator's explicit none was overridden: %v", got)
	}
}

// .
// .
// .
func TestADeclaredEffortLevelIsSavedAtOnceAndReachesTheNextTurn(t *testing.T) {
	f := newEffortFixture(t, "own", "")
	before := f.a.llmSwap.Current()
	if err := f.a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- f.a.setActiveEffort("high") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		f.a.releaseTurn()
		t.Fatal("choosing a declared effort level waited for the running turn")
	}
	if got := f.stored(t); got != "high" {
		t.Fatalf("stored effort = %q, want it saved at once", got)
	}
	if got := f.wireEffort(t); got != nil {
		t.Fatalf("the running turn's request carried effort %v; the change reached it mid-turn", got)
	}

	f.a.releaseTurn()
	deadline := time.Now().Add(3 * time.Second)
	for f.a.llmSwap.Current() == before {
		if time.Now().After(deadline) {
			t.Fatal("the saved level never reached the client after the turn ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.wireEffort(t); got != "high" {
		t.Fatalf("the next turn's request carried effort %v, want high", got)
	}
	if n := f.probes.Load(); n != 0 {
		t.Fatalf("a declared level was re-checked: %d substrate-check requests", n)
	}
}

// .
// .
// .
func TestOnlyADeclaredLevelSkipsTheCheckFromAnySurface(t *testing.T) {
	declared := newEffortFixture(t, "own", "")
	e := declared.entry
	e.ReasoningEffort = "medium"
	if err := declared.a.setProvider(e, true); err != nil {
		t.Fatal(err)
	}
	if n := declared.probes.Load(); n != 0 {
		t.Fatalf("Settings re-checked a level the model declares: %d substrate-check requests", n)
	}

	borrowed := newEffortFixture(t, "borrowed", "")
	e = borrowed.entry
	e.ReasoningEffort = "high"
	if err := borrowed.a.setProvider(e, true); err != nil {
		t.Fatal(err)
	}
	if n := borrowed.probes.Load(); n == 0 {
		t.Fatal("a level from the provider's borrowed list reached the runtime without the substrate check")
	}
}

// .
// .
// .
func TestTheComposerCannotChooseALevelTheModelDoesNotTake(t *testing.T) {
	f := newEffortFixture(t, "own", "low")
	err := f.a.setActiveEffort("xhigh")
	if err == nil || !strings.Contains(err.Error(), "not an effort level") {
		t.Fatalf("a level the model does not take was accepted: %v", err)
	}
	if got := f.stored(t); got != "low" {
		t.Fatalf("a refused level changed the file: stored %q", got)
	}

	n := newEffortFixture(t, "none", "")
	err = n.a.setActiveEffort("high")
	if err == nil || !strings.Contains(err.Error(), "takes no effort level") {
		t.Fatalf("a model with no effort parameter took one from the composer: %v", err)
	}
}

// .
// .
// .
// .
func TestTwoDeclaredChoicesDuringOneTurnBothReturnAndTheLastWins(t *testing.T) {
	f := newEffortFixture(t, "own", "")
	before := f.a.llmSwap.Current()
	if err := f.a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"high", "medium"} {
		done := make(chan error, 1)
		go func() { done <- f.a.setActiveEffort(level) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			f.a.releaseTurn()
			t.Fatalf("choosing %q while an earlier choice waited for the turn held the operator behind it", level)
		}
	}
	if got := f.stored(t); got != "medium" {
		t.Fatalf("stored effort = %q, want the last choice", got)
	}
	f.a.releaseTurn()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if f.a.llmSwap.Current() != before && f.wireEffort(t) == "medium" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the next turn never carried the last choice; it carried %v", f.wireEffort(t))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := f.probes.Load(); n != 0 {
		t.Fatalf("a declared level was re-checked: %d substrate-check requests", n)
	}
}

// .
// .
// .
// .
func TestAShippedLevelIsCheckedOnAnEntryPointedElsewhere(t *testing.T) {
	var declaredModel string
	for id, row := range embeddedRegistry().ModelCapabilities {
		if len(row.Effort) > 1 && strings.HasPrefix(id, "claude-") {
			declaredModel = id
			break
		}
	}
	if declaredModel == "" {
		t.Skip("no shipped claude row names effort levels")
	}
	f := newEffortFixtureNamed(t, "Relay", declaredModel, "")
	e := f.entry
	e.ReasoningEffort = embeddedRegistry().ModelCapabilities[declaredModel].Effort[0]
	if err := f.a.setProvider(e, true); err != nil {
		t.Fatal(err)
	}
	if n := f.probes.Load(); n == 0 {
		t.Fatal("a shipped level reached a relay's runtime without the substrate check")
	}
}
