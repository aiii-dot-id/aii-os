package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// wrappedBytesTool answers with foreign text inside the untrusted
// boundary, as a plugin's output is, holding a byte that is not UTF-8.
type wrappedBytesTool struct{ body string }

func (wrappedBytesTool) Name() string               { return "wrapped_bytes" }
func (wrappedBytesTool) Description() string        { return "test" }
func (wrappedBytesTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (w wrappedBytesTool) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Output: untrusted.Wrap("plugin example", w.body)}, nil
}

// seenScript asks for one call, then keeps what the model was sent.
type seenScript struct {
	argsScript
	sent []llm.Message
}

func (s *seenScript) Chat(ctx context.Context, msgs []llm.Message, o llm.ChatOptions) (*llm.Response, error) {
	s.sent = msgs
	return s.argsScript.Chat(ctx, msgs, o)
}

func TestAWrappedResultWhoseBytesWereReplacedKeepsItsOpeningMarker(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	body := "caf\xe9 " + strings.Repeat("the plugin's long answer. ", 400) + "the end"
	a.toolReg.Register(wrappedBytesTool{body: body})
	model := &seenScript{argsScript: argsScript{tool: "wrapped_bytes", args: "{}"}}
	const window = 2000
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil,
		testLoop(conversation.Config{MaxToolResultChars: window}))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatal(err)
	}
	var result string
	for _, m := range model.sent {
		if m.Role == "tool" {
			result = m.Content
		}
	}
	if result == "" {
		t.Fatalf("the model was sent no tool result: %+v", model.sent)
	}
	if !strings.HasPrefix(result, notUTF8Note+"\n") {
		t.Fatalf("the note does not stand before the result, outside its boundary: %.200q", result)
	}
	if !strings.Contains(result, "\n"+untrusted.Open+" source: plugin example\n") {
		t.Fatalf("THE CUT RESULT LOST ITS OPENING MARKER: the model reads foreign text as the host's: %.400q", result)
	}
	if !strings.HasSuffix(result, "the end\n"+untrusted.Close) {
		t.Fatalf("the result's tail and closing marker were not kept: %q", result[max(0, len(result)-200):])
	}
}
