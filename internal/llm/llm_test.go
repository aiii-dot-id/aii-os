package llm

import (
	"testing"
)

func TestParseVerbDirectives(t *testing.T) {
	text := `I noticed something interesting.
note("The API returns 200 with data")
Let me also recall what I know.
recall(query="previous API tests")
I should tell the operator.
send(to="operator", message="API is working")`

	actions := parseVerbDirectives(text)

	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}

	if actions[0].Name != "note" {
		t.Errorf("action 0 name = %q, want note", actions[0].Name)
	}
	if actions[0].Args["_positional"] != "The API returns 200 with data" {
		t.Errorf("action 0 args = %v", actions[0].Args)
	}

	if actions[1].Name != "recall" {
		t.Errorf("action 1 name = %q, want recall", actions[1].Name)
	}
	if actions[1].Args["query"] != "previous API tests" {
		t.Errorf("action 1 args = %v", actions[1].Args)
	}

	if actions[2].Name != "send" {
		t.Errorf("action 2 name = %q, want send", actions[2].Name)
	}
	if actions[2].Args["to"] != "operator" {
		t.Errorf("action 2 args[to] = %v", actions[2].Args["to"])
	}

	// Regression (2026-08-17 review): an INLINE mention of a verb is
	// prose, not an act. The resident explaining "to save something,
	// write note(\"like this\")" must not mint a ledger event. Only
	// line-standing directives execute.
	prose := `To record an observation, write note("like this") on its own line.
Earlier I ran recall(query="api") and it worked, then I said send(to="operator", message="hi") as an example.`
	if got := parseVerbDirectives(prose); len(got) != 0 {
		t.Fatalf("inline verb mentions must not parse as actions, got %d: %+v", len(got), got)
	}

	// Indented standalone directives still execute (whitespace-tolerant).
	indented := "thinking out loud\n    note(\"indented act\")"
	if got := parseVerbDirectives(indented); len(got) != 1 || got[0].Name != "note" {
		t.Fatalf("indented standalone directive must parse, got %+v", got)
	}
}

func TestParseToolCalls(t *testing.T) {
	resp := &Response{
		Choices: []Choice{
			{
				Message: Message{
					Content: "Let me read that file.",
					ToolCalls: []ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{
								Name:      "read",
								Arguments: `{"file_path": "/tmp/test.txt"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	actions, text := ParseResponse(resp)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}

	if actions[0].Type != "tool" {
		t.Errorf("action type = %q, want tool", actions[0].Type)
	}
	if actions[0].Name != "read" {
		t.Errorf("action name = %q, want read", actions[0].Name)
	}
	if actions[0].Args["file_path"] != "/tmp/test.txt" {
		t.Errorf("action args = %v", actions[0].Args)
	}

	if text != "Let me read that file." {
		t.Errorf("text = %q", text)
	}
}

func TestParseMixedResponse(t *testing.T) {
	resp := &Response{
		Choices: []Choice{
			{
				Message: Message{
					Content: "I'll read the file and note what I find.\nnote(\"Reading config file\")",
					ToolCalls: []ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							}{
								Name:      "read",
								Arguments: `{"file_path": "config.json"}`,
							},
						},
					},
				},
			},
		},
	}

	actions, _ := ParseResponse(resp)

	// Should have 1 tool call + 1 verb call
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(actions))
	}

	// Tool call first
	if actions[0].Type != "tool" || actions[0].Name != "read" {
		t.Errorf("action 0: type=%q name=%q", actions[0].Type, actions[0].Name)
	}

	// Verb call second
	if actions[1].Type != "verb" || actions[1].Name != "note" {
		t.Errorf("action 1: type=%q name=%q", actions[1].Type, actions[1].Name)
	}
}

func TestParseEmptyResponse(t *testing.T) {
	resp := &Response{
		Choices: []Choice{
			{Message: Message{Content: "Just thinking, no actions."}},
		},
	}

	actions, text := ParseResponse(resp)

	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
	if text != "Just thinking, no actions." {
		t.Errorf("text = %q", text)
	}
}

func TestParseNoChoices(t *testing.T) {
	resp := &Response{}

	actions, text := ParseResponse(resp)

	if actions != nil {
		t.Errorf("expected nil actions, got %d", len(actions))
	}
	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}
}

func TestParseJSONArgs(t *testing.T) {
	text := `commit({"variant": "belief.promote", "id": "b_001", "ring": 2})`

	actions := parseVerbDirectives(text)

	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}

	if actions[0].Name != "commit" {
		t.Errorf("name = %q", actions[0].Name)
	}
	if actions[0].Args["variant"] != "belief.promote" {
		t.Errorf("args[variant] = %v", actions[0].Args["variant"])
	}
	if actions[0].Args["id"] != "b_001" {
		t.Errorf("args[id] = %v", actions[0].Args["id"])
	}
}

func TestBuildMessages(t *testing.T) {
	systemPrompt := "You are a test identity."
	conversation := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
	}
	toolResults := []Message{
		{Role: "tool", Content: "file contents here"},
	}

	msgs := BuildMessages(systemPrompt, conversation, toolResults)

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != systemPrompt {
		t.Error("system message wrong")
	}
	if msgs[3].Role != "tool" {
		t.Error("tool result not at end")
	}
}
