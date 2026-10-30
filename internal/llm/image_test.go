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

func TestEachKindRidesAUserMessageInTheDialectsThatWriteIt(t *testing.T) {
	pdf := Image{MediaType: "application/pdf", Name: "brief.pdf", Data: []byte("%PDF-1.7 not a real document"), Tokens: 3000}
	wav := Image{MediaType: "audio/wav", Name: "note.wav", Data: []byte("RIFF....WAVEfmt not a real sound"), Tokens: 2000}
	mp4 := Image{MediaType: "video/mp4", Name: "clip.mp4", Data: []byte("....ftypisom not a real video"), Tokens: 9000}
	b64 := func(img Image) string { return base64.StdEncoding.EncodeToString(img.Data) }
	dataURL := func(img Image) string { return "data:" + img.MediaType + ";base64," + b64(img) }
	send := func(t *testing.T, cfg ClientConfig, files ...Image) (map[string]any, int, error) {
		t.Helper()
		var raw []byte
		asked := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked++
			raw, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusTeapot)
		}))
		defer srv.Close()
		cfg.Endpoint, cfg.APIKey, cfg.Model, cfg.Retries = srv.URL, "key", "model", -1
		_, err := New(&cfg).Chat(context.Background(), []Message{{Role: "user", Content: "what is this?", Images: files}}, ChatOptions{})
		var body map[string]any
		if asked > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("no request body was read: %v", err)
			}
		}
		return body, asked, err
	}
	part := func(t *testing.T, body any, path ...any) map[string]any {
		t.Helper()
		v := body
		for _, p := range path {
			switch k := p.(type) {
			case string:
				v = v.(map[string]any)[k]
			case int:
				v = v.([]any)[k]
			}
		}
		return v.(map[string]any)
	}
	openai := ClientConfig{NoStream: true}
	body, _, _ := send(t, openai, pdf, wav)
	if f := part(t, body, "messages", 0, "content", 1); f["type"] != "file" || part(t, f, "file")["filename"] != "brief.pdf" || part(t, f, "file")["file_data"] != dataURL(pdf) {
		t.Fatalf("chat completions' PDF: %#v", f)
	}
	if a := part(t, body, "messages", 0, "content", 2); a["type"] != "input_audio" || part(t, a, "input_audio")["data"] != b64(wav) || part(t, a, "input_audio")["format"] != "wav" {
		t.Fatalf("chat completions' sound: %#v", a)
	}
	body, _, _ = send(t, ClientConfig{Provider: "anthropic"}, pdf)
	if d := part(t, body, "messages", 0, "content", 0); d["type"] != "document" || part(t, d, "source")["media_type"] != "application/pdf" || part(t, d, "source")["data"] != b64(pdf) {
		t.Fatalf("anthropic's PDF: %#v", d)
	}
	body, _, _ = send(t, ClientConfig{Provider: "chatgpt"}, pdf)
	if f := part(t, body, "input", 0, "content", 1); f["type"] != "input_file" || f["filename"] != "brief.pdf" || f["file_data"] != dataURL(pdf) {
		t.Fatalf("the Responses API's PDF: %#v", f)
	}
	body, _, _ = send(t, ClientConfig{Provider: "gemini"}, pdf, wav, mp4, Image{MediaType: "image/png", Data: []byte("p")})
	for i, img := range []Image{pdf, wav, mp4} {
		if d := part(t, body, "contents", 0, "parts", i, "inlineData"); d["mimeType"] != img.MediaType || d["data"] != b64(img) {
			t.Fatalf("gemini's %s: %#v", img.MediaType, d)
		}
	}
	if text := part(t, body, "contents", 0, "parts", 4); text["text"] != "what is this?" {
		t.Fatalf("gemini's words do not follow the files: %#v", text)
	}
	for _, c := range []struct {
		cfg  ClientConfig
		file Image
	}{
		{ClientConfig{Provider: "anthropic"}, wav}, {ClientConfig{Provider: "chatgpt"}, wav}, {openai, mp4},
		{ClientConfig{Provider: "gemini"}, Image{MediaType: "image/gif", Name: "a.gif", Data: []byte("GIF89a")}},
	} {
		if _, asked, err := send(t, c.cfg, c.file); asked != 0 || err == nil || !strings.Contains(err.Error(), "writes no "+c.file.MediaType) {
			t.Fatalf("%s handed %s: asked %d, %v", c.cfg.Provider, c.file.MediaType, asked, err)
		}
	}
	for d, want := range map[Dialect]map[string]bool{
		DialectOpenAI:    {"image/png": true, "application/pdf": true, "audio/wav": true, "audio/mpeg": true, "audio/ogg": false, "video/mp4": false},
		DialectAnthropic: {"image/gif": true, "application/pdf": true, "audio/wav": false, "video/mp4": false},
		DialectResponses: {"image/webp": true, "application/pdf": true, "audio/mpeg": false},
		DialectGemini:    {"image/png": true, "image/gif": false, "application/pdf": true, "audio/ogg": true, "video/webm": true, "text/plain": false},
	} {
		for mediaType, writes := range want {
			if Writes(d, mediaType) != writes {
				t.Errorf("Writes(%s, %s) = %v", d, mediaType, !writes)
			}
		}
	}
}
