package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// The exhausted-budget shape, sent to the real endpoints.
//
// internal/conversation/loop.go holds two contradictory beliefs about the
// same rule, twenty lines apart in one function. The tool-budget note is
// deliberately appended to the LAST TOOL RESULT, and the comment above it
// says why: "A new user message here would sit directly after the
// tool-result user message and break the alternating-role rule the
// Anthropic dialect depends on." Then, on the final iteration, the loop
// appends exactly that message:
//
//	messages = append(messages, llm.Message{Role: "user",
//	    Content: "You've reached the tool call limit. ..."})
//
// The Anthropic adapter merges consecutive tool results into one user
// message (anthropic.go:262) but its user case appends unconditionally
// (anthropic.go:267), so on the wire this is user(tool_result) followed
// by user(text).
//
// One of those two beliefs is wrong. If the comment is right, every
// Anthropic-backed identity that exhausts its rounds with pending tool
// calls takes this path and is refused — latent today only because Aeon
// runs the OpenAI dialect. If the comment is wrong, the loop is carrying
// a constraint it does not need, and the note could be a plain message.
//
// A host test cannot decide this: it is a vendor contract, not a Go
// property (AGENTS.md §7). So it is asked of the endpoints themselves,
// in both dialects, with the loop's own literal string.
func TestToolLimitMessageAfterToolResult(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	// The literal the loop sends, copied so a drift in one shows up here.
	const limitMessage = "You've reached the tool call limit. Please respond to me now with what you've found."

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

			// The loop's exhausted-budget construction, exactly: the call,
			// its result, and then the limit message as its own user turn.
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
