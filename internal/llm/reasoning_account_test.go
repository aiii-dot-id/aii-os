package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestANormalizedReasoningFieldAloneIsTheProvidersAccount(t *testing.T) {
	serve := map[string]func(w http.ResponseWriter){
		"streamed": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning":"a summary "},"finish_reason":null}]}`)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"reasoning":"of the thinking"},"finish_reason":null}]}`)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
			sseChunk(t, w, "[DONE]")
		},
		"read whole": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"u","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"a summary of the thinking"},"finish_reason":"stop"}]}`))
		},
	}
	for name, write := range serve {
		srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) { write(w) })
		// A replaying client: what it puts on the message is what goes back.
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", ReplayReasoning: true, NoStream: name == "read whole"})
		resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ch := resp.Choices[0]
		if ch.Reasoning != "" {
			t.Fatalf("%s: a normalized reasoning field was taken for raw reasoning: %q", name, ch.Reasoning)
		}
		if ch.ReasoningAccount != "a summary of the thinking" {
			t.Fatalf("%s: the provider's account is %q, want it whole and once", name, ch.ReasoningAccount)
		}
		if ch.Message.Reasoning != "" {
			t.Fatalf("%s: the account was put on the message to be sent back: %q", name, ch.Message.Reasoning)
		}
		if ch.Message.Content != "ok" {
			t.Fatalf("%s: the reply changed: %q", name, ch.Message.Content)
		}
	}
}

// When the server sends both, they are one text: the raw field is the
// record, and the account beside it is left empty, not kept twice.
func TestRawReasoningIsKeptAndItsTwinIsNot(t *testing.T) {
	serve := map[string]func(w http.ResponseWriter){
		"streamed": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"two and two ","reasoning":"two and two "},"finish_reason":null}]}`)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"reasoning_content":"make four","reasoning":"make four"},"finish_reason":null}]}`)
			sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"4"},"finish_reason":"stop"}]}`)
			sseChunk(t, w, "[DONE]")
		},
		"read whole": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"u","choices":[{"index":0,"message":{"role":"assistant","content":"4","reasoning_content":"two and two make four","reasoning":"a shorter version"},"finish_reason":"stop"}]}`))
		},
	}
	for name, write := range serve {
		srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) { write(w) })
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", NoStream: name == "read whole"})
		resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "2+2?"}}, ChatOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := resp.Choices[0].Reasoning; got != "two and two make four" {
			t.Fatalf("%s: raw reasoning is %q", name, got)
		}
		if got := resp.Choices[0].ReasoningAccount; got != "" {
			t.Fatalf("%s: a call that returned raw reasoning also kept an account: %q", name, got)
		}
	}
}

