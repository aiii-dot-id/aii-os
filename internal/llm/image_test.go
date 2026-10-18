package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// AN IMAGE RIDES A USER MESSAGE IN EACH DIALECT, AS THAT DIALECT TAKES ONE.
// The message that shows it carries the picture beside its words; a
// message that shows none is on the wire as it always was. Read from the
// request each client really sends.
func TestAnImageRidesAUserMessageInEachDialect(t *testing.T) {
	img := Image{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\n-not-a-real-picture"), Tokens: 1600}
	b64 := base64.StdEncoding.EncodeToString(img.Data)
	dataURL := "data:image/png;base64," + b64
	messages := []Message{
		{Role: "user", Content: "what is this?", Images: []Image{img}},
		{Role: "assistant", Content: "a page proof"},
		{Role: "user", Content: "and now?"},
	}
	at := func(t *testing.T, v any, path ...any) any {
		t.Helper()
		for _, p := range path {
			switch key := p.(type) {
			case string:
				m, ok := v.(map[string]any)
				if !ok {
					t.Fatalf("at %v: not an object: %#v", path, v)
				}
				v = m[key]
			case int:
				a, ok := v.([]any)
				if !ok || key >= len(a) {
					t.Fatalf("at %v: no element %d in %#v", path, key, v)
				}
				v = a[key]
			}
		}
		return v
	}
	for _, tc := range []struct {
		name  string
		cfg   ClientConfig
		check func(t *testing.T, body any)
	}{
		{"openai-compatible", ClientConfig{NoStream: true}, func(t *testing.T, body any) {
			if got := at(t, body, "messages", 0, "content", 0, "text"); got != "what is this?" || at(t, body, "messages", 0, "content", 0, "type") != "text" {
				t.Fatalf("the words are not the first part: %#v", at(t, body, "messages", 0, "content"))
			}
			if at(t, body, "messages", 0, "content", 1, "type") != "image_url" || at(t, body, "messages", 0, "content", 1, "image_url", "url") != dataURL {
				t.Fatalf("the image is not a data URL part: %#v", at(t, body, "messages", 0, "content", 1))
			}
			if got := at(t, body, "messages", 2, "content"); got != "and now?" {
				t.Fatalf("a message with no image is not a string: %#v", got)
			}
		}},
		{"anthropic", ClientConfig{Provider: "anthropic"}, func(t *testing.T, body any) {
			if at(t, body, "messages", 0, "content", 0, "type") != "image" || at(t, body, "messages", 0, "content", 0, "source", "type") != "base64" ||
				at(t, body, "messages", 0, "content", 0, "source", "media_type") != "image/png" || at(t, body, "messages", 0, "content", 0, "source", "data") != b64 {
				t.Fatalf("the image block: %#v", at(t, body, "messages", 0, "content", 0))
			}
			if at(t, body, "messages", 0, "content", 1, "type") != "text" || at(t, body, "messages", 0, "content", 1, "text") != "what is this?" {
				t.Fatalf("the words do not follow the picture: %#v", at(t, body, "messages", 0, "content"))
			}
			if n := len(at(t, body, "messages", 2, "content").([]any)); n != 1 {
				t.Fatalf("a message with no image has %d blocks", n)
			}
		}},
		{"chatgpt responses", ClientConfig{Provider: "chatgpt"}, func(t *testing.T, body any) {
			if at(t, body, "input", 0, "content", 0, "type") != "input_text" || at(t, body, "input", 0, "content", 0, "text") != "what is this?" {
				t.Fatalf("the words: %#v", at(t, body, "input", 0, "content", 0))
			}
			image := at(t, body, "input", 0, "content", 1).(map[string]any)
			if image["type"] != "input_image" || image["image_url"] != dataURL {
				t.Fatalf("the image: %#v", image)
			}
			if _, has := image["text"]; has {
				t.Fatalf("an input_image carries a text member: %#v", image)
			}
			if n := len(at(t, body, "input", 2, "content").([]any)); n != 1 {
				t.Fatalf("a message with no image has %d contents", n)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusTeapot) // the request is what is read; the answer is not
			}))
			defer srv.Close()
			cfg := tc.cfg
			cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.Retries = srv.URL, "key", "model", -1
			_, _ = New(&cfg).Chat(context.Background(), messages, ChatOptions{})
			var body any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("no request body was read: %v (%d bytes)", err, len(raw))
			}
			tc.check(t, body)
			if strings.Count(string(raw), b64) != 1 {
				t.Fatalf("the picture is in the request %d times, want once", strings.Count(string(raw), b64))
			}
		})
	}
}

// AN IMAGE IS PRICED BY WHAT IT IS SAID TO COST, NEVER BY ITS BYTES. Three
// megabytes of picture priced as text would refuse every request that
// showed one.
func TestAnImageIsPricedByItsTokensNotItsBytes(t *testing.T) {
	words := Message{Role: "user", Content: "what is this?"}
	base, err := EstimateMessageTokens(words)
	if err != nil {
		t.Fatal(err)
	}
	shown := words
	shown.Images = []Image{{MediaType: "image/png", Data: make([]byte, 3<<20), Tokens: 1600}, {MediaType: "image/jpeg", Data: make([]byte, 1<<20), Tokens: 900}}
	got, err := EstimateMessageTokens(shown)
	if err != nil {
		t.Fatal(err)
	}
	if got != base+2500 {
		t.Fatalf("a message with two images is priced %d, want its words (%d) plus 2500", got, base)
	}
	if len(shown.Images) != 2 {
		t.Fatal("pricing a message took its images away")
	}
}

// A MESSAGE THAT SHOWS NOTHING IS ON THE WIRE AS IT ALWAYS WAS: content is
// a string, present even when empty, and every member Message encodes is
// there in Message's own order: a message with every member set is held
// to Message's own encoding.
func TestAMessageWithNoImageIsWiredAsBefore(t *testing.T) {
	call := ToolCall{ID: "call_1", Type: "function"}
	call.Function.Name = "recall"
	call.Function.Arguments = `{"q":"tides"}`
	full := Message{Role: "assistant", Content: "hi <b>&", ToolCallID: "c0", ToolCalls: []ToolCall{call}}
	own, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := json.Marshal(wired(full)); err != nil || string(got) != string(own) {
		t.Fatalf("a message is wired as\n%s (%v)\nand encodes itself as\n%s", got, err, own)
	}
	for _, c := range []struct {
		m    Message
		want string
	}{
		{Message{Role: "user", Content: "hi"}, `{"role":"user","content":"hi"}`},
		{Message{Role: "assistant"}, `{"role":"assistant","content":""}`},
		{Message{Role: "assistant", Content: "x", Reasoning: "r"}, `{"role":"assistant","content":"x","reasoning_content":"r"}`},
		// Only a user message shows a picture.
		{Message{Role: "assistant", Content: "x", Images: []Image{{MediaType: "image/png", Data: []byte("p")}}}, `{"role":"assistant","content":"x"}`},
	} {
		got, err := json.Marshal(wired(c.m))
		if err != nil || string(got) != c.want {
			t.Errorf("wired(%+v) = %s (%v), want %s", c.m, got, err, c.want)
		}
	}
}
