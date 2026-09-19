package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/speech"
)

// .
// .
// .
func TestEveryShippedSpeechMappingValidates(t *testing.T) {
	n := 0
	for _, e := range embeddedRegistry().Providers {
		e := e
		if err := validateSpeech(&e); err != nil {
			t.Error(err)
		}
		if e.Speech != nil {
			n++
		}
	}
	if n < 8 {
		t.Fatalf("only %d shipped entries declare speech", n)
	}
}

// .
func retarget(t *testing.T, shipped, srv string) string {
	t.Helper()
	u, err := url.Parse(shipped)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := url.Parse(srv)
	u.Scheme, u.Host = s.Scheme, s.Host
	return u.String()
}

func shippedEntry(t *testing.T, name string) providerEntry {
	t.Helper()
	for _, e := range embeddedRegistry().Providers {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("the shipped registry has no %q", name)
	return providerEntry{}
}

// .
type seen struct {
	method, path, query string
	header              http.Header
	fields              map[string]string
	partTypes           map[string]string
	body                []byte
}

func vendor(t *testing.T, got *seen, ctype string, reply []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query, got.header = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Clone()
		got.fields, got.partTypes = map[string]string{}, map[string]string{}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			mr, err := r.MultipartReader()
			if err != nil {
				t.Errorf("multipart: %v", err)
				return
			}
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				b, _ := io.ReadAll(p)
				got.partTypes[p.FormName()] = p.Header.Get("Content-Type")
				if p.FileName() == "" {
					got.fields[p.FormName()] = string(b)
				}
			}
		} else {
			got.body, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(reply)
	}))
}

func jsonBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body is not JSON: %v: %q", err, b)
	}
	return m
}

func get(doc any, path ...any) any {
	cur := doc
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := cur.(map[string]any)
			cur = m[k]
		case int:
			a, _ := cur.([]any)
			if k >= len(a) {
				return nil
			}
			cur = a[k]
		}
	}
	return cur
}

// .
// .
// .
// .
// .
func TestShippedTranscriptionRequestsAreTheVendorsShapes(t *testing.T) {
	pcm := make([]byte, 640)
	for _, tc := range []struct {
		vendor, model, key string
		reply              string
		check              func(t *testing.T, s seen)
	}{
		{"OpenAI", "gpt-4o-transcribe", "sk", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/v1/audio/transcriptions" && s.header.Get("Authorization") == "Bearer sk", "path %s auth %q", s.path, s.header.Get("Authorization"))
			want(t, s.fields["model"] == "gpt-4o-transcribe" && s.partTypes["file"] == "audio/wav", "fields %v types %v", s.fields, s.partTypes)
		}},
		{"Groq", "whisper-large-v3-turbo", "gq", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/openai/v1/audio/transcriptions" && s.fields["model"] == "whisper-large-v3-turbo", "path %s fields %v", s.path, s.fields)
		}},
		{"ElevenLabs", "scribe_v2", "el", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/v1/speech-to-text" && s.header.Get("xi-api-key") == "el" && s.header.Get("Authorization") == "", "path %s headers %v", s.path, s.header)
			_, hasModel := s.fields["model"]
			want(t, s.fields["model_id"] == "scribe_v2" && !hasModel && s.partTypes["file"] == "audio/wav", "fields %v", s.fields)
			_, hasLang := s.fields["language_code"]
			want(t, !hasLang, "an unset language was sent: %v", s.fields)
		}},
		{"Deepgram", "nova-3", "dg", `{"results":{"channels":[{"alternatives":[{"transcript":"ok"}]}]}}`, func(t *testing.T, s seen) {
			want(t, s.path == "/v1/listen" && s.query == "model=nova-3" && s.header.Get("Authorization") == "Token dg", "path %s query %q auth %q", s.path, s.query, s.header.Get("Authorization"))
			want(t, strings.HasPrefix(string(s.body), "RIFF") && s.header.Get("Content-Type") == "audio/wav", "body %q type %q", s.body[:4], s.header.Get("Content-Type"))
		}},
		{"Cartesia", "", "ca", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/stt" && s.header.Get("Authorization") == "Bearer ca" && s.header.Get("Cartesia-Version") == "2026-08-14", "path %s headers %v", s.path, s.header)
			_, named := s.fields["model"]
			want(t, !named, "Cartesia lists no model, so it is asked for none: %v", s.fields)
		}},
		{"AssemblyAI", "", "aai", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/transcribe" && s.header.Get("Authorization") == "aai" && s.header.Get("X-AAI-Model") == "", "path %s headers %v", s.path, s.header)
			want(t, s.partTypes["audio"] == "audio/wav", "audio part type %q", s.partTypes["audio"])
			_, hasConfig := s.partTypes["config"]
			want(t, !hasConfig, "a config part left empty by an unset language was sent: %v", s.partTypes)
		}},
		{"Mistral", "voxtral-mini-latest", "mi", `{"text":"ok"}`, func(t *testing.T, s seen) {
			want(t, s.path == "/v1/audio/transcriptions" && s.header.Get("Authorization") == "Bearer mi" && s.fields["model"] == "voxtral-mini-latest", "path %s fields %v", s.path, s.fields)
		}},
		{"Google Gemini", "gemini-3.5-transcribe", "go", `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`, func(t *testing.T, s seen) {
			want(t, s.path == "/v1beta/models/gemini-3.5-transcribe:generateContent" && s.header.Get("x-goog-api-key") == "go", "path %s headers %v", s.path, s.header)
			doc := jsonBody(t, s.body)
			data, _ := get(doc, "contents", 0, "parts", 0, "inlineData", "data").(string)
			wav, err := base64.StdEncoding.DecodeString(data)
			want(t, err == nil && strings.HasPrefix(string(wav), "RIFF") && get(doc, "contents", 0, "parts", 0, "inlineData", "mimeType") == "audio/wav", "inline audio %v", doc)
			want(t, get(doc, "generationConfig") == nil, "an unset language left an empty generationConfig: %v", doc)
		}},
	} {
		t.Run(tc.vendor, func(t *testing.T) {
			var s seen
			srv := vendor(t, &s, "application/json", []byte(tc.reply))
			defer srv.Close()
			e := shippedEntry(t, tc.vendor)
			svc := e.Speech.service(speech.STT)
			if svc != nil && svc.Base != "" {
				svc.Base = retarget(t, svc.Base, srv.URL)
			}
			res, err := speech.New(speech.Config{Endpoint: retarget(t, e.URL, srv.URL), Model: tc.model, APIKey: tc.key, Service: svc}).
				Transcribe(context.Background(), pcm, 16000, 1)
			if err != nil {
				t.Fatal(err)
			}
			want(t, s.method == http.MethodPost && res.Text == "ok", "method %s text %q", s.method, res.Text)
			tc.check(t, s)
		})
	}
}

