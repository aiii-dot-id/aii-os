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
		// This is the model that exposed Anthropic's Claude Code client-version
		// gate. A generic OAuth smoke test on another model did not prove it.
		{"claude-code", "Claude (Max/Pro)", "claude-fable-5-1"},
		{"claude-opus", "Claude (Max/Pro)", "claude-opus-5-5"},
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
			if tc.model == "claude-opus-5-5" && cc.ThinkingMode != "adaptive" {
				t.Fatal("Opus 5.5 did not resolve its captured adaptive thinking request")
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
	for _, tc := range []struct {
		kind, provider, model string
		budget                int
	}{
		{"claude-code", "Claude (Max/Pro)", "claude-haiku-4-5", 0},
		{"claude-opus", "Claude (Max/Pro)", "claude-opus-5-5", 0},

		{"claude-fable", "Claude (Max/Pro)", "claude-fable-5-1", 0},
		{"claude-haiku-thinking", "Claude (Max/Pro)", "claude-haiku-4-5", 2048},
		{"codex", "ChatGPT (Plus/Pro)", "gpt-5.6-luna", 0},
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
				Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
			convo := []llm.Message{{Role: "user", Content: "Call get_time, then tell me the time it returned."}}

			first, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools, ThinkingBudget: tc.budget})
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			t.Logf("resolved: model=%s thinking_mode=%q budget=%d; turn 1 carried %d thinking block(s)",
				cc.Model, cc.ThinkingMode, tc.budget, len(first.Choices[0].Message.Thinking))
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
			second, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools, ThinkingBudget: tc.budget})
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

func TestAdoptedCredentialReturnsReadableReasoning(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	for _, tc := range []struct {
		kind, provider, model string
		budget                int
		shape                 string // the thinking shape the registry names for the model
	}{
		{"claude-opus", "Claude (Max/Pro)", "claude-opus-5-5", 0, "adaptive"},

		{"claude-code", "Claude (Max/Pro)", "claude-fable-5-1", 0, "adaptive"},
		{"claude-sonnet", "Claude (Max/Pro)", "claude-sonnet-5-5", 0, "adaptive"},

		{"claude-haiku", "Claude (Max/Pro)", "claude-haiku-4-5", 2048, "enabled"},
		{"codex", "ChatGPT (Plus/Pro)", "gpt-5.6-luna", 0, ""},
		// A second model on that subscription, and one the registry has no
		// row for: the default asks every model on the Responses shape, and
		// one model's answer to a request parameter is not another's.
		{"codex-astra", "ChatGPT (Plus/Pro)", "gpt-6-astra", 0, ""},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			dir := t.TempDir()
			entry := liveProvider(t, tc.provider, tc.model)
			if entry.ThinkingDisplay != "" {
				t.Fatalf("the shipped entry says %q of readable reasoning; this test proves the default and needs it to say nothing", entry.ThinkingDisplay)
			}
			writeTestProviders(t, dir, entry)
			a := New(&Config{
				LLM:        LLMConfig{Provider: entry.Name, Model: tc.model, TimeoutSeconds: 120},
				SourcePath: filepath.Join(dir, "config.json"),
			})
			cc, _, err := a.resolveLLM()
			if err != nil {
				t.Fatalf("resolveLLM: %v", err)
			}
			plan := llm.PlanReasoningSummary(llm.DialectFor(cc.Provider), cc.ThinkingDisplay)
			if !plan.Sent {
				t.Fatalf("the client is not planned to ask for readable reasoning on %s: %+v", cc.Provider, plan)
			}
			t.Logf("resolved: dialect=%q model=%s thinking_mode=%q asks %s=%q", cc.Provider, cc.Model, cc.ThinkingMode, plan.Field, plan.Wire)
			if llm.DialectFor(cc.Provider) == llm.DialectAnthropic && cc.ThinkingMode != tc.shape {
				t.Fatalf("%s resolved the thinking shape %q, and the registry is to name %q for it", tc.model, cc.ThinkingMode, tc.shape)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			resp, err := llm.New(&cc).Chat(ctx, []llm.Message{{Role: "user", Content: "A train leaves at 14:37 and arrives at 17:12 the same day, after waiting 9 minutes at a signal on the way. How many minutes was it moving? Work it out step by step before you answer, then give the number."}},
				llm.ChatOptions{ThinkingBudget: tc.budget})
			if err != nil {
				t.Fatalf("the endpoint did not serve a request that asks for readable reasoning by default: %v", err)
			}
			if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
				b, _ := json.Marshal(resp)
				t.Fatalf("no content returned: %s", b)
			}
			ch := resp.Choices[0]
			blocks, readable := len(ch.Message.Thinking), len(strings.TrimSpace(ch.ReasoningAccount))
			t.Logf("READABLE REASONING on %s: answer %q; thinking blocks %d; readable characters kept %d",
				tc.model, strings.TrimSpace(ch.Message.Content), blocks, readable)
			switch llm.DialectFor(cc.Provider) {
			case llm.DialectAnthropic:
				if blocks > 0 && readable == 0 {
					t.Fatalf("%s thought (%d blocks) and none of it came back readable: the request asked for %q and the endpoint returned the text left out", tc.model, blocks, plan.Wire)
				}
				// A budget asks for thinking outright; adaptive leaves it to
				// the model, which may answer a small question without any.
				if blocks == 0 && tc.shape == "enabled" {
					t.Fatalf("%s was asked to think with a budget of %d tokens and returned no thinking block", tc.model, tc.budget)
				}
				if blocks == 0 {
					t.Logf("NOTE: %s answered without a thinking block, so nothing was there to read; the request was served, and the summary itself is not shown by this run", tc.model)
				}
			default:
				if readable == 0 {
					t.Logf("NOTE: %s returned no reasoning summary at %q; the request was served, and a summary is the provider's to give", tc.model, plan.Wire)
				}
			}
		})
	}
}
