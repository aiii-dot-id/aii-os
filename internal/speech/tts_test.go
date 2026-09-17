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
	"sync"
	"testing"
	"unicode/utf8"
)

// .
// .
func TestTheDialectAsksForPCM(t *testing.T) {
	var body map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(make([]byte, 481))
	}))
	defer srv.Close()

	var got []Audio
	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL + "/v1", Model: "gpt-4o-mini-tts", Voice: "marin", APIKey: "sk"}).
		Synthesize(context.Background(), "Hello.", func(a Audio) error { got = append(got, a); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/audio/speech" || auth != "Bearer sk" {
		t.Errorf("path %q auth %q", path, auth)
	}
	if body["model"] != "gpt-4o-mini-tts" || body["input"] != "Hello." || body["voice"] != "marin" || body["response_format"] != "pcm" {
		t.Errorf("body %v", body)
	}
	if len(got) != 1 || got[0].Rate != 24000 || got[0].Channels != 1 || len(got[0].PCM) != 480 {
		t.Fatalf("audio %+v — an odd trailing byte must be dropped, never played", got)
	}
}

// .
// .
func TestAVoiceInThePathAndAFormatInTheQuery(t *testing.T) {
	var rawPath, query, key string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawPath, query, key = r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("xi-api-key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write(make([]byte, 64))
	}))
	defer srv.Close()

	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL, Model: "eleven_flash_v2_5", Voice: "voice/id 1", APIKey: "el",
		Service: svc(t, `{"path":"/v1/text-to-speech/{voice}","auth":{"in":"header","name":"xi-api-key"},
			"query":{"output_format":"pcm_{rate}"},"body":{"text":"{text}","model_id":"{model}"},
			"response":{"kind":"audio","format":"pcm_s16le","rate":24000}}`)}).
		Synthesize(context.Background(), "Hi", func(Audio) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if rawPath != "/v1/text-to-speech/voice%2Fid%201" {
		t.Errorf("path %q — a voice id must not be able to walk the path", rawPath)
	}
	if query != "output_format=pcm_24000" || key != "el" || body["model_id"] != "eleven_flash_v2_5" || body["text"] != "Hi" {
		t.Errorf("query %q key %q body %v", query, key, body)
	}
}

// .
// .
func TestAWAVAnswerSaysItsOwnRateEvenInsideJSON(t *testing.T) {
	wav := wrapWAV(make([]byte, 400), 22050, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"audio_data":"` + base64.StdEncoding.EncodeToString(wav) + `"}`))
	}))
	defer srv.Close()

	var got Audio
	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL, Model: "voxtral-mini-tts-latest", Voice: "v",
		Service: svc(t, `{"body":{"model":"{model}","input":"{text}","voice_id":"{voice}","response_format":"wav"},
			"response":{"kind":"json_base64","audio":"$.audio_data","format":"wav"}}`)}).
		Synthesize(context.Background(), "x", func(a Audio) error { got = a; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.Rate != 22050 || got.Channels != 2 || len(got.PCM) != 400 {
		t.Fatalf("audio %d Hz %d ch %d bytes", got.Rate, got.Channels, len(got.PCM))
	}
}

// .
// .
func TestATemplateEscapesTheText(t *testing.T) {
	var body, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, ctype = string(b), r.Header.Get("Content-Type")
		_, _ = w.Write(make([]byte, 32))
	}))
	defer srv.Close()

	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL, Voice: "en-US-AvaNeural", APIKey: "az",
		Service: svc(t, `{"path":"/cognitiveservices/v1","encoding":"template","escape":"xml",
			"auth":{"in":"header","name":"Ocp-Apim-Subscription-Key"},
			"headers":{"X-Microsoft-OutputFormat":"raw-24khz-16bit-mono-pcm"},
			"body":"<speak version='1.0' xml:lang='en-US'><voice name='{voice}'>{text}</voice></speak>",
			"response":{"kind":"audio","format":"pcm_s16le","rate":24000}}`)}).
		Synthesize(context.Background(), `Fish & chips <now> "quoted"`, func(Audio) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `Fish &amp; chips &lt;now&gt; &quot;quoted&quot;`) || strings.Contains(body, "<now>") {
		t.Errorf("body %q", body)
	}
	if ctype != "application/ssml+xml" {
		t.Errorf("content type %q", ctype)
	}
}

// .
// .
func TestLongTextIsSpokenInSentenceChunks(t *testing.T) {
	var mu sync.Mutex
	var inputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		inputs = append(inputs, b["input"].(string))
		mu.Unlock()
		_, _ = w.Write(make([]byte, 16))
	}))
	defer srv.Close()

	text := "The release notes are ready. The installer still needs its check! Shall I start? " +
		"Afterwards I will draft the summary for the beta group and send it to you for review."
	emitted := 0
	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL, Model: "m", Voice: "v", MaxChars: 60}).
		Synthesize(context.Background(), text, func(Audio) error { emitted++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if emitted != len(inputs) || len(inputs) < 3 {
		t.Fatalf("%d chunks spoken, %d emitted", len(inputs), emitted)
	}
	for _, in := range inputs {
		if utf8.RuneCountInString(in) > 60 {
			t.Errorf("a chunk exceeds the limit: %q", in)
		}
	}
	if inputs[0] != "The release notes are ready." {
		t.Errorf("the first chunk did not end at the first sentence that fits: %q", inputs[0])
	}
	if got := strings.Join(inputs, " "); got != strings.TrimSpace(text) {
		t.Errorf("chunks lost or reordered words:\n got %q\nwant %q", got, text)
	}
}

// .
func TestAWordLongerThanTheLimitIsStillSpoken(t *testing.T) {
	got := chunkText(strings.Repeat("a", 25), 10)
	if strings.Join(got, "") != strings.Repeat("a", 25) {
		t.Fatalf("chunks %q", got)
	}
	for _, c := range got {
		if len(c) > 10 {
			t.Fatalf("chunk %q exceeds the limit", c)
		}
	}
}

// .
// .
func TestAnAnswerThatIsNotAudioIsNotPlayed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"detail":{"message":"voice not found"}}`))
	}))
	defer srv.Close()
	err := NewSynthesizer(SynthConfig{Endpoint: srv.URL, Model: "m", Voice: "v"}).
		Synthesize(context.Background(), "x", func(Audio) error { t.Fatal("noise was played"); return nil })
	if err == nil || !strings.Contains(err.Error(), "voice not found") {
		t.Fatalf("err %v", err)
	}
}

// .
func TestFloatWAVIsRefusedByName(t *testing.T) {
	wav := wrapWAV(make([]byte, 64), 24000, 1)
	binary.LittleEndian.PutUint16(wav[20:22], 3)
	binary.LittleEndian.PutUint16(wav[34:36], 32)
	_, err := parseWAV(wav)
	if err == nil || !strings.Contains(err.Error(), "32-bit") || !strings.Contains(err.Error(), "16-bit PCM") {
		t.Fatalf("err %v", err)
	}
}

// .
func TestAStreamedWAVPlaysToItsEnd(t *testing.T) {
	wav := wrapWAV(make([]byte, 100), 16000, 1)
	binary.LittleEndian.PutUint32(wav[40:44], 0xFFFFFFFF)
	a, err := parseWAV(wav)
	if err != nil || len(a.PCM) != 100 || a.Rate != 16000 {
		t.Fatalf("audio %d bytes at %d Hz, err %v", len(a.PCM), a.Rate, err)
	}
}
