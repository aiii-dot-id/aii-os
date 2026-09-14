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

// .
// .
// .
// .
// .
// .
// .
// .
// .

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

	// .
	// .
	// .
	// .
	// .
	// .
	c := New(Config{Endpoint: srv.URL + "/v1", Model: "whisper-1", Language: "en"})
	res, err := c.Transcribe(context.Background(), make([]byte, 3200), 48000, 1)
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	// .
	// .
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

	// .
	// .
	if len(file) < 44 {
		t.Fatalf("the file part is %d bytes — too short to carry a header", len(file))
	}
	if string(file[0:4]) != "RIFF" || string(file[8:12]) != "WAVE" || string(file[12:16]) != "fmt " {
		t.Fatalf("the file part is not a WAV: %q", file[:16])
	}
	if got := binary.LittleEndian.Uint32(file[24:28]); got != 48000 {
		t.Fatalf("header says %d Hz, want 48000 — a wrong rate is a silently sped-up transcript", got)
	}
	// .
	// .
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

// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
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

// .
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

// .
// .
func TestSilenceIsRefusedBeforeTheNetwork(t *testing.T) {
	c := New(Config{Endpoint: "http://127.0.0.1:1", Model: "m"})
	if _, err := c.Transcribe(context.Background(), nil, 16000, 1); err == nil {
		t.Fatal("empty audio was sent to the network")
	}
	if _, err := c.Transcribe(context.Background(), make([]byte, 320), 0, 1); err == nil {
		t.Fatal("a zero sample rate was sent to the network")
	}
}

// .
// .
// .
// .
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
