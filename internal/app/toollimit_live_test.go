package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

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
// .
func TestToolLimitMessageAfterToolResult(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	// .
	const limitMessage = "You've reached this turn's round limit. Please respond to me now with what you've found."

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
				t.Skipf("model chose not to call the tool (finish=%s) — the shape needs a real tool result",
					first.Choices[0].FinishReason)
			}

			// .
			// .
			convo = append(convo, first.Choices[0].Message)
			convo = append(convo, llm.Message{Role: "tool", ToolCallID: calls[0].ID, Content: "12:00 UTC"})
			convo = append(convo, llm.Message{Role: "user", Content: limitMessage})

			resp, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools})
			if err != nil {
				t.Fatalf("the loop's own final-iteration shape was REFUSED by %s: %v\n"+
					"loop.go:394 builds this on every turn that exhausts its rounds with pending tool calls",
					tc.kind, err)
			}
			if len(resp.Choices) == 0 {
				t.Fatalf("%s accepted the shape but returned no choice", tc.kind)
			}
			t.Logf("%s ACCEPTS user(tool_result) followed by user(text): %.70q (finish=%s)",
				tc.kind, resp.Choices[0].Message.Content, resp.Choices[0].FinishReason)
		})
	}
}
