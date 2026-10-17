package llm

import (
	"encoding/json"
	"testing"
)

// Responses may normalize omitted strictness into a schema where optional
// fields become required. Preserve the author's schema, including omission
// versus null and open objects. Validation remains the tool owner's job.
func TestChatGPTPreservesOptionalToolSchemas(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{"session_id":{"type":"string","minLength":1}},"additionalProperties":false}`,
		`{"type":"object","properties":{"label":{"type":"string"},"options":{"type":"object","properties":{"limit":{"type":"integer"}},"additionalProperties":false}},"required":["label"],"additionalProperties":false}`,
		`{"type":"object","properties":{},"additionalProperties":true}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var schema map[string]any
			if err := json.Unmarshal([]byte(raw), &schema); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(schema)
			srv, got := captureServer(t, streamOneItem+"\n"+streamTerminal)
			client := New(&ClientConfig{Endpoint: srv.URL, APIKey: "fixture", Provider: "chatgpt", Model: "fixture"})
			tools := []ToolDefinition{{Type: "function", Function: ToolFunction{
				Name: "inspect_registry", Description: "Inspect the registry.", Parameters: schema,
			}}}
			// Repeated use must neither lose strict:false nor mutate a schema
			// shared with a later turn or another provider dialect.
			for range 2 {
				if _, err := client.Chat(t.Context(), []Message{{Role: "user", Content: "Inspect the registry."}}, ChatOptions{Tools: tools}); err != nil {
					t.Fatal(err)
				}
				wireTools, ok := (*got)["tools"].([]any)
				if !ok || len(wireTools) != 1 {
					t.Fatalf("missing tool on wire: %v", (*got)["tools"])
				}
				tool := wireTools[0].(map[string]any)
				if strict, present := tool["strict"]; !present || strict != false {
					t.Fatalf("optional arguments must remain optional: strict=%v present=%v", strict, present)
				}
				wireSchema, _ := json.Marshal(tool["parameters"])
				after, _ := json.Marshal(schema)
				if string(wireSchema) != string(before) || string(after) != string(before) {
					t.Fatalf("schema changed: original=%s wire=%s caller=%s", before, wireSchema, after)
				}
			}
		})
	}
}
