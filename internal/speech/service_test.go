package speech

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func svc(t *testing.T, doc string) *Service {
	t.Helper()
	var s Service
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("mapping %s: %v", doc, err)
	}
	return &s
}

// .
// .
func TestTheDialectIsAValidMapping(t *testing.T) {
	var none *Service
	if err := none.Validate(STT); err != nil {
		t.Fatalf("the speech-to-text dialect does not validate: %v", err)
	}
	if err := none.Validate(TTS); err != nil {
		t.Fatalf("the text-to-speech dialect does not validate: %v", err)
	}
}

// .
// .
func TestAMappingOutsideTheClosedSetsIsRefused(t *testing.T) {
	for _, tc := range []struct {
		why  string
		dir  Direction
		doc  string
		want string
	}{
		{"an unknown placeholder", STT, `{"query":{"x":"{secret}"}}`, "unknown placeholder {secret}"},
		{"the key through a placeholder", STT, `{"headers":{"X-Key":"{key}"}}`, "placed by auth"},
		{"text in speech-to-text", STT, `{"fields":{"prompt":"{text}"}}`, "{text}"},
		{"inline audio in a speech-to-text query", STT, `{"query":{"a":"{audio_base64}"}}`, "{audio_base64}"},
		{"a voice in speech-to-text", STT, `{"path":"/v/{voice}"}`, "{voice}"},
		{"an encoding nobody wrote", STT, `{"encoding":"graphql"}`, "not a stt encoding"},
		{"multipart speech", TTS, `{"encoding":"multipart"}`, "not a tts encoding"},
		{"a cookie", STT, `{"auth":{"in":"cookie","name":"k"}}`, `auth.in`},
		{"digest auth", STT, `{"auth":{"in":"header","name":"Authorization","scheme":"Digest"}}`, "auth.scheme"},
		{"a relative path", STT, `{"path":"v1/listen"}`, "must start with /"},
		{"a filter in a path", STT, `{"response":{"text":"$.results[?(@.final)]"}}`, "index and nothing else"},
		{"a wildcard", STT, `{"response":{"text":"$.parts[*].text"}}`, "index and nothing else"},
		{"a recursive descent", STT, `{"response":{"text":"$..text"}}`, "member name"},
		{"an answer kind nobody wrote", STT, `{"response":{"kind":"xml"}}`, "not a stt answer"},
		{"a template with no escaping", TTS, `{"encoding":"template","body":"<speak>{text}</speak>"}`, `escape "xml"`},
		{"a float format", TTS, `{"response":{"format":"pcm_f32le"}}`, "not pcm_s16le or wav"},
		{"fields on a raw body", STT, `{"encoding":"raw","fields":{"model":"{model}"}}`, "multipart only"},
		{"a header that splits", STT, `{"headers":{"X-A":"b\r\nX-Injected: 1"}}`, "not a header"},
	} {
		err := svc(t, tc.doc).Validate(tc.dir)
		if err == nil {
			t.Errorf("%s: accepted", tc.why)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refused for the wrong reason: %v", tc.why, err)
		}
	}
}

// .
// .
// .
func TestABlankPlaceholderLeavesItsFieldOut(t *testing.T) {
	doc, err := renderJSON(json.RawMessage(`{"locales":["{language}"],"model":"{model}","keep":"x-{language}-y"}`), values{"model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["locales"]; ok {
		t.Errorf("an array of blank placeholders was sent: %s", doc)
	}
	if got["model"] != "m" {
		t.Errorf("model = %v", got["model"])
	}
	if got["keep"] != "x--y" {
		t.Errorf("a placeholder inside other text must still expand: %v", got["keep"])
	}

	u, err := requestURL("http://h", "/v1/listen", map[string]string{"model": "{model}", "language": "{language}"}, values{"model": "nova-3"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Has("language") || u.Query().Get("model") != "nova-3" {
		t.Errorf("query = %s", u.RawQuery)
	}
}

// .
func TestAnAnswerIsFoundByItsPath(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"results":{"channels":[{"alternatives":[{"transcript":"hello"}]}]}}`), &doc)
	if got, ok := lookup(doc, "$.results.channels[0].alternatives[0].transcript"); !ok || got != "hello" {
		t.Fatalf("lookup = %v, %v", got, ok)
	}
	for _, missing := range []string{"$.results.channels[1]", "$.results.nope", "$.results.channels.alternatives"} {
		if _, ok := lookup(doc, missing); ok {
			t.Errorf("%s resolved", missing)
		}
	}
}

// .
// .
func TestTheKeyGoesOnlyWhereAuthSays(t *testing.T) {
	for _, tc := range []struct {
		auth       Auth
		key        string
		header     string
		wantHeader string
		wantQuery  string
	}{
		{Auth{In: "header", Name: "Authorization", Scheme: "Bearer"}, "k1", "Authorization", "Bearer k1", ""},
		{Auth{In: "header", Name: "Authorization", Scheme: "Token"}, "k2", "Authorization", "Token k2", ""},
		{Auth{In: "header", Name: "xi-api-key"}, "k3", "xi-api-key", "k3", ""},
		{Auth{In: "query", Name: "key"}, "k4", "", "", "k4"},
		{Auth{In: "header", Name: "xi-api-key"}, "", "xi-api-key", "", ""},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://h/p", nil)
		applyAuth(req, &tc.auth, tc.key)
		if tc.header != "" && req.Header.Get(tc.header) != tc.wantHeader {
			t.Errorf("%+v: %s = %q, want %q", tc.auth, tc.header, req.Header.Get(tc.header), tc.wantHeader)
		}
		if got := req.URL.Query().Get("key"); got != tc.wantQuery {
			t.Errorf("%+v: query key = %q, want %q", tc.auth, got, tc.wantQuery)
		}
	}
}

// .
// .
func TestARedirectIsNotFollowedWithTheKey(t *testing.T) {
	var leaked bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "" {
			leaked = true
		}
		_, _ = w.Write([]byte(`{"text":"captured"}`))
	}))
	defer elsewhere.Close()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/speech-to-text", http.StatusTemporaryRedirect)
	}))
	defer vendor.Close()

	c := New(Config{Endpoint: vendor.URL, Model: "scribe_v2", APIKey: "secret-key",
		Service: svc(t, `{"path":"/v1/speech-to-text","auth":{"in":"header","name":"xi-api-key"}}`)})
	_, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if leaked {
		t.Fatal("the key followed a redirect to another host")
	}
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("a redirect was not reported as the refusal it is: %v", err)
	}
}

// .
// .
func TestAKeyNeverReachesAnError(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"key ` + r.URL.Query().Get("key") + ` is not valid"}`))
	}))
	defer echo.Close()
	mapping := `{"path":"/v1/stt","auth":{"in":"query","name":"key"}}`

	_, err := New(Config{Endpoint: echo.URL, Model: "m", APIKey: "super-secret-1234", Service: svc(t, mapping)}).
		Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err == nil || strings.Contains(err.Error(), "super-secret-1234") {
		t.Fatalf("a refusal carried the key: %v", err)
	}

	// .
	// .
	const odd = "sk+live/9=secret"
	_, err = New(Config{Endpoint: "http://127.0.0.1:1", Model: "m", APIKey: odd, Service: svc(t, mapping)}).
		Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err == nil || strings.Contains(err.Error(), odd) || strings.Contains(err.Error(), url.QueryEscape(odd)) {
		t.Fatalf("a transport failure carried the key: %v", err)
	}
}

