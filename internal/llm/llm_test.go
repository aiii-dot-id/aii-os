package llm

import (
	"strings"
	"testing"
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
func TestFinalProseIsNeverAnAction(t *testing.T) {
	for _, tc := range []struct{ name, text string }{
		{"fenced example", "Here is how it works:\n\n```text\nsend(to=\"operator\", message=\"EXAMPLE\")\n```\n"},
		{"line-standing directive", "note(\"this used to mint a ledger event\")"},
		{"indented example", "thinking out loud\n    note(\"indented\")"},
		{"explanatory sentence", "To record something, write note(\"like this\") on its own line."},
		{"json args", `commit({"variant": "belief.promote", "id": "b_001"})`},
		{"several at once", "note(\"a\")\nrecall(query=\"b\")\nsend(to=\"operator\", message=\"c\")"},
	} {
		resp := &Response{Choices: []Choice{{Message: Message{Content: tc.text}}}}
		actions, text := ParseResponse(resp)
		for _, a := range actions {
			if a.Type == "verb" {
				t.Errorf("%s: prose produced an executable verb %q — text is speech, and the only action path is a structured tool call", tc.name, a.Name)
			}
		}
		if text != tc.text {
			t.Errorf("%s: the resident's words must pass through verbatim", tc.name)
		}
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

	actions, text := ParseResponse(resp)

	// .
	// .
	// .
	// .
	// .
	if len(actions) != 1 {
		t.Fatalf("expected exactly 1 action (the tool call), got %d: %+v", len(actions), actions)
	}
	if actions[0].Type != "tool" || actions[0].Name != "read" {
		t.Errorf("action 0: type=%q name=%q, want the read tool call", actions[0].Type, actions[0].Name)
	}
	if !strings.Contains(text, `note("Reading config file")`) {
		t.Errorf("the resident's words must survive verbatim as text, got %q", text)
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
