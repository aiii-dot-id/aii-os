package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func wav(first int16) []byte {
	pcm := make([]byte, 3200)
	binary.LittleEndian.PutUint16(pcm, uint16(first))
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], 16000)
	binary.LittleEndian.PutUint32(h[28:], 32000)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}

// corpus writes a two-clip frozen corpus: one speech clip and one of silence.
func corpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	type clip struct {
		File       string `json:"file"`
		SHA256     string `json:"sha256"`
		Speech     bool   `json:"speech"`
		Transcript string `json:"transcript,omitempty"`
		Condition  string `json:"condition"`
	}
	var clips []clip
	for i, c := range []clip{{File: "a.wav", Speech: true, Transcript: "check the ledger", Condition: "quiet"}, {File: "b.wav", Condition: "nonspeech"}} {
		blob := wav(int16(i + 1))
		if err := os.WriteFile(filepath.Join(dir, c.File), blob, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(blob)
		c.SHA256 = hex.EncodeToString(sum[:])
		clips = append(clips, c)
	}
	manifest, _ := json.Marshal(map[string]any{"corpus": "cmd-test", "frozen": "2026-10-02", "vocabulary": []string{"ledger"},
		"contract": map[string]any{"max_wer": map[string]float64{"": 0.25}, "min_term_recall": 0.95, "min_term_occurrences": 1,
			"max_hallucination": 0.0, "max_rtf_p95": 100.0, "max_failure_rate": 0.0},
		"clips": clips})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// endpoint answers the OpenAI dialect, one scripted transcript per request, in order.
func endpoint(t *testing.T, texts ...string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(texts) {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": texts[i]})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func exec(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, func(k string) string {
		if k == "SPEECH_EVAL_KEY" {
			return "secret-key-value"
		}
		return ""
	})
	return code, out.String(), errOut.String()
}

func TestAnEndpointThatClearsTheBarExitsZeroAndTheReportEndsInPass(t *testing.T) {
	srv := endpoint(t, "check the ledger", "")
	rep := filepath.Join(t.TempDir(), "report.txt")
	code, out, errOut := exec("-corpus", corpus(t), "-endpoint", srv.URL, "-model", "m", "-language", "en", "-report", rep)
	if code != exitPass || !strings.HasSuffix(strings.TrimSpace(out), "VERDICT: PASS") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	saved, err := os.ReadFile(rep)
	if err != nil || !strings.Contains(string(saved), "VERDICT: PASS") {
		t.Fatalf("the report file must hold the same report: %v %q", err, saved)
	}
	for _, want := range []string{"target " + srv.URL, `model "m"`, `language "en"`} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("the saved report must name what it measured (%q):\n%s", want, saved)
		}
	}
}

func TestAnInventedWordInSilenceExitsOneAndNamesTheClip(t *testing.T) {
	srv := endpoint(t, "check the ledger", "thank you")
	code, out, _ := exec("-corpus", corpus(t), "-endpoint", srv.URL)
	if code != exitFail || !strings.Contains(out, "invented: b.wav") || !strings.Contains(out, "VERDICT: FAIL") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestUsageAndConfigurationErrorsExitTwo(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":          nil,
		"no endpoint":           {"-corpus", t.TempDir()},
		"a corpus with nothing": {"-corpus", t.TempDir(), "-endpoint", "http://127.0.0.1:1"},
		"an empty key variable": {"-corpus", t.TempDir(), "-endpoint", "http://127.0.0.1:1", "-key-env", "NOT_SET"},
	} {
		if code, _, _ := exec(args...); code != exitError {
			t.Errorf("%s: exit %d, want %d", name, code, exitError)
		}
	}
}

func TestTheKeyIsSentButNeverPrinted(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]string{"text": ""})
	}))
	defer srv.Close()
	_, out, errOut := exec("-corpus", corpus(t), "-endpoint", srv.URL, "-key-env", "SPEECH_EVAL_KEY")
	if got, _ := auth.Load().(string); got != "Bearer secret-key-value" {
		t.Fatalf("the endpoint must receive the key as a bearer token, got %q", got)
	}
	if strings.Contains(out, "secret-key-value") || strings.Contains(errOut, "secret-key-value") {
		t.Fatalf("the key was printed:\n%s\n%s", out, errOut)
	}
}
