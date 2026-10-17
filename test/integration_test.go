package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// TestEndToEnd is an integration test that births an identity, composes
// a prompt, sends a real message to an LLM, executes the tool calls it
// makes, and verifies the ledger reflects the conversation.
//
// This uses a live OpenAI-compatible endpoint named by AII_LIVE_ENDPOINT,
// AII_LIVE_API_KEY and AII_LIVE_MODEL.
func TestEndToEnd(t *testing.T) {
	// Live-API test: opt-in via AII_TEST_LIVE=1. A test that always fails
	// in CI trains people to ignore red — guard it instead. (Locally the
	// key can also come from the OpenClaw auth profiles.)
	if os.Getenv("AII_TEST_LIVE") != "1" {
		t.Skip("integration test requires AII_TEST_LIVE=1 (live LLM API)")
	}

	endpoint := trimSpace(os.Getenv("AII_LIVE_ENDPOINT"))
	apiKey := trimSpace(os.Getenv("AII_LIVE_API_KEY"))
	model := trimSpace(os.Getenv("AII_LIVE_MODEL"))
	if endpoint == "" || apiKey == "" || model == "" {
		t.Skip("set AII_LIVE_ENDPOINT, AII_LIVE_API_KEY and AII_LIVE_MODEL for the live end-to-end test")
	}

	dir := t.TempDir()

	// Step 1: Birth the identity.
	//
	root := genesistest.NewRoot(t)
	result, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "IntegrationTest",
		Ring0Bundle: root.Ring0Bundle(t),
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("Birth failed: %v", err)
	}
	t.Logf("Identity born: %s (%s)", result.Name, result.Fingerprint)

	// Step 2: Open store and load Ring 0
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	defer st.Close()

	// Materialize the birth event into the store
	st.Materialize(result.BirthEvent)

	// Load Ring 0 from ledger
	rc, err := genesis.LoadRing0(result.Ledger)
	if err != nil {
		t.Fatalf("LoadRing0 failed: %v", err)
	}

	rings := ring.NewManager()
	_ = rings.SealSafePosture(rc.Content)

	// Step 3: Create LLM client (OpenAI-compatible)
	llmClient := llm.New(&llm.ClientConfig{
		Endpoint:        endpoint,
		APIKey:          apiKey,
		Model:           model,
		MaxOutputTokens: 4096,
	})

	// Step 4: Create tool registry
	toolReg := tools.NewRegistry(dir, nil, tools.Timeouts{})

	// Step 5: Create prompt composer
	composer := prompt.New(rings, 32000)

	// Step 6: Create identity engine
	engine := identity.NewEngine(st, testEventWriter{result.Ledger, st, result.KeyPair}, rings, testDiscoverer{toolReg}, identity.Ports{})

	// Step 7: Record an operator message
	operatorMsg := "Hello! What are your founding principles?"
	if err := engine.RecordConversationTurn("operator", operatorMsg); err != nil {
		t.Fatalf("RecordConversationTurn failed: %v", err)
	}

	// Step 8: Compose prompt
	p, err := composer.Compose("", 0)
	if err != nil {
		t.Fatalf("Compose failed: %v", err)
	}
	t.Logf("Prompt composed: %d sections, ~%d tokens", len(p.Sections), p.TokenEstimate)

	// Step 9: Build messages and call LLM
	messages := []llm.Message{
		{Role: "system", Content: p.Text},
		{Role: "user", Content: operatorMsg},
	}

	ctx := context.Background()
	resp, err := llmClient.Chat(ctx, messages, llm.ChatOptions{ThinkingBudget: 4096})
	if err != nil {
		t.Fatalf("LLM Chat failed: %v", err)
	}

	if len(resp.Choices) == 0 {
		t.Fatalf("No choices in LLM response")
	}

	responseText := resp.Choices[0].Message.Content
	t.Logf("LLM response (%d tokens): %s", resp.Usage.TotalTokens, truncate(responseText, 200))

	if responseText == "" {
		t.Fatal("Empty LLM response")
	}

	// Step 10: Execute the structured tool calls; the text is speech.
	calls := resp.Choices[0].Message.ToolCalls
	t.Logf("%d tool calls in the response", len(calls))

	for _, tc := range calls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		result, err := engine.ExecuteAction(ctx, "tool", tc.Function.Name, args)
		if err != nil {
			t.Logf("Action tool/%s error: %v", tc.Function.Name, err)
		} else if result != "" {
			t.Logf("Action tool/%s: %s", tc.Function.Name, truncate(result, 100))
		}
	}

	// Step 11: Record identity response
	if err := engine.RecordConversationTurn("resident", responseText); err != nil {
		t.Fatalf("Record response failed: %v", err)
	}

	// Step 12: Verify the ledger
	result.Ledger.Close()
	events, err := ledger.ReadAll(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// Birth contributes only ring0.genesis; any later events come from
	// conscious actions. Conversation turns remain store-only.
	if len(events) < 1 {
		t.Errorf("expected at least ring0.genesis, got %d events", len(events))
	}

	t.Logf("Ledger has %d events", len(events))

	// Verify chain
	if _, err := ledger.VerifyChain(filepath.Join(dir, "ledger.jsonl"), result.KeyPair.PublicKey, nil); err != nil {
		t.Fatalf("Chain verification failed: %v", err)
	}
	t.Log("Ledger chain verified ✓")

	// Step 13: Verify store has conversation turns
	turns, err := st.RecentTurns(10)
	if err != nil {
		t.Fatalf("RecentTurns failed: %v", err)
	}

	if len(turns) < 2 {
		t.Errorf("expected at least 2 conversation turns, got %d", len(turns))
	}

	for _, turn := range turns {
		t.Logf("  [%s] %s", turn.Role, truncate(turn.Content, 100))
	}

	// Step 14: Verify stats
	stats, err := st.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	t.Logf("Stats: %d live beliefs, %d conversations, %d experiences, seq %d",
		stats.BeliefCount, stats.ConversationCount, stats.ExperienceCount, stats.LedgerSeq)

	if live, err := st.ListBeliefs(); err == nil {
		if len(live) != int(stats.BeliefCount) {
			t.Errorf("stats/list disagreement: stats report %d beliefs, ListBeliefs returns %d live",
				stats.BeliefCount, len(live))
		}
	} else {
		t.Errorf("ListBeliefs failed: %v", err)
	}
}

func TestRewrapRefusesUnreplayableChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lg.Append(ledger.EventRing0Genesis, 0, map[string]any{
		"name": "unreplayable", "public_key": kp.PublicKeyB64(), "fingerprint": kp.Fingerprint()}, kp); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventBeliefPromote, 2, map[string]any{"id": "missing", "ring": 2}, kp); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replay := func(candidatePath string) error {
		projection, err := store.NewMemory()
		if err != nil {
			return err
		}
		defer projection.Close()
		return projection.ReplayFromFile(candidatePath)
	}

	if _, err := ledger.Rewrap(path, kp, "", replay); err == nil || !strings.Contains(err.Error(), "does not replay") {
		t.Fatalf("rewrap must refuse an unreplayable chain, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("refused rewrap changed the ledger")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// testDiscoverer adapts the registry for the engine (tests adapt, never the runtime).
type testDiscoverer struct{ reg *tools.Registry }

type testEventWriter struct {
	ledger *ledger.Ledger
	store  *store.Store
	key    *crypto.KeyPair
}

func (w testEventWriter) Append(eventType ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	prepared, err := w.ledger.PreparePayload(payload)
	if modelID != "" {
		prepared, err = w.ledger.PreparePayloadWithModel(payload, modelID)
	}
	if err != nil {
		return nil, err
	}
	if err := w.store.ValidateEvent(eventType, ring, prepared.Bytes()); err != nil {
		return nil, err
	}
	evt, err := w.ledger.AppendPrepared(eventType, ring, prepared, w.key)
	if err != nil {
		return nil, err
	}
	return evt, w.store.Materialize(evt)
}

func (t testDiscoverer) Discover(depth int) []identity.ToolInfo {
	infos := t.reg.Discover(depth)
	out := make([]identity.ToolInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, identity.ToolInfo{Name: i.Name, Description: i.Description})
	}
	return out
}
