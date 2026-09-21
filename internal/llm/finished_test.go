package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func replying(t *testing.T, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(&ClientConfig{Endpoint: srv.URL, Model: "fake", NoStream: true})
}

const usage = `,"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

// .
// .
// .
// .
// .
// .
func TestAnUnfinishedReplyIsNotAReply(t *testing.T) {
	text := func(reason string) string {
		return `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"You notice that your operator asks short questions and that you"},"finish_reason":` + reason + `}]` + usage
	}
	for name, tc := range map[string]struct {
		body     string
		finished bool
	}{
		"an ordinary end":                   {text(`"stop"`), true},
		"a provider that omits the reason":  {text(`""`), true},
		"cut off at the output limit":       {text(`"length"`), false},
		"declined by the provider":          {text(`"refusal"`), false},
		"filtered":                          {text(`"content_filter"`), false},
		"a reason this build does not know": {text(`"pause_turn"`), false},
	} {
		got, _, err := replying(t, tc.body).ChatSimple(context.Background(), "s", "u")
		var incomplete *IncompleteResponseError
		switch {
		case tc.finished && (err != nil || got == ""):
			t.Errorf("%s: refused: %v", name, err)
		case !tc.finished && !errors.As(err, &incomplete):
			t.Errorf("%s: came back as a reply (%q, err %v)", name, got, err)
		case !tc.finished && got != "":
			t.Errorf("%s: the fragment was returned beside the error: %q", name, got)
		}
	}
}

// .
// .
// .
func TestAToolCallAnnouncedAndNotThereIsRefused(t *testing.T) {
	body := `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"half a thought"},"finish_reason":"tool_calls"}]` + usage
	if got, _, err := replying(t, body).ChatSimple(context.Background(), "s", "u"); err == nil || got != "" {
		t.Fatalf("came back as a reply: %q err=%v", got, err)
	}
}

// .
// .
// .
func TestAnUnfinishedStructuredReplyIsNotAReply(t *testing.T) {
	tool := ToolDefinition{Type: "function"}
	tool.Function.Name = "commit"
	call := func(reason, args string) string {
		return `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"commit","arguments":` + args + `}}]},"finish_reason":` + reason + `}]` + usage
	}
	if got, _, viaTool, err := replying(t, call(`"tool_calls"`, `"{\"operations\":[]}"`)).ChatStructured(context.Background(), "s", "u", tool); err != nil || !viaTool || got == "" {
		t.Fatalf("a finished tool call: %q viaTool=%v err=%v", got, viaTool, err)
	}
	got, _, _, err := replying(t, call(`"length"`, `"{\"operations\":[{\"op\":\"upsert\",\"id\":\"n1\",\"stat"`)).ChatStructured(context.Background(), "s", "u", tool)
	var incomplete *IncompleteResponseError
	if !errors.As(err, &incomplete) || got != "" {
		t.Fatalf("arguments cut off at the output limit came back as a product: %q err=%v", got, err)
	}
}
