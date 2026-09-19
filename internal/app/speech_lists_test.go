package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// .
func TestNothingShipsAVendorsCatalogue(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(embeddedProviders, &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw["providers"].([]any) {
		entry := e.(map[string]any)
		sp, ok := entry["speech"].(map[string]any)
		if !ok {
			continue
		}
		for dir, o := range sp {
			for _, catalogue := range []string{"models", "voices"} {
				if _, found := o.(map[string]any)[catalogue]; found {
					t.Errorf("%s speech.%s ships a list of %s", entry["name"], dir, catalogue)
				}
			}
		}
	}
	// .
	// .
	// .
	bad := `{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","speech":{"tts":{"voices":[{"id":"x"}]}}}]}`
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil || len(reg.Providers) != 0 || len(reg.broken) != 1 ||
		reg.broken[0].name != "my-voice" || !strings.Contains(reg.broken[0].reason, "voices") {
		t.Errorf("an entry carrying a catalogue was admitted, or not set aside by name: %v %+v", err, reg)
	}
}

// .
// .
// .
// .
func TestEveryOfferedDirectionListsWhatItsAPIReads(t *testing.T) {
	speaking := 0
	for _, e := range embeddedRegistry().Providers {
		for _, d := range []speech.Direction{speech.STT, speech.TTS} {
			o := e.Speech.offer(d)
			if o == nil {
				continue
			}
			speaking++
			if o.Uses(d, "model") && o.ListModels == nil {
				t.Errorf("%s %s demands a model and lists none", e.Name, d)
			}
			if d == speech.TTS && o.Uses(d, "voice") && o.ListVoices == nil {
				t.Errorf("%s %s demands a voice and lists none", e.Name, d)
			}
		}
	}
	if speaking < 6 {
		t.Fatalf("only %d shipped directions speak — this test proves little", speaking)
	}
}

// .
// .
// .
// .
func TestShippedSpeechListsAreTheVendorsShapes(t *testing.T) {
	const elevenModels = `[{"model_id":"eleven_flash_v2_5","name":"Eleven Ember v2.5","can_do_text_to_speech":true,
			"languages":[{"language_id":"en","name":"English"},{"language_id":"de","name":"German"}]},
		{"model_id":"eleven_english_sts_v2","name":"Eleven English v2","can_do_text_to_speech":false,"languages":[{"language_id":"en","name":"English"}]}]`
	const elevenVoices = `{"voices":[{"voice_id":"21m00Tcm4TlvDq8ikWAM","name":"Rachel","category":"premade",
			"labels":{"accent":"american","gender":"female","age":"young"},"verified_languages":[{"language":"en","locale":"en-US"}]}],
		"has_more":false,"total_count":21,"next_page_token":null}`
	const deepgram = `{"stt":[{"name":"nova-3","canonical_name":"nova-3-general","languages":["en","es"],"batch":true,"streaming":true},
			{"name":"nova-3","canonical_name":"nova-3-general","languages":["en"],"batch":true},
			{"name":"streaming","canonical_name":"live-only","languages":["en"],"batch":false}],
		"tts":[{"name":"thalia","canonical_name":"aura-2-thalia-en","languages":["en","en-US"],
			"metadata":{"display_name":"Thalia","accent":"American","age":"Adult","sample":"https://static.deepgram.test/thalia.wav"}}]}`
	const mistralModels = `{"object":"list","data":[{"id":"voxtral-mini-latest","name":"Voxtral Mini","capabilities":{"audio_transcription":true,"audio_speech":false}},
		{"id":"voxtral-mini-tts-2603","name":"Voxtral TTS","capabilities":{"audio_transcription":false,"audio_speech":true}},
		{"id":"chat-model","name":"Chat","capabilities":{"completion_chat":true}}]}`
	for _, tc := range []struct {
		vendor              string
		dir                 speech.Direction
		voices              bool
		reply               string
		path, query         string
		header, headerValue string
		want                string
	}{
		{"OpenAI", speech.STT, false, `{"object":"list","data":[{"id":"gpt-4o-transcribe"},{"id":"gpt-realtime-whisper"},{"id":"whisper-1"},{"id":"chat-model"},{"id":"gpt-4o-mini-tts"}]}`,
			"/v1/models", "", "Authorization", "Bearer k", "[{gpt-4o-transcribe  [] } {whisper-1  [] }]"},
		{"Groq", speech.STT, false, `{"object":"list","data":[{"id":"whisper-large-v3"},{"id":"canopylabs/orpheus-v1-english"},{"id":"chat-model"}]}`,
			"/openai/v1/models", "", "Authorization", "Bearer k", "[{whisper-large-v3  [] }]"},
		{"Google Gemini", speech.STT, false, `{"models":[{"name":"models/gemini-3.1-pro","displayName":"Gemini 3.1 Pro"},{"name":"models/gemini-2.5-flash-tts","displayName":"TTS"},{"name":"models/text-embedding-004","displayName":"Embedding"}]}`,
			"/v1beta/models", "pageSize=1000", "x-goog-api-key", "k", "[{gemini-3.1-pro Gemini 3.1 Pro [] }]"},
		{"ElevenLabs", speech.TTS, false, elevenModels, "/v1/models", "", "xi-api-key", "k",
			"[{eleven_flash_v2_5 Eleven Ember v2.5 [{en English} {de German}] }]"},
		{"ElevenLabs", speech.TTS, true, elevenVoices, "/v2/voices", "page_size=100", "xi-api-key", "k",
			"[{21m00Tcm4TlvDq8ikWAM Rachel [{en }] american · female · young}]"},
		{"Deepgram", speech.STT, false, deepgram, "/v1/models", "", "Authorization", "Token k",
			"[{nova-3-general nova-3 [{en } {es }] }]"},
		{"Deepgram", speech.TTS, false, deepgram, "/v1/models", "", "Authorization", "Token k",
			"[{aura-2-thalia-en Thalia [{en } {en-US }] American · Adult}]"},
		{"Cartesia", speech.TTS, true, `{"data":[{"id":"db6b0ed5-d5d3-463d-ae85-518a07d3c2b4","name":"Skylar","language":"en","gender":"feminine"}],"has_more":false,"next_page":null}`,
			"/voices", "limit=100", "Cartesia-Version", "2026-08-14",
			"[{db6b0ed5-d5d3-463d-ae85-518a07d3c2b4 Skylar [{en }] feminine}]"},
		{"Mistral", speech.STT, false, mistralModels, "/v1/models", "", "Authorization", "Bearer k", "[{voxtral-mini-latest Voxtral Mini [] }]"},
		{"Mistral", speech.TTS, false, mistralModels, "/v1/models", "", "Authorization", "Bearer k", "[{voxtral-mini-tts-2603 Voxtral TTS [] }]"},
		{"Mistral", speech.TTS, true, `{"items":[{"id":"c3a1e0f2","name":"Paul","gender":"male","age":"adult","languages":["en","fr"]}],"total":1,"page":1,"page_size":100,"total_pages":1}`,
			"/v1/audio/voices", "limit=100", "Authorization", "Bearer k", "[{c3a1e0f2 Paul [{en } {fr }] male · adult}]"},
		{"Hume", speech.TTS, true, `{"page_number":0,"page_size":100,"total_pages":1,"voices_page":[{"id":"d8ab67c6-953d-4bd8-9370-8fa53a0f1453","name":"Colton Rivers","provider":"HUME_AI","compatible_octave_models":["1","2"]}]}`,
			"/v0/tts/voices", "page_size=100&provider=HUME_AI", "X-Hume-Api-Key", "k",
			"[{d8ab67c6-953d-4bd8-9370-8fa53a0f1453 Colton Rivers [] }]"},
	} {
		kind := "models"
		if tc.voices {
			kind = "voices"
		}
		t.Run(tc.vendor+"/"+tc.dir.String()+"/"+kind, func(t *testing.T) {
			var s seen
			srv := vendor(t, &s, "application/json", []byte(tc.reply))
			defer srv.Close()
			e := shippedEntry(t, tc.vendor)
			o := e.Speech.offer(tc.dir)
			if o == nil {
				t.Fatalf("%s no longer speaks %s", tc.vendor, tc.dir)
			}
			l := o.ListModels
			if tc.voices {
				l = o.ListVoices
			}
			if l == nil {
				t.Fatalf("%s lists no %s for %s", tc.vendor, kind, tc.dir)
			}
			// .
			// .
			ask := *o
			if ask.Base != "" {
				ask.Base = retarget(t, ask.Base, srv.URL)
			}
			items, whole, err := ask.ListItems(context.Background(), tc.dir, l, retarget(t, e.URL, srv.URL), "k", 0, speech.Ask{})
			if err != nil {
				t.Fatal(err)
			}
			want(t, s.method == http.MethodGet && s.path == tc.path && s.query == tc.query, "asked %s %s?%s", s.method, s.path, s.query)
			want(t, s.header.Get(tc.header) == tc.headerValue, "%s = %q", tc.header, s.header.Get(tc.header))
			want(t, fmt.Sprint(items) == tc.want, "items %v", items)
			want(t, whole, "the list did not read as complete")
		})
	}
}

// .
// .
// .
// .
func TestSpeechListsAreReadLiveWithTheEntrysKey(t *testing.T) {
	var asked atomic.Int32
	var voicesQuery atomic.Value
	voicesQuery.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path == "/v2/voices" {
			voicesQuery.Store(r.URL.RawQuery)
		}
		if r.Header.Get("xi-api-key") != "el-key-1234" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v2/voices":
			_, _ = w.Write([]byte(`{"voices":[{"voice_id":"v-1","name":"Rachel","labels":{"accent":"american"},"verified_languages":[{"language":"en"}]},
				{"voice_id":"v-2","name":"Adam","verified_languages":[{"language":"de"}]}],"has_more":false}`))
		case "/v1/models":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":{"message":"this key lacks the models_read permission"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a, path := speechSettingsApp(t, srv.URL)

	got, err := a.speechLists("ElevenLabs", "tts", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.VoicesListed || !got.VoicesComplete || len(got.Voices) != 2 || got.Voices[0].Name != "Rachel" || got.Voices[0].Detail != "american" {
		t.Errorf("voices read %+v", got.Voices)
	}
	if fmt.Sprint(got.Languages) != "[{de } {en }]" {
		t.Errorf("the languages the service named read %v", got.Languages)
	}
	if got.ModelsListed || got.ModelsError != "ElevenLabs refused the list (403): this key lacks the models_read permission" {
		t.Errorf("models read %+v", got)
	}

	// .
	// .
	if got, err = a.speechLists("ElevenLabs", "tts", "barber", "en", ""); err != nil {
		t.Fatal(err)
	}
	if got.Search != "barber" || got.Language != "en" || !strings.Contains(voicesQuery.Load().(string), "search=barber") {
		t.Errorf("the search did not reach the vendor: %q %+v", voicesQuery.Load(), got)
	}

	before := asked.Load()
	t.Setenv("ELEVENLABS_API_KEY", "")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"`+srv.URL+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = a.speechLists("ElevenLabs", "tts", "", "", "")
	if err != nil || !got.NeedsKey || asked.Load() != before || got.VoicesError != "" {
		t.Errorf("a service with no key was asked, or did not say what it needs: %+v %v", got, err)
	}

	for _, tc := range []struct{ why, name, dir string }{
		{"a provider that is not a speech service", "not-a-vendor", "tts"},
		{"a direction the service does not speak", "Hume", "stt"},
		{"a direction that is not one", "ElevenLabs", "sideways"},
	} {
		if _, err := a.speechLists(tc.name, tc.dir, "", "", ""); err == nil {
			t.Errorf("%s was answered", tc.why)
		}
	}
}

// .
// .
// .
func TestAnOwnServerListsWhatItServes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"whisper-1"},{"id":"kokoro"}]}`))
		case "/v1/audio/voices":
			_, _ = w.Write([]byte(`{"voices":[{"id":"af_bella","name":"Bella"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	vendorSrv, _ := speakingVendor(t, "", nil)
	a, _ := speechSettingsApp(t, vendorSrv.URL)
	name := "OpenAI-compatible · " + strings.TrimPrefix(srv.URL, "http://")
	if err := a.setSpeechService(name, "", srv.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	got, err := a.speechLists(name, "tts", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "whisper-1" || len(got.Voices) != 1 || got.Voices[0].Name != "Bella" || got.NeedsKey {
		t.Fatalf("the operator's own server answered %+v", got)
	}
	// .
	if got, err = a.speechLists(name, "stt", "", "", ""); err != nil || len(got.Models) != 2 || got.Models[0].ID != "whisper-1" {
		t.Fatalf("the operator's own server lists nothing to hear with: %+v %v", got, err)
	}
}

// .
// .
// .
func TestAPublishedSpecIsReadOnceAndRemembered(t *testing.T) {
	var fetched atomic.Int32
	spec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"components":{"voices":["alloy","ash"]}}`))
	}))
	defer spec.Close()
	vendorSrv, _ := speakingVendor(t, "", nil)
	a, path := speechSettingsApp(t, vendorSrv.URL)
	own := `{"providers":[{"name":"my-speech","url":"` + vendorSrv.URL + `","api_key":"k","speech":{"tts":{
		"list_voices":{"spec":"` + spec.URL + `/openapi.json","items":"$.components.voices","id":"$"}}}}]}`
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		got, err := a.speechLists("my-speech", "tts", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Voices) != 2 || got.Voices[0].ID != "alloy" || !got.VoicesComplete {
			t.Fatalf("read %d: %+v", i, got)
		}
	}
	if fetched.Load() != 1 {
		t.Errorf("the specification was fetched %d times", fetched.Load())
	}
}

// .
// .
// .
func TestATypedKeyAsksWithoutBeingStored(t *testing.T) {
	var sawKey atomic.Value
	sawKey.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey.Store(r.Header.Get("xi-api-key"))
		if r.URL.Path == "/v2/voices" {
			_, _ = w.Write([]byte(`{"voices":[{"voice_id":"v-1","name":"Rachel"}]}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	a, path := speechSettingsApp(t, srv.URL)
	t.Setenv("ELEVENLABS_API_KEY", "")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"`+srv.URL+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.speechLists("ElevenLabs", "tts", "", "", "typed-by-the-operator")
	if err != nil {
		t.Fatal(err)
	}
	if got.NeedsKey || len(got.Voices) != 1 || sawKey.Load().(string) != "typed-by-the-operator" {
		t.Fatalf("the typed key was not the one asked with: %+v, vendor saw %q", got, sawKey.Load())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || strings.Contains(string(after), "typed-by-the-operator") {
		t.Errorf("asking with a typed key wrote it down:\n%s", after)
	}
}

// .
// .
// .
// .
// .
// .
func TestAShippedSpeechServiceResolvesBeforeItIsAdded(t *testing.T) {
	reg := &providerRegistry{Providers: []providerEntry{{Name: "Cartesia", URL: "http://127.0.0.1:9/mine"}}}
	mine := speechEntryNamed(reg, "Cartesia")
	if mine == nil || mine.URL != "http://127.0.0.1:9/mine" {
		t.Fatalf("the file's own entry did not win: %+v", mine)
	}
	empty := &providerRegistry{}
	shipped := speechEntryNamed(empty, "Cartesia")
	if shipped == nil || shipped.Speech == nil || shipped.Speech.offer(speech.TTS) == nil {
		t.Fatalf("a shipped speech service did not resolve before it was added: %+v", shipped)
	}
	if shipped.URL == "" {
		t.Fatal("the shipped service resolved without the address it answers at")
	}
	if entryNamed(empty, "Cartesia") != nil {
		t.Fatal("the shipped entry was written into this install's registry")
	}
	if speechEntryNamed(empty, "not-a-service") != nil {
		t.Fatal("a name no one ships resolved to something")
	}
}

// .
// .
// .
func TestAShippedServiceWithNoKeyAsksForOne(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"Local","url":"http://127.0.0.1:9"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	shipped := speechEntryNamed(&providerRegistry{}, "Cartesia")
	if shipped == nil {
		t.Skip("no shipped service to stand for this rule")
	}
	if shipped.APIKeyEnv != "" {
		t.Setenv(shipped.APIKeyEnv, "")
	}
	got, err := a.speechLists("Cartesia", "tts", "", "", "")
	if err != nil {
		t.Fatalf("a shipped service was refused before it was added: %v", err)
	}
	if !got.NeedsKey || len(got.Voices) != 0 {
		t.Fatalf("the pickers did not ask for a key: %+v", got)
	}
}