// The normalized field's shape is the server's own. One that is not text
// (null, an object, a list, a number) is passed over: the reply still
// arrives, with no account.
func TestAReasoningFieldThatIsNotTextFailsNothing(t *testing.T) {
	for _, shape := range []string{`null`, `{"summary":"x"}`, `[{"type":"reasoning.text","text":"x"}]`, `7`, `true`} {
		for _, whole := range []bool{false, true} {
			srv, _ := streamedServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
				if whole {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"id":"u","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":` + shape + `},"finish_reason":"stop"}]}`))
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"role":"assistant","reasoning":`+shape+`},"finish_reason":null}]}`)
				sseChunk(t, w, `{"id":"r","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
				sseChunk(t, w, "[DONE]")
			})
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", NoStream: whole})
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err != nil {
				t.Fatalf("reasoning=%s whole=%v: the reply was lost to a field that is not text: %v", shape, whole, err)
			}
			if ch := resp.Choices[0]; ch.Message.Content != "ok" || ch.ReasoningAccount != "" || ch.Reasoning != "" {
				t.Fatalf("reasoning=%s whole=%v: got content %q, account %q, raw %q", shape, whole, ch.Message.Content, ch.ReasoningAccount, ch.Reasoning)
			}
		}
	}
}

// A blank account is no account.
func TestABlankAccountIsNone(t *testing.T) {
	if got := accountBeside("", " \n\t"); got != "" {
		t.Fatalf("a blank account was kept: %q", got)
	}
	if got := accountBeside(" \n", "a summary"); got != "a summary" {
		t.Fatalf("blank raw reasoning hid the account: %q", got)
	}
}

// The anthropic dialect: a thinking block's text is the provider's
// account, a summary when the entry asked for one. By the vendor's
// default the text is empty, and a redacted block has none: no account.
// The blocks themselves are untouched, for replay within the turn.
func TestAnthropicThinkingTextIsTheProvidersAccount(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"summarized": {`{"content":[
			{"type":"thinking","thinking":"weighing the two readings","signature":"sig-1"},
			{"type":"text","text":"here"},
			{"type":"thinking","thinking":"then checking the date","signature":"sig-2"},
			{"type":"text","text":"is the answer"}],"stop_reason":"end_turn"}`, "weighing the two readings\n\nthen checking the date"},
		"the vendor's default: empty text": {`{"content":[
			{"type":"thinking","thinking":"","signature":"sig-1"},
			{"type":"text","text":"here is the answer"}],"stop_reason":"end_turn"}`, ""},
		"redacted": {`{"content":[
			{"type":"redacted_thinking","data":"opaque"},
			{"type":"text","text":"here is the answer"}],"stop_reason":"end_turn"}`, ""},
		"no thinking at all": {`{"content":[{"type":"text","text":"here is the answer"}],"stop_reason":"end_turn"}`, ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
		resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ch := resp.Choices[0]
		if ch.ReasoningAccount != tc.want {
			t.Fatalf("%s: the account is %q, want %q", name, ch.ReasoningAccount, tc.want)
		}
		if ch.Reasoning != "" || ch.Message.Reasoning != "" {
			t.Fatalf("%s: a thinking block was taken for raw reasoning: %q / %q", name, ch.Reasoning, ch.Message.Reasoning)
		}
		if name == "summarized" && (len(ch.Message.Thinking) != 2 || ch.Message.Thinking[0].Signature != "sig-1") {
			t.Fatalf("%s: the blocks were not left as they came: %+v", name, ch.Message.Thinking)
		}
	}
}

const reasoningItemWithSummary = `data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"Checked the ledger head first."},{"type":"summary_text","text":"Then compared the two dates."}]}}
`

// The Responses dialect: a reasoning item's summary is the provider's
// account. The item goes back as it came (NativeContent); an item with no
// summary, or one whose summary is not a list of text parts, gives none
// and fails nothing.
func TestAResponsesReasoningSummaryIsTheProvidersAccount(t *testing.T) {
	resp, err := readResponsesStream(strings.NewReader(reasoningItemWithSummary + "\n" + streamOneItem + "\n" + streamTerminal))
	if err != nil {
		t.Fatal(err)
	}
	ch := resp.Choices[0]
	if want := "Checked the ledger head first.\n\nThen compared the two dates."; ch.ReasoningAccount != want {
		t.Fatalf("the account is %q, want %q", ch.ReasoningAccount, want)
	}
	if ch.Reasoning != "" {
		t.Fatalf("a summary was taken for raw reasoning: %q", ch.Reasoning)
	}
	if ch.Message.Content != "I will start by reading the ledger" {
		t.Fatalf("the reply changed: %q", ch.Message.Content)
	}
	if native := string(ch.Message.NativeContent); !strings.Contains(native, `"encrypted_content":"opaque"`) || !strings.Contains(native, `"summary_text"`) {
		t.Fatalf("the reasoning item did not stay as it came: %s", native)
	}

	for name, item := range map[string]string{
		"no summary":          `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}`,
		"an empty summary":    `{"type":"reasoning","id":"rs_1","summary":[]}`,
		"a blank part":        `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"  "}]}`,
		"a summary of text":   `{"type":"reasoning","id":"rs_1","summary":"not a list"}`,
		"a summary of ints":   `{"type":"reasoning","id":"rs_1","summary":[1,2]}`,
		"a part with no text": `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text"}]}`,
	} {
		stream := `data: {"type":"response.output_item.done","item":` + item + "}\n\n" + streamOneItem + "\n" + streamTerminal
		resp, err := readResponsesStream(strings.NewReader(stream))
		if err != nil {
			t.Fatalf("%s: the reply was lost: %v", name, err)
		}
		if ch := resp.Choices[0]; ch.ReasoningAccount != "" || ch.Message.Content != "I will start by reading the ledger" {
			t.Fatalf("%s: account %q, content %q", name, ch.ReasoningAccount, ch.Message.Content)
		}
	}
}