// .
func testWAV(samples, rate int) []byte {
	data := samples * 2
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	return b
}

func TestShippedSpeechRequestsAreTheVendorsShapes(t *testing.T) {
	wav := testWAV(32, 24000)
	for _, tc := range []struct {
		vendor, model, voice string
		ctype                string
		reply                []byte
		check                func(t *testing.T, s seen)
	}{
		{"ElevenLabs", "eleven_multilingual_v2", "21m00Tcm4TlvDq8ikWAM", "audio/pcm", make([]byte, 64), func(t *testing.T, s seen) {
			b := jsonBody(t, s.body)
			want(t, s.path == "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM" && s.query == "output_format=pcm_24000" && s.header.Get("xi-api-key") == "k", "path %s query %q", s.path, s.query)
			want(t, b["text"] == "Hello." && b["model_id"] == "eleven_multilingual_v2", "body %v", b)
		}},
		{"Deepgram", "aura-asteria-en", "", "application/octet-stream", make([]byte, 64), func(t *testing.T, s seen) {
			b := jsonBody(t, s.body)
			q, _ := url.ParseQuery(s.query)
			want(t, s.path == "/v1/speak" && q.Get("model") == "aura-asteria-en" && q.Get("encoding") == "linear16" && q.Get("container") == "none" && q.Get("sample_rate") == "24000", "path %s query %q", s.path, s.query)
			want(t, b["text"] == "Hello." && s.header.Get("Authorization") == "Token k", "body %v auth %q", b, s.header.Get("Authorization"))
		}},
		{"Cartesia", "", "v-1", "application/octet-stream", make([]byte, 64), func(t *testing.T, s seen) {
			b := jsonBody(t, s.body)
			want(t, s.path == "/tts/bytes" && s.header.Get("Cartesia-Version") == "2026-08-14" && s.header.Get("Authorization") == "Bearer k", "path %s headers %v", s.path, s.header)
			// .
			// .
			_, named := b["model_id"]
			want(t, !named && b["transcript"] == "Hello." && get(b, "voice", "id") == "v-1", "body %v", b)
			want(t, get(b, "output_format", "container") == "raw" && get(b, "output_format", "encoding") == "pcm_s16le" && get(b, "output_format", "sample_rate") == float64(24000), "output_format %v", b["output_format"])
		}},
		{"Mistral", "voxtral-mini-tts-latest", "nova", "application/json", []byte(`{"audio_data":"` + base64.StdEncoding.EncodeToString(wav) + `"}`), func(t *testing.T, s seen) {
			b := jsonBody(t, s.body)
			want(t, s.path == "/v1/audio/speech" && b["voice_id"] == "nova" && b["response_format"] == "wav" && b["input"] == "Hello.", "path %s body %v", s.path, b)
		}},
		{"Hume", "", "Ava Song", "audio/wav", wav, func(t *testing.T, s seen) {
			b := jsonBody(t, s.body)
			want(t, s.path == "/v0/tts/file" && s.header.Get("X-Hume-Api-Key") == "k", "path %s headers %v", s.path, s.header)
			want(t, get(b, "utterances", 0, "text") == "Hello." && get(b, "utterances", 0, "voice", "id") == "Ava Song" && get(b, "utterances", 0, "voice", "provider") == "HUME_AI" && get(b, "format", "type") == "wav", "body %v", b)
		}},
	} {
		t.Run(tc.vendor, func(t *testing.T) {
			var s seen
			srv := vendor(t, &s, tc.ctype, tc.reply)
			defer srv.Close()
			e := shippedEntry(t, tc.vendor)
			o := e.Speech.offer(speech.TTS)
			if o == nil {
				t.Fatalf("%s ships no text-to-speech", tc.vendor)
			}
			heard := 0
			err := speech.NewSynthesizer(speech.SynthConfig{Endpoint: retarget(t, e.URL, srv.URL), Model: tc.model, Voice: tc.voice, APIKey: "k",
				MaxChars: o.MaxChars, Service: e.Speech.service(speech.TTS)}).
				Synthesize(context.Background(), "Hello.", func(a speech.Audio) error { heard += len(a.PCM); return nil })
			if err != nil {
				t.Fatal(err)
			}
			want(t, s.method == http.MethodPost && heard > 0, "method %s audio %d bytes", s.method, heard)
			tc.check(t, s)
		})
	}
}

