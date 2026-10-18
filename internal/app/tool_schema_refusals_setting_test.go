package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestTheOperatorsNumberOfRefusedToolsReachesTheTurn(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "SeamTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	cfg := &Config{
		Identity:   IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	cfg.Agency.ToolSchemaRefusals = 1
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)

	var mu sync.Mutex
	var lists []int // the length of the tool list in each request the provider was sent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		lists = append(lists, len(body.Tools))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid schema for function: not a 'regex'.","type":"invalid_request_error","param":"tools[0].parameters","code":"invalid_function_parameters"}}`)
	}))
	defer srv.Close()
	turn := func(want int, when string) {
		t.Helper()
		// A client of its own each time: what one turn left out is
		// remembered by the client, and the count is the turn's.
		app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
		mu.Lock()
		lists = nil
		mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatalf("acquire turn: %v", err)
		}
		_, err := app.observeChat(ctx, "anything", func(kind, name, args string) {})
		var refused *llm.ToolSchemaRefusedError
		mu.Lock()
		sent := append([]int(nil), lists...)
		mu.Unlock()
		// One request for each refusal, each list shorter by the tool just left out.
		ok := len(sent) == want
		for i := 1; i < len(sent); i++ {
			ok = ok && sent[i] == sent[i-1]-1
		}
		if !errors.As(err, &refused) || refused.Tool == "" || !ok {
			t.Fatalf("%s: THE TURN SENT REQUESTS WITH %v TOOL(S) AND ENDED WITH %v; want %d request(s), each a tool shorter, ending on the refusal of a named tool", when, sent, err, want)
		}
	}
	turn(2, "started with agency.tool_schema_refusals 1")

	// A reload applies the agency block whole (applyAgency).
	ag := app.configSnapshot().Agency
	ag.ToolSchemaRefusals = 2
	app.applyAgency(ag)
	turn(3, "after a reload set 2")
	ag.ToolSchemaRefusals = 0
	app.applyAgency(ag)
	turn(4, "after a reload set 0, which is the default of 3")

	// THE DOOR TAKES IT, as a row of the settings table: in force for the
	// next turn, and refused outside its range by its key.
	if _, err := app.applyConfigChange(map[string]any{"agency.tool_schema_refusals": float64(1)}); err != nil {
		t.Fatal(err)
	}
	turn(2, "after the door set 1")
	var refusal *CognitionRefusal
	if _, err := app.applyConfigChange(map[string]any{"agency.tool_schema_refusals": float64(maxToolSchemaRefusals + 1)}); !errors.As(err, &refusal) || refusal.Key != "agency.tool_schema_refusals" {
		t.Fatalf("the door took %d tools a turn: %v", maxToolSchemaRefusals+1, err)
	}

	// The loader holds a file's number to the table's bounds and says so;
	// less than nothing is refused, by its key.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"agency":{"tool_schema_refusals":%d}}`, maxToolSchemaRefusals+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if held, err := LoadConfig(path); err != nil || held.Agency.ToolSchemaRefusals != maxToolSchemaRefusals || held.cognitionHeld["agency.tool_schema_refusals"] == "" {
		t.Fatalf("a file stating %d is held to %d and said: %v, in force %d, said %q", maxToolSchemaRefusals+1, maxToolSchemaRefusals, err, held.Agency.ToolSchemaRefusals, held.cognitionHeld["agency.tool_schema_refusals"])
	}
	if err := os.WriteFile(path, []byte(`{"agency":{"tool_schema_refusals":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "agency.tool_schema_refusals") {
		t.Fatalf("a file stating agency.tool_schema_refusals -1 was loaded: %v", err)
	}
}

// AND A SPAWNED RUN TAKES THE SAME NUMBER. A child's loop is built for it,
// and what is not handed to it there it takes as the loop's default whatever
// the operator chose (as the turn's token fence once was). The provider
// refuses the first tool of every request: with the operator's number at 1
// no request of the child's ever carries fewer than one tool less than its
// first; at the default of 3 it would go on to two and three fewer.
func TestASpawnedRunLeavesOutAsManyToolsAsTheOperatorSays(t *testing.T) {
	var mu sync.Mutex
	var lists []int
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		lists = append(lists, len(body.Tools))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid schema for function: not a 'regex'.","type":"invalid_request_error","param":"tools[0].parameters","code":"invalid_function_parameters"}}`)
	}))
	defer fake.Close()

	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5, ToolSchemaRefusals: 1},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: fake.URL, Model: "fake-subagent", MaxOutputTokens: 64, Retries: -1}))
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"review the boundary"}`
	if got := a.executeToolCall(t.Context(), call).Text; !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	// The child's first run: two requests, then its failure on the refusal.
	deadline := time.Now().Add(10 * time.Second)
	sent := func() []int { mu.Lock(); defer mu.Unlock(); return append([]int(nil), lists...) }
	for len(sent()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // room for a third request, which must not come with fewer tools
	got := sent()
	if len(got) < 2 || got[0] < 3 {
		t.Fatalf("rig: the child sent requests with %v tool(s)", got)
	}
	for _, n := range got {
		if n < got[0]-1 {
			t.Fatalf("WITH agency.tool_schema_refusals 1 A SPAWNED RUN LEFT OUT MORE THAN ONE TOOL: its requests carried %v tool(s)", got)
		}
	}
}

func TestWhatAProviderRefusedIsNotSentToItAgainByAnyClientOfTheHosts(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	// Every client the host builds is given the host's one memory.
	cc, err := app.entryTransport(providerEntry{Name: "fixture", URL: "https://fixture.example", APIType: "openai"}, "k")
	if err != nil || cc.ToolRefusals == nil || cc.ToolRefusals != app.toolRefusals || cc.Entry != "fixture" {
		t.Fatalf("a client configuration the host built does not carry the host's memory of refused definitions, or not under its entry's name (%q): %v", cc.Entry, err)
	}

	var mu sync.Mutex
	var lists []int
	refuse := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []json.RawMessage `json:"tools"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		lists = append(lists, len(body.Tools))
		refusing := refuse
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !refusing {
			_, _ = io.WriteString(w, textReply("done"))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid schema for function: not a 'regex'.","type":"invalid_request_error","param":"tools[0].parameters","code":"invalid_function_parameters"}}`)
	}))
	defer srv.Close()
	made := func() *llm.Client {
		return llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, ToolRefusals: app.toolRefusals})
	}
	turn := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := app.acquireTurn(ctx); err != nil {
			t.Fatalf("acquire turn: %v", err)
		}
		_, err := app.observeChat(ctx, "anything", func(kind, name, args string) {})
		return err
	}
	// The first turn meets four refusals (the default leaves out three) and
	// fails; four definitions are now known refused.
	app.llmSwap.Swap(made())
	if err := turn(); err == nil {
		t.Fatal("rig: the first turn was answered")
	}
	mu.Lock()
	first, all := lists[0], len(lists)
	lists, refuse = nil, false
	mu.Unlock()
	if all != 4 {
		t.Fatalf("rig: the first turn sent %d request(s)", all)
	}
	// The same client, the next turn: none of the four is sent.
	if err := turn(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	next := append([]int(nil), lists...)
	lists = nil
	mu.Unlock()
	if len(next) != 1 || next[0] != first-4 {
		t.Fatalf("THE RESIDENT'S NEXT TURN SENT %v TOOL(S); the provider had refused four of the %d, and one request without them was wanted", next, first)
	}
	// A client made anew for the same entry and model: none of the four either.
	app.llmSwap.Swap(made())
	if err := turn(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	again := append([]int(nil), lists...)
	mu.Unlock()
	if len(again) != 1 || again[0] != first-4 {
		t.Fatalf("A CLIENT MADE ANEW FOR THE SAME ENTRY AND MODEL SENT %v TOOL(S); want one request with %d", again, first-4)
	}
}
