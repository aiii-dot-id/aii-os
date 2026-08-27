package speech

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stt_test.go — the wire, against a server that checks what it received.
//
// THE POINT OF THESE IS THE DIALECT. A transcription client that builds
// a request no real engine accepts is a client that passes every unit
// test and fails on the first spoken word, which is exactly the class of
// failure that reached the operator three times in one day from
// internal/llm. So this stands up an HTTP server that asserts what
// whisper.cpp, faster-whisper, vLLM and OpenAI all require: multipart,
// a file part, a model field, and a WAV a sniffer will recognise.

func recordingEngine(t *testing.T, reply string, status int, seen *http.Request, body *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("the engine could not parse the request as multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no 'file' part — every engine in this family requires one: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		*body = b
		*seen = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
}

func TestTranscribeSpeaksTheDialectEveryEngineAccepts(t *testing.T) {
	var seen http.Request
	var file []byte
	srv := recordingEngine(t, `{"text":"  the ledger and the outbox disagree  ","language":"en"}`, 200, &seen, &file)
	defer srv.Close()

	// 48000 ON PURPOSE, NOT 16000. A browser's AudioContext hands you
	// whatever the device decided, and 48000 is the common answer — so
	// the rate written into the header must be the one PASSED IN. A
	// fixture at the value a bug would hardcode proves nothing: this
	// test passed against a wrapWAV that ignored its argument entirely
	// until the fixture stopped agreeing with it.
	c := New(Config{Endpoint: srv.URL + "/v1", Model: "whisper-1", Language: "en"})
	res, err := c.Transcribe(context.Background(), make([]byte, 3200), 48000, 1)
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	// TRIMMED, because engines pad. An utterance rendered with leading
	// whitespace becomes a conversation turn with leading whitespace.
	if res.Text != "the ledger and the outbox disagree" {
		t.Fatalf("text = %q — the engine's padding was not trimmed", res.Text)
	}
	if res.Language != "en" {
		t.Fatalf("language = %q", res.Language)
	}
	if got := seen.FormValue("model"); got != "whisper-1" {
		t.Fatalf("model field = %q — an engine with no model name refuses", got)
	}
	if got := seen.FormValue("language"); got != "en" {
		t.Fatalf("language field = %q", got)
	}
	if !strings.HasPrefix(seen.Header.Get("Content-Type"), "multipart/form-data") {
		t.Fatalf("content type = %q", seen.Header.Get("Content-Type"))
	}

	// AND IT IS A WAV A SNIFFER WILL RECOGNISE. These servers do not
	// take headerless PCM; they take a file and look at its first bytes.
	if len(file) < 44 {
		t.Fatalf("the file part is %d bytes — too short to carry a header", len(file))
	}
	if string(file[0:4]) != "RIFF" || string(file[8:12]) != "WAVE" || string(file[12:16]) != "fmt " {
		t.Fatalf("the file part is not a WAV: %q", file[:16])
	}
	if got := binary.LittleEndian.Uint32(file[24:28]); got != 48000 {
		t.Fatalf("header says %d Hz, want 48000 — a wrong rate is a silently sped-up transcript", got)
	}
	// The derived fields have to follow the rate too, or a player reads
	// the right samples at the wrong speed.
	if got := binary.LittleEndian.Uint32(file[28:32]); got != 48000*2 {
		t.Fatalf("byte rate = %d, want %d", got, 48000*2)
	}
	if got := binary.LittleEndian.Uint16(file[32:34]); got != 2 {
		t.Fatalf("block align = %d, want 2 (mono, 16-bit)", got)
	}
	if got := binary.LittleEndian.Uint16(file[22:24]); got != 1 {
		t.Fatalf("header says %d channels, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(file[40:44]); int(got) != 3200 {
		t.Fatalf("header declares %d data bytes, want 3200 — a wrong length truncates the last words", got)
	}
	if len(file) != 44+3200 {
		t.Fatalf("file is %d bytes, want %d", len(file), 44+3200)
	}
}

// AND THE FORMAT IS WHATEVER WAS PASSED, not whatever is common. Two
// channels at an odd rate: every derived field has to move with it.
func TestTheWAVHeaderDescribesTheAudioItWasGiven(t *testing.T) {
	var seen http.Request
	var file []byte
	srv := recordingEngine(t, `{"text":"x"}`, 200, &seen, &file)
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "m"})
	if _, err := c.Transcribe(context.Background(), make([]byte, 800), 44100, 2); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(file[22:24]); got != 2 {
		t.Fatalf("channels = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(file[24:28]); got != 44100 {
		t.Fatalf("rate = %d, want 44100", got)
	}
	if got := binary.LittleEndian.Uint16(file[32:34]); got != 4 {
		t.Fatalf("block align = %d, want 4 (stereo, 16-bit)", got)
	}
	if got := binary.LittleEndian.Uint32(file[28:32]); got != 44100*4 {
		t.Fatalf("byte rate = %d, want %d", got, 44100*4)
	}
}

// NO CREDENTIAL MEANS NO HEADER. An empty bearer is not "no credential",
// it is a malformed one, and a local engine that ignores auth entirely
// may still reject it — which turns a working local setup into a 401
// nobody can explain.
func TestNoKeyMeansNoAuthorizationHeader(t *testing.T) {
	var seen http.Request
	var file []byte
	srv := recordingEngine(t, `{"text":"x"}`, 200, &seen, &file)
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "m"})
	if _, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1); err != nil {
		t.Fatal(err)
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Fatalf("an empty key was sent as %q", got)
	}

	c = New(Config{Endpoint: srv.URL, Model: "m", APIKey: "sk-test"})
	if _, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1); err != nil {
		t.Fatal(err)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
}

