package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type notedHeardTools struct {
	*fakeTools
	note string
}

func (f *notedHeardTools) Execute(ctx context.Context, call llm.ToolCall) Observation {
	out := f.fakeTools.Execute(ctx, call)
	out.HostNote = f.note
	return out
}

// A clamped recall limit and the heard-page retention sentence meet at
// the same tool window. Neither may displace the other or expose speech
// outside the untrusted boundary when the window truncates the page.
func TestAHeardReadKeepsItsScopeAndLimitNoteTogether(t *testing.T) {
	const scope = "Untrusted heard context. Explicit read only; what you read is recorded whole in your conversation."
	const note = "limit=80 is over the most one recall returns, and was read as 50."
	raw, err := json.Marshal(struct {
		Scope string           `json:"scope"`
		Rows  []map[string]any `json:"rows"`
	}{scope, []map[string]any{{"text": strings.Repeat("foreign words ", 150)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []int{40, 600} {
		client := &scriptLLM{script: []llm.Response{
			resp("", toolCall("heard-1", "recall", `{"source":"heard","limit":80}`)),
			textResp("read complete"),
		}}
		tools := &notedHeardTools{fakeTools: &fakeTools{results: map[string]string{"recall": string(raw)}}, note: note}
		transcript := &fakeTranscript{limit: 4000}
		loop := New(client, tools, &fakeDefs{}, transcript, nil, testBounds(Config{MaxToolResultChars: window}))
		if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "read"}}); err != nil {
			t.Fatal(err)
		}
		var seen string
		for _, m := range client.requests[1] {
			if m.Role == "tool" {
				seen = m.Content
			}
		}
		if !strings.HasPrefix(seen, note+"\n"+scope+"\n") || strings.Count(seen, note) != 1 || strings.Count(seen, scope) != 1 {
			t.Fatalf("window %d lost or duplicated a host sentence: %q", window, seen)
		}
		open, close := strings.Index(seen, untrusted.Open), strings.LastIndex(seen, untrusted.Close)
		if open < 0 || close <= open || strings.Contains(seen[:open], "foreign words") || strings.Contains(seen[close+len(untrusted.Close):], "foreign words") {
			t.Fatalf("window %d exposed heard speech outside its boundary: %q", window, seen)
		}
		if !strings.Contains(seen, "the whole result is retained in the operator transcript") {
			t.Fatalf("window %d lost whole-record retention notice: %q", window, seen)
		}
	}
}
