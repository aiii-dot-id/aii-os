package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

// replyUsage is what an answer says its call cost. Most tests read none of
// it and take textReply's and callReply's default; a fake provider whose
// usage a test reads back says its own, through chatReply.
type replyUsage struct{ prompt, completion int }

var defaultReplyUsage = replyUsage{prompt: 10, completion: 5}

// textReply is a finished answer that says text.
func textReply(text string) string {
	return chatReply(text, "stop", nil, defaultReplyUsage)
}

// callReply is an answer that says text (often nothing) and asks for the
// given tool calls (toolCall).
func callReply(text string, calls ...string) string {
	return chatReply(text, "tool_calls", calls, defaultReplyUsage)
}

// toolCall is one call an answer asks for: its id, the tool, and the
// arguments as JSON text.
func toolCall(id, name, args string) string {
	a, _ := json.Marshal(args)
	return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`, id, name, a)
}

// chatReply is the answer itself: what it says, how it ended, the calls it
// asks for, and the usage it reports.
func chatReply(text, finish string, calls []string, u replyUsage) string {
	content, _ := json.Marshal(text)
	message := `{"role":"assistant","content":` + string(content)
	if len(calls) > 0 {
		message += `,"tool_calls":[` + strings.Join(calls, ",") + `]`
	}
	message += `}`
	usage := fmt.Sprintf(`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`, u.prompt, u.completion, u.prompt+u.completion)
	return `{"id":"r","model":"fake","choices":[{"index":0,"message":` + message + `,"finish_reason":"` + finish + `"}],"usage":` + usage + `}`
}