// THE ENGINE'S OWN WORDS REACH THE OPERATOR. The likely failures here —
// a wrong model name, an endpoint that is something else entirely — are
// only fixable by someone who can see what the server actually said.
func TestARefusalCarriesWhatTheEngineSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'whisper-9' does not exist"}`))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "whisper-9"})
	_, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1)
	if err == nil {
		t.Fatal("a 404 was treated as a transcription")
	}
	if !strings.Contains(err.Error(), "whisper-9 ") && !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("the engine's reason did not survive: %v", err)
	}
}

// An operator who wrote the whole path meant it.
func TestAFullPathIsUsedAsGiven(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://x/v1", "http://x/v1/audio/transcriptions"},
		{"http://x/v1/", "http://x/v1/audio/transcriptions"},
		{"http://x/v1/audio/transcriptions", "http://x/v1/audio/transcriptions"},
	} {
		if got := New(Config{Endpoint: tc.in}).Endpoint(); got != tc.want {
			t.Errorf("%q resolved to %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Nothing to transcribe is refused before a request is built: a round
// trip that can only return silence is a round trip worth not making.
func TestSilenceIsRefusedBeforeTheNetwork(t *testing.T) {
	c := New(Config{Endpoint: "http://127.0.0.1:1", Model: "m"})
	if _, err := c.Transcribe(context.Background(), nil, 16000, 1); err == nil {
		t.Fatal("empty audio was sent to the network")
	}
	if _, err := c.Transcribe(context.Background(), make([]byte, 320), 0, 1); err == nil {
		t.Fatal("a zero sample rate was sent to the network")
	}
}

// A RESPONSE THAT IS NOT A TRANSCRIPTION IS NOT A TRANSCRIPT. Pointing
// at the wrong port reaches something that answers 200 with HTML, and
// treating that as words would put a web page into the identity's
// conversation as though a person had said it aloud.
func TestSomethingThatIsNotAnEngineIsNotBelieved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>hello</body></html>"))
	}))
	defer srv.Close()

	c := New(Config{Endpoint: srv.URL, Model: "m"})
	if _, err := c.Transcribe(context.Background(), make([]byte, 320), 16000, 1); err == nil {
		t.Fatal("a web page was accepted as a transcription")
	}
}
