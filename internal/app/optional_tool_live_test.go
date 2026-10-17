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

func TestAdoptedCredentialPreservesOptionalToolArguments(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	dir := t.TempDir()
	entry := liveProvider(t, "ChatGPT (Plus/Pro)", "gpt-5.6-sol")
	writeTestProviders(t, dir, entry)
	a := New(&Config{
		LLM:        LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 90},
		SourcePath: filepath.Join(dir, "config.json"),
	})
	cc, _, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	client := llm.New(&cc)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	tools := []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "inspect_registry", Description: "Read the durable registry. No open session is needed. Omit session_id unless the caller provides an exact one; never invent a value.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"session_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Optional exact session ID; omit to read the durable registry."},
			},
		},
	}}}
	var convo []llm.Message
	for turn := range 2 {
		convo = append(convo, llm.Message{Role: "user", Content: "Inspect the durable registry now. I have no session ID. Call inspect_registry with no arguments, then tell me whether the returned ready value is true."})
		response, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools, RequireTool: true})
		if err != nil {
			t.Fatalf("turn %d tool request: %v", turn+1, err)
		}
		if len(response.Choices) != 1 || len(response.Choices[0].Message.ToolCalls) != 1 {
			t.Fatalf("turn %d must actually call one tool", turn+1)
		}
		message := response.Choices[0].Message
		call := message.ToolCalls[0]
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil || args == nil || len(args) != 0 || call.Function.Name != "inspect_registry" {
			t.Fatalf("turn %d: expected inspect_registry({}), got %s(%s), parse error=%v", turn+1, call.Function.Name, call.Function.Arguments, err)
		}
		convo = append(convo, message, llm.Message{Role: "tool", ToolCallID: call.ID, Content: `{"ready":true}`})
		answer, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools, DisableTools: true})
		if err != nil {
			t.Fatalf("turn %d tool-result continuation: %v", turn+1, err)
		}
		if len(answer.Choices) != 1 || len(answer.Choices[0].Message.ToolCalls) != 0 || strings.TrimSpace(answer.Choices[0].Message.Content) == "" {
			t.Fatalf("turn %d did not complete its answer", turn+1)
		}
		convo = append(convo, answer.Choices[0].Message)
		t.Logf("turn %d: exact empty arguments accepted and tool-result continuation answered", turn+1)
	}
}