func want(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// .
// .
func TestASpeechOnlyEntryIsNeverOfferedForChat(t *testing.T) {
	if !speechOnly(shippedEntry(t, "ElevenLabs")) || !speechOnly(shippedEntry(t, "Deepgram")) {
		t.Fatal("a shipped speech vendor reads as a chat provider")
	}
	if speechOnly(shippedEntry(t, "OpenAI")) || speechOnly(shippedEntry(t, "Groq")) {
		t.Fatal("a chat provider that also speaks reads as speech-only")
	}
}

// .
// .
// .
// .
func TestAFilledVendorFactIsNeverWrittenBack(t *testing.T) {
	for _, tc := range []struct {
		fact, file, leaked string
		onlyFill           func(*providerRegistry) bool
	}{
		{"a catalogue author",
			`{"providers":[{"name":"zAI","api_type":"openai","url":"https://api.z.ai/api/paas/v4","default_model":"glm-5.3","effort_levels":["low","high"],"api_key":"k"}]}`,
			"catalogue_author",
			func(r *providerRegistry) bool { return len(r.filledAuthor) == 1 && len(r.filledSpeech) == 0 }},
		{"a speech mapping",
			`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","api_key":"el"}]}`,
			`"speech"`,
			func(r *providerRegistry) bool { return len(r.filledSpeech) == 1 && len(r.filledAuthor) == 0 }},
	} {
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// .
		// .
		if !tc.onlyFill(reg) || len(reg.filled)+len(reg.filledEffort)+len(reg.filledOAuth) != 0 {
			t.Fatalf("%s: the file must hold that fill and no other", tc.fact)
		}
		if _, err := saveProvidersFile(path, reg); err != nil {
			t.Fatal(err)
		}
		if written, _ := os.ReadFile(path); strings.Contains(string(written), tc.leaked) {
			t.Errorf("%s lent by the shipped registry was written into the operator's file:\n%s", tc.fact, written)
		}
	}
}

// .
// .
func TestAnOperatorsSpeechBlockSurvivesASettingsEdit(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	own := `{"providers":[{"name":"my-speech","url":"http://127.0.0.1:9","default_model":"m",
		"speech":{"stt":{"path":"/stt"}}}]}`
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.setProvider(providerEntry{Name: "my-speech", URL: "http://127.0.0.1:9", DefaultModel: "m2"}, true); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Providers[0].Speech == nil || reg.Providers[0].Speech.STT.Path != "/stt" {
		t.Fatalf("the edit erased the operator's speech block: %+v", reg.Providers[0].Speech)
	}
}

// .
// .
// .
func TestTextToSpeechAsksOnlyForWhatItsServiceUses(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","api_key":"el"},
		{"name":"Hume","url":"https://api.hume.ai","api_key":"hu"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "ElevenLabs", Model: "eleven_flash_v2_5"}
	if _, err := a.resolveTTS(); err == nil || !strings.Contains(err.Error(), "voice") {
		t.Fatalf("an ElevenLabs voice was not required: %v", err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "Hume", Voice: "Ava Song"}
	if s, err := a.resolveTTS(); err != nil || s == nil {
		t.Fatalf("Hume was refused for a model it does not use: %v", err)
	}
	a.cfg.Speech.TTS = TTSConfig{}
	if s, err := a.resolveTTS(); err != nil || s != nil {
		t.Fatalf("no provider must mean the browser's voice, not an error: %v %v", s, err)
	}
}

// .
// .
func TestABadSpeechMappingSetsTheEntryAsideByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	bad := `{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","speech":{"tts":{"query":{"x":"{secret}"}}}}]}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil || len(reg.Providers) != 0 || len(reg.broken) != 1 || reg.broken[0].name != "my-voice" ||
		!strings.Contains(reg.broken[0].reason, `"my-voice"`) || !strings.Contains(reg.broken[0].reason, "unknown placeholder {secret}") {
		t.Fatalf("a bad mapping was admitted, or set aside without naming itself: %v %+v", err, reg)
	}
	if reg.broken[0].repair != nil {
		t.Fatalf("a repair was offered for speech settings no release ships for this name: %+v", reg.broken[0].repair)
	}
}

// .
// .
func TestTheDirectoryMarksSpeechOnlyEntriesAndNeverProbesThem(t *testing.T) {
	var probed atomic.Int32
	speechSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probed.Add(1) }))
	defer speechSrv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	reg := `{"providers":[{"name":"chatty","url":"http://127.0.0.1:1/v1","default_model":"m","api_key":"k"},
		{"name":"ElevenLabs","url":"` + speechSrv.URL + `","api_key":"el"}]}`
	if err := os.WriteFile(path, []byte(reg), 0o600); err != nil {
		t.Fatal(err)
	}
	infos := a.providerDirectoryLive()
	byName := map[string]bool{}
	var elevenSpeech bool
	for _, p := range infos {
		byName[p.Name] = p.Chat
		if p.Name == "ElevenLabs" {
			elevenSpeech = p.Speech != nil && p.Speech.TTS != nil && p.Speech.TTS.VoiceRequired
		}
	}
	if byName["ElevenLabs"] || !byName["chatty"] {
		t.Fatalf("chat flags = %v", byName)
	}
	if !elevenSpeech {
		t.Fatal("the page was not told ElevenLabs speaks and needs a voice")
	}
	if n := probed.Load(); n != 0 {
		t.Fatalf("a speech-only entry was probed as a substrate %d times", n)
	}
}

// .
// .
func TestTheSpeechPointerSpeaksItsVendorsMapping(t *testing.T) {
	var s seen
	srv := vendor(t, &s, "application/json", []byte(`{"results":{"channels":[{"alternatives":[{"transcript":"heard"}]}]}}`))
	defer srv.Close()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"Deepgram","url":"`+srv.URL+`","api_key":"dg"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.STT = STTConfig{Provider: "Deepgram", Model: "nova-3"}
	c, err := a.resolveSpeech()
	if err != nil || c == nil {
		t.Fatalf("resolve: %v", err)
	}
	res, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "heard" || s.path != "/v1/listen" || s.header.Get("Authorization") != "Token dg" {
		t.Fatalf("text %q path %s auth %q — the runtime did not use the vendor's mapping", res.Text, s.path, s.header.Get("Authorization"))
	}
}

// .
// .
func TestASpeechBlockIsNotPartOfTheProbeKey(t *testing.T) {
	a := shippedEntry(t, "OpenAI")
	b := a
	b.Speech = nil
	if a.Speech == nil || probeKey(a) != probeKey(b) {
		t.Fatal("a speech block changed the probe key")
	}
}

// .
// .
// .
func TestAScaffoldLendsSpeechMappingsInsteadOfCopyingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"speech"`) {
		t.Error("the scaffold copied speech mappings into the operator's file")
	}
	shipped := embeddedRegistry().Providers
	if len(reg.Providers) != len(shipped) {
		t.Fatalf("the scaffold carries %d entries, the shipped registry %d", len(reg.Providers), len(shipped))
	}
	for i, e := range shipped {
		want, _ := json.Marshal(e.Speech)
		got, _ := json.Marshal(reg.Providers[i].Speech)
		if reg.Providers[i].Name != e.Name || string(got) != string(want) {
			t.Errorf("%s: a scaffolded install does not have the shipped speech block", e.Name)
		}
	}
}
