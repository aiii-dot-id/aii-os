package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// Live end-to-end proof that an ADOPTED subscription credential serves
// inference through the real client. Opt-in: it spends the operator's
// own quota, so it runs only with AII_OAUTH_LIVE=1.
//
//	AII_OAUTH_LIVE=1 go test -run TestAdoptedCredential ./internal/app/
func liveProvider(t *testing.T, name, model string) providerEntry {
	t.Helper()
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name == name {
			entry.DefaultModel = model
			entry.Models = []string{model}
			entry.Default = true
			return entry
		}
	}
	t.Fatalf("embedded provider %q not found", name)
	return providerEntry{}
}

func TestAdoptedCredentialServesInference(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	for _, tc := range []struct {
		kind, provider, model string
	}{
		{"claude-code", "Claude (Max/Pro)", "claude-opus-5"},
		{"codex", "ChatGPT (Plus/Pro)", "gpt-5.6-luna"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			entry := liveProvider(t, tc.provider, tc.model)
			writeTestProviders(t, dir, entry)
			a := New(&Config{
				LLM:        LLMConfig{Provider: entry.Name, Model: tc.model, TimeoutSeconds: 90},
				SourcePath: filepath.Join(dir, "config.json"),
			})
			cc, entry, err := a.resolveLLM()
			if err != nil {
				t.Fatalf("resolveLLM: %v", err)
			}
			if cc.Credential == nil {
				t.Fatal("the entry names a credential source but none was wired")
			}
			if cc.APIKey != "" {
				t.Fatal("an adopted credential must replace the key entirely")
			}
			t.Logf("resolved: dialect=%q endpoint=%s model=%s", cc.Provider, cc.Endpoint, cc.Model)

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			// Discovery must work through the same credential (this is what
			// the use-door needs to let the operator select the provider).
			models, derr := a.discoverForEntry(ctx, entry, "")
			switch {
			case derr != nil:
				t.Errorf("discovery failed: %v", derr)
			default:
				t.Logf("discovery: %d models, first=%q", len(models), models[0])
			}

			// And inference must actually happen.
			client := llm.New(&cc)
			resp, err := client.Chat(ctx, []llm.Message{
				{Role: "user", Content: "Reply with the single word: alive"},
			}, llm.ChatOptions{})
			if err != nil {
				t.Fatalf("chat through the adopted credential failed: %v", err)
			}
			if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
				b, _ := json.Marshal(resp)
				t.Fatalf("no content returned: %s", b)
			}
			t.Logf("INFERENCE OK: %q (finish=%s, tokens in/out %d/%d)",
				strings.TrimSpace(resp.Choices[0].Message.Content),
				resp.Choices[0].FinishReason, resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
		})
	}
}

// Tool use is how an identity does anything, and it is a TWO-turn
// exchange: the call, then the result. The second turn is where the
// ChatGPT dialect broke — a function_call_output whose function_call was
// not echoed back is rejected outright. One turn passing proves nothing.
func TestAdoptedCredentialSurvivesAToolRoundTrip(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	for _, tc := range []struct{ kind, provider, model string }{
		{"claude-code", "Claude (Max/Pro)", "claude-haiku-4-5"},
		{"codex", "ChatGPT (Plus/Pro)", "gpt-5.6-luna"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			entry := liveProvider(t, tc.provider, tc.model)
			writeTestProviders(t, dir, entry)
			a := New(&Config{
				LLM:        LLMConfig{Provider: entry.Name, Model: tc.model, TimeoutSeconds: 90},
				SourcePath: filepath.Join(dir, "config.json"),
			})
			cc, _, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			client := llm.New(&cc)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			tools := []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
				Name: "get_time", Description: "Return the current time.",
				Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			}}}
			convo := []llm.Message{{Role: "user", Content: "Call get_time, then tell me the time it returned."}}

			first, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools})
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			calls := first.Choices[0].Message.ToolCalls
			if len(calls) == 0 {
				t.Skipf("model chose not to call the tool (finish=%s) — nothing to round-trip",
					first.Choices[0].FinishReason)
			}
			t.Logf("turn 1: tool call %q id=%s", calls[0].Function.Name, calls[0].ID)

			// THE SECOND TURN: the assistant's call, then its result.
			convo = append(convo, first.Choices[0].Message)
			convo = append(convo, llm.Message{
				Role: "tool", ToolCallID: calls[0].ID, Content: "12:00 UTC",
			})
			second, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools})
			if err != nil {
				t.Fatalf("turn 2 (the call must be echoed with its result): %v", err)
			}
			if len(second.Choices) == 0 {
				t.Fatal("turn 2 returned no choice")
			}
			t.Logf("turn 2 OK: %.60q (finish=%s)",
				second.Choices[0].Message.Content, second.Choices[0].FinishReason)
		})
	}
}