// .
// .
func TestABaseReplacesTheEntryURL(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"text":"x"}`))
	}))
	defer srv.Close()
	s := svc(t, `{"base":"`+srv.URL+`/v1beta","path":"/models/{model}:transcribe"}`)
	if err := s.Validate(STT); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Endpoint: "http://127.0.0.1:1/chat-api", Model: "m", Service: s}).
		Transcribe(context.Background(), make([]byte, 320), 16000, 1); err != nil {
		t.Fatal(err)
	}
	if path != "/v1beta/models/m:transcribe" {
		t.Fatalf("path %q", path)
	}
	for _, bad := range []string{"ftp://h/x", "https://", "https://h/x?key=1", "not a url"} {
		if err := svc(t, `{"base":"`+bad+`"}`).Validate(STT); err == nil {
			t.Errorf("base %q accepted", bad)
		}
	}
}

// .
// .
// .
func TestAnObjectLeftEmptyIsLeftOut(t *testing.T) {
	doc, err := renderJSON(json.RawMessage(`{"a":1,"cfg":{"inner":{"langs":["{language}"]}},"keep":{}}`), values{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "cfg") {
		t.Errorf("an object emptied by blanks was sent: %s", doc)
	}
	if !strings.Contains(string(doc), `"keep":{}`) {
		t.Errorf("an object the template wrote empty was dropped: %s", doc)
	}
}

// .
// .
// .
func TestAPartEmptiedByBlanksIsLeftOut(t *testing.T) {
	var form map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
			return
		}
		form = r.MultipartForm.Value
		_, _ = w.Write([]byte(`{"text":"x"}`))
	}))
	defer srv.Close()
	_, err := New(Config{Endpoint: srv.URL, Model: "m",
		Service: svc(t, `{"parts":{"config":{"language_code":"{language}"},"empty":{}}}`)}).
		Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v, sent := form["config"]; sent {
		t.Errorf("a part emptied by blanks was sent: %q", v)
	}
	if v := form["empty"]; len(v) != 1 || v[0] != "{}" {
		t.Errorf("a part the mapping wrote empty was not sent as {}: %q", v)
	}
	if doc, err := renderJSON(json.RawMessage(`{"langs":["{language}"]}`), values{}); err != nil || string(doc) != "{}" {
		t.Errorf("an all-blank document rendered %s (%v), not {}", doc, err)
	}
}

// .
// .
func TestUsesNamesWhatAServiceReads(t *testing.T) {
	var dialect *Service
	if !dialect.Uses(TTS, "voice") || !dialect.Uses(TTS, "model") {
		t.Error("the dialect reads a voice and a model")
	}
	noModel := svc(t, `{"path":"/v0/tts/file","body":{"utterances":[{"text":"{text}"}]},"response":{"format":"wav"}}`)
	if noModel.Uses(TTS, "model") || noModel.Uses(TTS, "voice") {
		t.Error("a body naming neither model nor voice was said to read them")
	}
	inPath := svc(t, `{"path":"/v1/text-to-speech/{voice}","body":{"text":"{text}"}}`)
	if !inPath.Uses(TTS, "voice") {
		t.Error("a voice in the path was missed")
	}
}

// .
// .
func TestARefusalQuotesTheServicesSentence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"You didn't provide an API key.","type":"invalid_request_error","param":null}}`))
	}))
	defer srv.Close()
	_, err := New(Config{Endpoint: srv.URL, Model: "m"}).Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	var r *Refusal
	if !errors.As(err, &r) || r.Status != http.StatusUnauthorized {
		t.Fatalf("a refusal did not say its status: %v", err)
	}
	if r.Said != "You didn't provide an API key." || strings.Contains(err.Error(), "invalid_request_error") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the refusal quoted the envelope, or lost the sentence: %v", err)
	}
}
