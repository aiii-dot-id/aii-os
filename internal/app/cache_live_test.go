package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
func TestCacheLiveReuse(t *testing.T) {
	if os.Getenv("AII_CACHE_LIVE") != "1" {
		t.Skip("set AII_CACHE_LIVE=1 for isolated, quota-consuming provider probes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, model, file string }{
		{"Claude (Max/Pro)", "claude-haiku-4-5", filepath.Join(home, ".claude", ".credentials.json")},
		{"ChatGPT (Plus/Pro)", "gpt-5.6-luna", filepath.Join(home, ".codex", "auth.json")},
		{"OpenAI", "gpt-5.6-luna", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := liveProvider(t, tc.name, tc.model)
			entry.MaxOutputTokens = 512
			entry.ReasoningEffort = ""
			if entry.APIType == "openai" {
				entry.ReasoningEffort = "none"
			}
			entry.ThinkingBudget = 0
			entry.Cache = &llm.CachePolicy{}
			if tc.file != "" {
				before, err := os.ReadFile(tc.file)
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(before)
				t.Cleanup(func() {
					after, err := os.ReadFile(tc.file)
					if err != nil {
						t.Error(err)
						return
					}
					if sha256.Sum256(after) != digest {
						t.Error("credential owner file changed during the probe; credential preservation is unproven")
					}
				})
				if entry.CredentialOptions == nil {
					entry.CredentialOptions = map[string]string{}
				}
				entry.CredentialOptions["file"] = tc.file
			} else {
				if os.Getenv("OPENAI_API_KEY") == "" {
					t.Skip("OPENAI_API_KEY is not available")
				}
				entry.APIKeyEnv = "OPENAI_API_KEY"
			}
			if entry.APIType == "anthropic" {
				entry.Cache.TTL = "1h"
				entry.Cache.Diagnostics = true
				entry.Cache.TailTTL = "5m"
			}
			if entry.Credential == "codex" {
				entry.Cache.Key = fmt.Sprintf("cache-live-%d", time.Now().UnixNano())
			}
			dir := t.TempDir()
			writeTestProviders(t, dir, entry)
			a := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: tc.model, TimeoutSeconds: 90, Retries: -1}, SourcePath: filepath.Join(dir, "config.json")})
			t.Cleanup(a.bgCancel)
			cc, _, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			client := llm.New(&cc)
			var reference strings.Builder
			fmt.Fprintf(&reference, "Synthetic cache acceptance %d. Return only the value of the requested row. Reference data follows.\n", time.Now().UnixNano())
			for i := 0; i < 650; i++ {
				fmt.Fprintf(&reference, "Row %04d has reference value VALUE_%04d. This is immutable synthetic reference data for the acceptance test.\n", i, i)
			}
			stable := reference.String()
			messages := []llm.Message{{Role: "system", Content: stable + "\nCurrent check: A", StableLen: len(stable)}, {Role: "user", Content: "What is the value of row 0042? Reply with only its value."}}
			reads := 0
			previous := ""
			for i := 0; i < 3; i++ {
				if i == 2 {
					messages[0].Content = stable + "\nCurrent check: B"
				}
				ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
				response, err := client.Chat(ctx, messages, llm.ChatOptions{PreviousResponseID: previous})
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Choices) == 0 || !strings.Contains(response.Choices[0].Message.Content, "VALUE_0042") {
					t.Fatal("reference lookup did not produce its independently supplied expected value")
				}
				previous = response.ID
				u := response.Usage
				t.Logf("endpoint=%s model=%s request=%d input=%d read=%d write=%d write_1h=%d duration_ms=%d usage_reported=%t cache_reported=%t response_id=%q request_id=%q", cc.Endpoint, cc.Model, i+1, u.PromptTokens, u.CachedPromptTokens, u.CacheWriteTokens, u.CacheWrite1hTokens, response.Duration.Milliseconds(), u.Reported, u.CacheReadReported, response.ID, response.RequestID)
				if !u.Reported || !u.CacheReadReported {
					t.Error("provider did not report enough usage to qualify cache reuse")
				}
				reads += u.CachedPromptTokens
				if i == 2 && cc.Provider != "chatgpt" && u.CachedPromptTokens == 0 {
					t.Error("changing only the volatile suffix lost the stable-prefix cache")
				}
			}
			if reads == 0 && cc.Provider != "chatgpt" {
				t.Error("fixed three-request workload had no reported cache reuse")
			}
			if cc.Provider == "chatgpt" {
				t.Logf("subscription cache observation: %d reads across three requests; its private automatic-cache routing has no declared hit guarantee", reads)
			}
		})
	}
}
