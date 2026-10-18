package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestAPictureIsTakenByTheRealEndpoints(t *testing.T) {
	if os.Getenv("AII_OAUTH_LIVE") != "1" {
		t.Skip("set AII_OAUTH_LIVE=1 to run (uses the operator's real subscription)")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if x >= 24 && x < 72 && y >= 24 && y < 72 {
				c = color.RGBA{220, 20, 20, 255}
			}
			canvas.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		t.Fatal(err)
	}
	picture := llm.Image{MediaType: "image/png", Data: buf.Bytes(), Tokens: defaultImageTokens}
	const ask = "What colour is the square in the picture? Answer with the one word."

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
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			saysRed := func(shape string, resp *llm.Response, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s REFUSED %s: %v", tc.kind, shape, err)
				}
				if len(resp.Choices) == 0 || !strings.Contains(strings.ToLower(resp.Choices[0].Message.Content), "red") {
					t.Fatalf("%s accepted %s and did not see the picture: %+v", tc.kind, shape, resp.Choices)
				}
				t.Logf("%s TAKES %s: %.60q", tc.kind, shape, resp.Choices[0].Message.Content)
			}

			// 1. A user message that shows an image.
			resp, err := client.Chat(ctx, []llm.Message{{Role: "user", Content: ask, Images: []llm.Image{picture}}}, llm.ChatOptions{})
			saysRed("a user message that shows an image", resp, err)

			// 2. A tool result, then the boundary message that shows one.
			tools := []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
				Name: "read", Description: "Read a file.",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}},
			}}}
			convo := []llm.Message{{Role: "user", Content: "Call read on square.png, then answer: " + ask}}
			first, err := client.Chat(ctx, convo, llm.ChatOptions{Tools: tools})
			if err != nil {
				t.Fatalf("turn 1: %v", err)
			}
			calls := first.Choices[0].Message.ToolCalls
			if len(calls) == 0 {
				t.Skipf("model chose not to call the tool (finish=%s): the shape needs a real tool result", first.Choices[0].FinishReason)
			}
			convo = append(convo, first.Choices[0].Message)
			convo = append(convo, llm.Message{Role: "tool", ToolCallID: calls[0].ID,
				Content: "square.png is an image: image/png, 96 by 96 pixels, 412 bytes.\n[it is shown to you after this round's results]"})
			convo = append(convo, llm.Message{Role: "user", Content: "[sent here: the 1 picture(s) this round's results describe, in the order read]", Images: []llm.Image{picture}})
			resp, err = client.Chat(ctx, convo, llm.ChatOptions{Tools: tools})
			saysRed("user(tool_result) followed by user(text and image)", resp, err)
		})
	}
}
