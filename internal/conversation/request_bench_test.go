package conversation

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A tool result under the cap, as most are: multi-byte text, so a rune
// is not a byte.
func BenchmarkTruncateToolResultUnderTheCap(b *testing.B) {
	for _, kb := range []int{8, 64, 1024} {
		result := strings.Repeat("résumé ", kb<<10/8)
		b.Run(fmt.Sprintf("%dKB", kb), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if truncateToolResult(result, len(result), 0) != result {
					b.Fatal("a result under the cap changed")
				}
			}
		})
	}
}

// One fit of a tool round: a 40 KB system prompt, 60 KB of history in
// 1 KB / 4 KB exchanges, the current message, three 6 KB tool results
// and thirteen tools of about 2 KB of schema each — the shape of a turn
// mid-flight. Calm is half the budget; tight is 88 % of it, so the
// warning and the context line both ride the newest result.
func BenchmarkFitRequest(b *testing.B) {
	text := func(kb int) string { return strings.Repeat("a line of \"quoted\" text\n", kb<<10/24) }
	system := text(40)
	msgs := []llm.Message{{Role: "system", Content: system}}
	for range 12 {
		msgs = append(msgs, llm.Message{Role: "user", Content: text(1)}, llm.Message{Role: "assistant", Content: text(4)})
	}
	current := len(msgs)
	msgs = append(msgs, llm.Message{Role: "user", Content: "read the three files and compare them"})
	for i := range 3 {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall(id, "read", `{"path":"/src/file.go"}`)}},
			llm.FormatToolResult(id, text(6)))
	}
	var tools []llm.ToolDefinition
	for i := range 13 {
		props := map[string]any{}
		for p := range 6 {
			props[fmt.Sprintf("param_%d", p)] = map[string]any{"type": "string", "description": strings.Repeat("what this parameter means. ", 11)}
		}
		tools = append(tools, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
			Name: fmt.Sprintf("tool_%d", i), Description: strings.Repeat("what this tool does. ", 10),
			Parameters: map[string]any{"type": "object", "properties": props, "required": []string{"param_0"}},
		}})
	}
	used, err := llm.EstimateInputTokens(msgs, tools)
	if err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		budget int
		noted  bool
	}{{"calm", 2 * used, false}, {"tight", used * 100 / 88, true}} {
		b.Run(c.name, func(b *testing.B) {
			fit := func() []llm.Message {
				m := slices.Clone(msgs)
				st := fitState{current: current}
				if err := fitRequest(&m, &st, system, tools, c.budget, nil); err != nil {
					b.Fatal(err)
				}
				return m
			}
			newest := fit()[len(msgs)-1].Content
			if noted := strings.Contains(newest, contextTightNote) && strings.Contains(newest, contextLinePrefix); noted != c.noted {
				b.Fatalf("the fixture is not %s: the newest result carries the notes = %v", c.name, noted)
			}
			b.ReportAllocs()
			for b.Loop() {
				fit()
			}
		})
	}
}
