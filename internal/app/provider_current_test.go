package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

func TestCredentialWarningNamesTheAdoptedCredential(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, "creds.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "acc", "refreshToken": "ref",
		// Usable for ten more minutes: inside the warning window.
		"expiresAt": time.Now().Add(oauth.ExpirySkew + 10*time.Minute).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// A unique, absent Keychain service: the fixture file is the only
	// credential this test can read.
	reg := providerRegistry{Providers: []providerEntry{{Name: "Claude", Credential: "claude-code",
		CredentialOptions: map[string]string{"file": credPath, "keychain_service": "aii-os-test-absent-" + dir}}}}
	fillEmbeddedCredentialOptions(&reg)
	onCredential := reg.Providers[0]
	onCredential.URL, onCredential.APIType, onCredential.DefaultModel = "https://claude.example", "anthropic", "m"
	onKey := providerEntry{Name: "Claude", URL: "https://claude.example", APIType: "anthropic", APIKey: "k", DefaultModel: "m"}

	// On the credential, while the file has moved the entry to a key.
	writeTestProviders(t, dir, onKey)
	a := New(&Config{LLM: LLMConfig{Provider: "Claude"}, SourcePath: filepath.Join(dir, "config.json")})
	a.adoptProvider(onCredential)
	if got := a.credentialWarning(); !strings.Contains(got, "becomes unusable in") {
		t.Fatalf("the identity is on a credential ten minutes from unusable and the status says %q", got)
	}

	writeTestProviders(t, dir, onCredential)
	if got := New(&Config{LLM: LLMConfig{Provider: "Claude"}, SourcePath: filepath.Join(dir, "config.json")}).credentialWarning(); got != "" {
		t.Fatalf("no substrate is adopted and the status warns about the file's credential: %q", got)
	}
}

func TestSpawnedChildThinksWithinTheAdoptedBudget(t *testing.T) {
	var mu sync.Mutex
	var budgets []float64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Thinking *struct {
				BudgetTokens float64 `json:"budget_tokens"`
			} `json:"thinking"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		if req.Thinking != nil {
			budgets = append(budgets, req.Thinking.BudgetTokens)
		} else {
			budgets = append(budgets, 0)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"VERDICT: done"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`)
	}))
	t.Cleanup(srv.Close)

	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: limits.Of(2), SubagentMaxToolRounds: limits.Of(2), SubagentMaxToolCalls: limits.Of(8), SubagentMaxLegs: limits.Of(1), SubagentWallSeconds: 5})
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: "anthropic", APIKey: "k", Model: "m",
		MaxOutputTokens: 16000, ThinkingMode: "budget", ThinkingBudget: 7000, Retries: -1}))
	entry := providerEntry{Name: "p", URL: srv.URL, APIType: "anthropic", APIKey: "k", DefaultModel: "m",
		MaxOutputTokens: 16000, ThinkingMode: "budget", ThinkingBudget: 3000}
	a.adoptProvider(entry)
	edited := entry
	edited.ThinkingBudget, edited.Default = 5000, true
	writeTestProviders(t, filepath.Dir(a.cfg.SourcePath), edited)

	waitDelivered(t, st, spawnChild(t, a, st, "say done"))
	mu.Lock()
	defer mu.Unlock()
	if len(budgets) == 0 {
		t.Fatal("the child never called the model")
	}
	for _, b := range budgets {
		if b != 3000 {
			t.Fatalf("a main-thread child thought with budget_tokens %v; the identity runs on 3000 (the file's unadopted 5000 and the client's own 7000 are not its ceiling): %v", b, budgets)
		}
	}
}
