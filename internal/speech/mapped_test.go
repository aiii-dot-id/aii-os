package speech

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// .
// .
// .

// .
// .
// .
func TestAMultipartMappingShapesEveryPart(t *testing.T) {
	var seenModel, seenAuth, audioType, configType, config string
	var audio []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenModel, seenAuth = r.Header.Get("X-AAI-Model"), r.Header.Get("Authorization")
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("not multipart: %v", err)
			return
		}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "audio":
				audio, audioType = b, p.Header.Get("Content-Type")
			case "config":
				config, configType = string(b), p.Header.Get("Content-Type")
			}
		}
		_, _ = w.Write([]byte(`{"text":" call me back "}`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "universal-3-5-pro", APIKey: "aai",
		Service: svc(t, `{"path":"/transcribe","auth":{"in":"header","name":"Authorization"},
			"audio_field":"audio","fields":{},"headers":{"X-AAI-Model":"{model}"},
			"parts":{"config":{"language_code":"{language}","punctuate":true}}}`)})
	res, err := c.Transcribe(context.Background(), make([]byte, 640), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "call me back" {
		t.Errorf("text = %q", res.Text)
	}
	if seenModel != "universal-3-5-pro" || seenAuth != "aai" {
		t.Errorf("model header %q, auth %q", seenModel, seenAuth)
	}
	if string(audio[:4]) != "RIFF" || audioType != "audio/wav" {
		t.Errorf("audio part is %q typed %q", audio[:4], audioType)
	}
	if configType != "application/json" || strings.Contains(config, "language_code") || !strings.Contains(config, `"punctuate":true`) {
		t.Errorf("config part %q typed %q", config, configType)
	}
}

// .
// .
func TestARawMappingSendsTheWAVAsTheBody(t *testing.T) {
	var body []byte
	var ctype, auth, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		ctype, auth, query = r.Header.Get("Content-Type"), r.Header.Get("Authorization"), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":{"channels":[{"alternatives":[{"transcript":"four thirty tomorrow"}]}]}}`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "nova-3", Language: "en", APIKey: "dg",
		Service: svc(t, `{"path":"/v1/listen","encoding":"raw","auth":{"in":"header","name":"Authorization","scheme":"Token"},
			"query":{"model":"{model}","language":"{language}"},
			"response":{"text":"$.results.channels[0].alternatives[0].transcript"}}`)})
	res, err := c.Transcribe(context.Background(), make([]byte, 640), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "four thirty tomorrow" {
		t.Errorf("text = %q", res.Text)
	}
	if string(body[:4]) != "RIFF" || binary.LittleEndian.Uint32(body[24:28]) != 48000 || ctype != "audio/wav" {
		t.Errorf("body %q at %d Hz typed %q", body[:4], binary.LittleEndian.Uint32(body[24:28]), ctype)
	}
	if auth != "Token dg" || query != "language=en&model=nova-3" {
		t.Errorf("auth %q query %q", auth, query)
	}
}

// .
// .
func TestAJSONMappingCarriesTheAudioInline(t *testing.T) {
	var got map[string]any
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("x-goog-api-key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hello there"}]}}]}`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "gemini-3.5-transcribe", APIKey: "g",
		Service: svc(t, `{"path":"/v1beta/models/{model}:generateContent","encoding":"json",
			"auth":{"in":"header","name":"x-goog-api-key"},
			"body":{"contents":[{"parts":[{"inline_data":{"mime_type":"audio/wav","data":"{audio_base64}"}}]}]},
			"response":{"text":"$.candidates[0].content.parts[0].text"}}`)})
	res, err := c.Transcribe(context.Background(), make([]byte, 640), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello there" || key != "g" {
		t.Fatalf("text %q key %q", res.Text, key)
	}
	data, _ := lookup(any(got), "$.contents[0].parts[0].inline_data.data")
	wav, err := base64.StdEncoding.DecodeString(data.(string))
	if err != nil || string(wav[:4]) != "RIFF" {
		t.Fatalf("the inline audio is not a base64 WAV: %v", err)
	}
}

// .
func TestATextAnswerIsTheTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("  plain words \n"))
	}))
	defer srv.Close()
	res, err := New(Config{Endpoint: srv.URL, Model: "m", Service: svc(t, `{"response":{"kind":"text"}}`)}).
		Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err != nil || res.Text != "plain words" {
		t.Fatalf("text %q err %v", res.Text, err)
	}
}

// .
// .
func TestAMappingThatMissesTheAnswerSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"transcript":"here it is"}`))
	}))
	defer srv.Close()
	_, err := New(Config{Endpoint: srv.URL, Model: "m"}).Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err == nil || !strings.Contains(err.Error(), "$.text") {
		t.Fatalf("a missed answer was not named: %v", err)
	}
}
