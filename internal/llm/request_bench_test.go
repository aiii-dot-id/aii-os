package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A streamed tool call whose arguments arrive in token-sized deltas: a
// 32 KB write of a file, 5 bytes per event.
func BenchmarkReadChatStreamLongArguments(b *testing.B) {
	var sse bytes.Buffer
	sse.WriteString(`data: {"id":"s","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"write","arguments":""}}]}}]}` + "\n\n")
	piece, _ := json.Marshal("x = 1")
	for range (32 << 10) / 5 {
		fmt.Fprintf(&sse, `data: {"id":"s","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":%s}}]}}]}`+"\n\n", piece)
	}
	sse.WriteString(`data: {"id":"s","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n")
	stream := sse.String()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := readChatStream(strings.NewReader(stream)); err != nil {
			b.Fatal(err)
		}
	}
}

// benchRequest is a turn mid-flight: a 40 KB system prompt, 60 KB of
// history in 1 KB / 4 KB exchanges, the current message, three 6 KB
// tool results, and thirteen tools of about 2 KB of schema each.
func benchRequest() ([]Message, []ToolDefinition) {
	text := func(kb int) string { return strings.Repeat("a line of \"quoted\" text\n", kb<<10/24) }
	msgs := []Message{{Role: "system", Content: text(40)}}
	for range 12 {
		msgs = append(msgs, Message{Role: "user", Content: text(1)}, Message{Role: "assistant", Content: text(4)})
	}
	msgs = append(msgs, Message{Role: "user", Content: "read the three files and compare them"})
	for i := range 3 {
		var call ToolCall
		call.ID, call.Type = fmt.Sprintf("call_%d", i), "function"
		call.Function.Name, call.Function.Arguments = "read", `{"path":"/src/file.go"}`
		msgs = append(msgs, Message{Role: "assistant", ToolCalls: []ToolCall{call}}, FormatToolResult(call.ID, text(6)))
	}
	var tools []ToolDefinition
	for i := range 13 {
		props := map[string]any{}
		for p := range 6 {
			props[fmt.Sprintf("param_%d", p)] = map[string]any{"type": "string", "description": strings.Repeat("what this parameter means. ", 11)}
		}
		tools = append(tools, ToolDefinition{Type: "function", Function: ToolFunction{
			Name: fmt.Sprintf("tool_%d", i), Description: strings.Repeat("what this tool does. ", 10),
			Parameters: map[string]any{"type": "object", "properties": props, "required": []string{"param_0"}},
		}})
	}
	return msgs, tools
}

// The provider entries the body is built for: nothing extra; extra keys;
// extra with a legacy cache key; a declared explicit cache.
var benchConfigs = []struct {
	name string
	cfg  ClientConfig
}{
	{"plain", ClientConfig{Model: "m"}},
	{"extra", ClientConfig{Model: "m", Extra: map[string]any{"min_p": 0.05, "repetition_penalty": 1.05}}},
	{"extra+cache-key", ClientConfig{Model: "m", Extra: map[string]any{"min_p": 0.05, "prompt_cache_key": "stable-key"}}},
	{"explicit-cache", ClientConfig{Model: "m", ExplicitCache: true, Cache: &CachePolicy{Mode: "explicit"}}},
}

func benchChatRequest(c *Client, msgs []Message, tools []ToolDefinition) ChatRequest {
	return ChatRequest{Model: c.model, Messages: wireMessages(msgs), Tools: tools,
		ReasoningEffort: c.effortPlan().Wire, MaxTokens: DefaultMaxOutputTokens, Temperature: c.temperature, TopP: c.topP}
}

// The body Chat sends on the first streamed rung, from the typed request
// to the bytes.
func BenchmarkOpenAIRequestBody(b *testing.B) {
	msgs, tools := benchRequest()
	for _, tc := range benchConfigs {
		cfg := tc.cfg
		c := New(&cfg)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				body, err := c.requestBody(benchChatRequest(c, msgs, tools), msgs)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := withStreamFields(body, streamWithUsage); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
